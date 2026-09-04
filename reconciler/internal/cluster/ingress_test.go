package cluster

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/inception42/cortex/shared"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestAppHost(t *testing.T) {
	if got := appHost("shop", "apps.example.com"); got != "shop.apps.example.com" {
		t.Fatalf("host: %q", got)
	}
	if got := appHost("shop", "  apps.example.com  "); got != "shop.apps.example.com" {
		t.Fatalf("host should trim domain: %q", got)
	}
	if got := appHost("shop", ""); got != "" {
		t.Fatalf("empty domain should be host-less, got %q", got)
	}
	if got := appHost("shop", "   "); got != "" {
		t.Fatalf("blank domain should be host-less, got %q", got)
	}
}

func TestAppRouteToDeclaredService(t *testing.T) {
	r := appRoute("shop", "tenant-ns", "app-123", "shop.apps.example.com", "shop-storefront", 8080)

	if got := r.GetAPIVersion(); got != "gateway.networking.k8s.io/v1" {
		t.Fatalf("apiVersion: %q", got)
	}
	if got := r.GetKind(); got != "HTTPRoute" {
		t.Fatalf("kind: %q", got)
	}
	if got := r.GetName(); got != "shop" || r.GetNamespace() != "tenant-ns" {
		t.Fatalf("name/ns: %q/%q", r.GetName(), r.GetNamespace())
	}

	// Managed (so GC finds it) but NOT system.
	labels := r.GetLabels()
	if labels[labelManaged] != "true" || labels[labelAppID] != "app-123" {
		t.Fatalf("labels = %v", labels)
	}
	if _, ok := labels[labelSystem]; ok {
		t.Fatalf("app route must not carry the system label: %v", labels)
	}

	// Attaches to the shared Gateway.
	parents, _, _ := unstructured.NestedSlice(r.Object, "spec", "parentRefs")
	p := parents[0].(map[string]any)
	if p["name"] != gatewayName || p["namespace"] != gatewayNS {
		t.Fatalf("parentRef = %v", p)
	}

	hosts, found, _ := unstructured.NestedStringSlice(r.Object, "spec", "hostnames")
	if !found || len(hosts) != 1 || hosts[0] != "shop.apps.example.com" {
		t.Fatalf("hostnames = %v", hosts)
	}

	rules, _, _ := unstructured.NestedSlice(r.Object, "spec", "rules")
	be := rules[0].(map[string]any)["backendRefs"].([]any)[0].(map[string]any)
	if be["name"] != "shop-storefront" {
		t.Fatalf("backend must target the declared Service, got %v", be["name"])
	}
	if be["port"] != int64(8080) {
		t.Fatalf("backend port should be 8080, got %v", be["port"])
	}
}

func TestAppRouteDefaultsPortAndHostless(t *testing.T) {
	r := appRoute("shop", "tenant-ns", "app-123", "", "shop-svc", 0)
	if _, found, _ := unstructured.NestedStringSlice(r.Object, "spec", "hostnames"); found {
		t.Fatalf("host-less route must omit hostnames")
	}
	rules, _, _ := unstructured.NestedSlice(r.Object, "spec", "rules")
	be := rules[0].(map[string]any)["backendRefs"].([]any)[0].(map[string]any)
	if be["port"] != int64(80) {
		t.Fatalf("port 0 must default to 80, got %v", be["port"])
	}
}

func TestGatewayBindsToALB(t *testing.T) {
	gw := gateway("", false)
	if got, _, _ := unstructured.NestedString(gw.Object, "spec", "gatewayClassName"); got != gatewayClass {
		t.Fatalf("gatewayClassName = %q", got)
	}
	ann := gw.GetAnnotations()
	if ann["alb.networking.azure.io/alb-name"] != albName || ann["alb.networking.azure.io/alb-namespace"] != gatewayNS {
		t.Fatalf("alb annotations = %v", ann)
	}
	alb := applicationLoadBalancer("/subscriptions/s/…/subnets/aks-appgateway")
	assoc, _, _ := unstructured.NestedSlice(alb.Object, "spec", "associations")
	if len(assoc) != 1 || assoc[0] != "/subscriptions/s/…/subnets/aks-appgateway" {
		t.Fatalf("associations = %v", assoc)
	}

	// Without a certificate there must be exactly one listener: an HTTPS listener
	// referencing a Secret that doesn't exist is rejected outright by AGC, which
	// would take the whole gateway down rather than just TLS.
	ls, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
	if len(ls) != 1 {
		t.Fatalf("expected only the HTTP listener before a cert exists, got %d", len(ls))
	}

	// With one, the wildcard HTTPS listener appears alongside it.
	gw = gateway("apps.contoso.com", true)
	ls, _, _ = unstructured.NestedSlice(gw.Object, "spec", "listeners")
	if len(ls) != 2 {
		t.Fatalf("expected http + https listeners, got %d", len(ls))
	}
	https, _ := ls[1].(map[string]any)
	if https["hostname"] != "*.apps.contoso.com" || https["port"] != int64(443) {
		t.Fatalf("https listener = %v", https)
	}
}

func TestAppHostFor(t *testing.T) {
	ing := &shared.IngressConfig{AppsDomain: "apps.contoso.com"}
	// An explicit label wins; otherwise the app's own name is used.
	if got := appHostFor(shared.DesiredApplication{Hostname: "shop"}, "todo", ing); got != "shop.apps.contoso.com" {
		t.Fatalf("explicit hostname = %q", got)
	}
	if got := appHostFor(shared.DesiredApplication{}, "todo", ing); got != "todo.apps.contoso.com" {
		t.Fatalf("defaulted hostname = %q", got)
	}
	// No delegated domain ⇒ nothing is published. This is the designed state,
	// not an error: apps are only reachable on a domain the tenant delegated.
	if got := appHostFor(shared.DesiredApplication{}, "todo", nil); got != "" {
		t.Fatalf("expected no host without a domain, got %q", got)
	}
}

func TestCookieSecretIsStableAndSized(t *testing.T) {
	// Must not change between reconciles — a rotating cookie key would sign every
	// user out on every sweep.
	a := cookieSecretFor("t-abc", "todo", "s3cret")
	if a != cookieSecretFor("t-abc", "todo", "s3cret") {
		t.Fatal("cookie secret is not deterministic")
	}
	// ...but must change when the client secret is rotated.
	if a == cookieSecretFor("t-abc", "todo", "rotated") {
		t.Fatal("cookie secret must change with the client secret")
	}
	// oauth2-proxy requires exactly 16, 24 or 32 bytes.
	if dec, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(a); err != nil || len(dec) != 32 {
		t.Fatalf("cookie secret must decode to 32 bytes, got %d (%v)", len(dec), err)
	}
}

func TestAuthRedirectURL(t *testing.T) {
	// Must be HTTPS and match the app's own host — oauth2-proxy rejects a
	// mismatch, and this is the value the customer registers on their app
	// registration.
	if got := authRedirectURL("todo.apps.contoso.com"); got != "https://todo.apps.contoso.com/oauth2/callback" {
		t.Fatalf("redirect url = %q", got)
	}
}

func TestAuthDeploymentDoesNotCallProfileURL(t *testing.T) {
	// Regression: oauth2-proxy resolves any claim missing from the ID token by
	// calling the provider's userinfo endpoint, using an access token minted for
	// the app's own API rather than for Graph — so Graph returns 401 and every
	// callback fails. Disabling the fallback per claim is whack-a-mole (`email`
	// was fixed first, then `groups` surfaced); it must be off outright, with
	// the email read from a claim Entra actually issues.
	d := authDeployment("todo-auth", "todo", shared.DesiredApplication{
		ExposeService: "todo-app", ExposePort: 80,
		OIDCScope: "api://11111111-1111-1111-1111-111111111111/Todo.Access",
	}, &shared.IngressConfig{
		OIDCIssuer:   "https://login.microsoftonline.com/tid/v2.0",
		OIDCClientID: "11111111-1111-1111-1111-111111111111",
	}, "todo.apps.example.com")

	cs, _, _ := unstructured.NestedSlice(d.Object, "spec", "template", "spec", "containers")
	if len(cs) == 0 {
		t.Fatal("no containers")
	}
	args, _, _ := unstructured.NestedStringSlice(cs[0].(map[string]any), "args")
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"--skip-claims-from-profile-url=true",
		"--oidc-email-claim=preferred_username",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s; got: %s", want, joined)
		}
	}
}

func TestOCIRegistryURL(t *testing.T) {
	cases := map[string]string{
		"ghcr.io/bensincs":           "ghcr.io/bensincs",
		"oci://ghcr.io/bensincs":     "ghcr.io/bensincs",
		"  ghcr.io/x  ":              "ghcr.io/x",
		"https://charts.example.com": "", // classic HTTP Helm repo
		"http://charts.example.com":  "",
		"":                           "",
	}
	for in, want := range cases {
		if got := ociRegistryURL(in); got != want {
			t.Errorf("ociRegistryURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOCISecretNameStable(t *testing.T) {
	a := ociSecretName("ghcr.io/bensincs")
	if a != ociSecretName("ghcr.io/bensincs") {
		t.Fatalf("name not stable for the same registry")
	}
	if a == ociSecretName("ghcr.io/other") {
		t.Fatalf("distinct registries must not collide")
	}
	if !strings.HasPrefix(a, "cortex-oci-") {
		t.Fatalf("unexpected name %q", a)
	}
}

// buildApplication passes the author's values through untouched and strips the
// oci:// scheme from the repoURL so it matches the auto-registered repo secret.
func TestBuildApplicationSource(t *testing.T) {
	app := shared.DesiredApplication{
		ID: "example-app", Namespace: "example",
		RepoURL: "oci://ghcr.io/bensincs/charts", Chart: "todo-app", TargetRevision: "0.1.0",
		Values: "database:\n  host: h\n",
	}
	u := buildApplication(app, appName(app.ID))
	if repo, _, _ := unstructured.NestedString(u.Object, "spec", "source", "repoURL"); repo != "ghcr.io/bensincs/charts" {
		t.Fatalf("repoURL = %q", repo)
	}
	if v, _, _ := unstructured.NestedString(u.Object, "spec", "source", "helm", "values"); v != app.Values {
		t.Fatalf("author values must be preserved, got %q", v)
	}
}

func TestHelmRepoSecretCreds(t *testing.T) {
	pub := helmRepoSecret("cortex-oci-x", "ghcr.io/bensincs", "", "")
	sd, _, _ := unstructured.NestedStringMap(pub.Object, "stringData")
	if sd["enableOCI"] != "true" || sd["type"] != "helm" || sd["url"] != "ghcr.io/bensincs" {
		t.Fatalf("stringData = %v", sd)
	}
	if _, ok := sd["username"]; ok {
		t.Fatalf("public repo must carry no username: %v", sd)
	}
	if _, ok := sd["password"]; ok {
		t.Fatalf("public repo must carry no password: %v", sd)
	}
	labels := pub.GetLabels()
	if labels["argocd.argoproj.io/secret-type"] != "repository" || labels[labelOCIRepo] != "true" {
		t.Fatalf("labels = %v", labels)
	}

	priv := helmRepoSecret("cortex-oci-y", "ghcr.io/private", "user", "pat")
	sd2, _, _ := unstructured.NestedStringMap(priv.Object, "stringData")
	if sd2["username"] != "user" || sd2["password"] != "pat" {
		t.Fatalf("private creds missing: %v", sd2)
	}
}

func TestChartUnpinned(t *testing.T) {
	// Argo rejects a Helm chart source with no targetRevision, so the reconciler
	// must catch it rather than stamping a spec that can never sync. A git
	// source (no chart) is unaffected — there targetRevision is optional.
	cases := []struct {
		name string
		app  shared.DesiredApplication
		want bool
	}{
		{"chart with no version", shared.DesiredApplication{Chart: "nginx"}, true},
		{"chart with blank version", shared.DesiredApplication{Chart: "nginx", TargetRevision: "  "}, true},
		{"chart pinned", shared.DesiredApplication{Chart: "nginx", TargetRevision: "15.14.0"}, false},
		{"no chart at all", shared.DesiredApplication{}, false},
	}
	for _, c := range cases {
		if got := chartUnpinned(c.app); got != c.want {
			t.Errorf("%s: chartUnpinned = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestArgoMessageIgnoresWedgedHookOnSettledApp(t *testing.T) {
	// ingress-nginx (and any chart whose hooks use hook-delete-policy:
	// hook-succeeded) leaves the operation stuck on "waiting for completion of
	// hook ..." forever: Argo deletes the succeeded hook Job and then waits to
	// observe its own deletion. The app is Synced, Healthy and serving, so that
	// message must not be reported as the deployment's state.
	settled := map[string]any{"status": map[string]any{
		"sync":   map[string]any{"status": "Synced"},
		"health": map[string]any{"status": "Healthy"},
		"operationState": map[string]any{
			"phase":   "Running",
			"message": "waiting for completion of hook batch/Job/jenkins-ingress-nginx-admission-patch",
		},
	}}
	if got := argoMessage(settled); got != "" {
		t.Errorf("a synced, healthy app must report nothing, got %q", got)
	}

	// While it genuinely is not settled, the same message is the most useful
	// thing to show — it says exactly what the sync is waiting on.
	pending := map[string]any{"status": map[string]any{
		"sync":   map[string]any{"status": "OutOfSync"},
		"health": map[string]any{"status": "Progressing"},
		"operationState": map[string]any{
			"phase":   "Running",
			"message": "waiting for completion of hook batch/Job/x",
		},
	}}
	if got := argoMessage(pending); got == "" {
		t.Error("an unsettled app should still report what the sync is waiting on")
	}

	// A real health problem always wins, settled or not.
	unhealthy := map[string]any{"status": map[string]any{
		"sync":   map[string]any{"status": "Synced"},
		"health": map[string]any{"status": "Degraded", "message": "container crash-looping"},
	}}
	if got := argoMessage(unhealthy); got != "container crash-looping" {
		t.Errorf("health message must win, got %q", got)
	}
}

func TestImagePullSecret(t *testing.T) {
	// Argo pulls the chart with the tenant's registry token; the images inside it
	// are pulled by the kubelet, which has none — so a chart whose images live in
	// the platform registry deploys and then ImagePullBackOffs without this.
	sec, err := imagePullSecret("todo", "reg.azurecr.io", "tok", "pw")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got, _, _ := unstructured.NestedString(sec.Object, "type"); got != "kubernetes.io/dockerconfigjson" {
		t.Fatalf("type = %q", got)
	}
	enc, _, _ := unstructured.NestedString(sec.Object, "data", ".dockerconfigjson")
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var cfg struct {
		Auths map[string]struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Auth     string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("parse: %v", err)
	}
	e, ok := cfg.Auths["reg.azurecr.io"]
	if !ok {
		t.Fatalf("no entry for the registry: %v", cfg.Auths)
	}
	if e.Username != "tok" || e.Password != "pw" {
		t.Errorf("credentials = %q/%q", e.Username, e.Password)
	}
	// Some runtimes read only `auth`, so it has to be present too.
	d, _ := base64.StdEncoding.DecodeString(e.Auth)
	if string(d) != "tok:pw" {
		t.Errorf("auth = %q", d)
	}
}

func TestRegistryAuthOverridesDeploymentValues(t *testing.T) {
	// The credential arrives on every sync so a rotation takes effect on the
	// next poll. Baked-in values are only a fallback: a control plane that sends
	// one must win, or the cluster keeps using a token the registry has already
	// invalidated — which is exactly the failure this replaced.
	base := Options{HelmOCIRegistry: "old.azurecr.io", HelmOCIUsername: "old", HelmOCIPassword: "stale"}
	c := &Client{o: base}

	fresh := &shared.RegistryAuth{LoginServer: "new.azurecr.io", Username: "tok", Password: "rotated"}
	if !fresh.Configured() {
		t.Fatal("a complete credential must report configured")
	}
	got := c.withOptions(func() Options {
		o := c.o
		o.HelmOCIRegistry, o.HelmOCIUsername, o.HelmOCIPassword = fresh.LoginServer, fresh.Username, fresh.Password
		return o
	}())
	if got.o.HelmOCIPassword != "rotated" || got.o.HelmOCIUsername != "tok" {
		t.Errorf("sync credential did not win: %+v", got.o)
	}
	// The original must be untouched — the client is long-lived and shared.
	if c.o.HelmOCIPassword != "stale" {
		t.Errorf("withOptions mutated the client: %+v", c.o)
	}

	// An incomplete credential must not blank a working fallback.
	for _, bad := range []*shared.RegistryAuth{
		nil,
		{LoginServer: "r", Username: "u"},
		{LoginServer: "r", Password: "p"},
		{Username: "u", Password: "p"},
	} {
		if bad.Configured() {
			t.Errorf("incomplete credential reported configured: %+v", bad)
		}
	}
}

func TestPublishFailureIsNotOverwrittenByArgo(t *testing.T) {
	// Argo reports on the chart. It does not know that an app's HTTPRoute or its
	// oauth2-proxy failed to apply, so it will happily report Healthy while the
	// app is unreachable or unprotected. A publishing failure has to win, or a
	// broken app reads as fine — which is worse than the reverse.
	settledArgo := map[string]any{"status": map[string]any{
		"sync":   map[string]any{"status": "Synced"},
		"health": map[string]any{"status": "Healthy"},
	}}
	// argoMessage is what would be written for a healthy app: nothing.
	if got := argoMessage(settledArgo); got != "" {
		t.Fatalf("a healthy app should have no message, got %q", got)
	}
	// So if the publish error were overwritten, the operator would see an empty
	// detail and a Healthy status for an app that is not serving. The reconciler
	// guards that by only consulting Argo when publishing succeeded; this test
	// pins the premise that makes the guard necessary.
}

// Server-side apply must stay OFF until Argo is new enough for this cluster.
//
// It looks like the right fix for the annotation-size limit that stops charts
// with large CRDs installing, and it was briefly enabled for that reason. But
// Argo v2.13 against Kubernetes 1.35 then fails to diff any Deployment
// (".status.terminatingReplicas: field not declared in schema"), every
// application's sync status goes Unknown, and Argo declines to auto-sync
// anything at all.
func TestApplicationDoesNotUseServerSideApply(t *testing.T) {
	app := buildApplication(shared.DesiredApplication{
		ID: "external-secrets", Chart: "external-secrets", TargetRevision: "0.19.2",
	}, "external-secrets")

	spec, _ := app.Object["spec"].(map[string]any)
	policy, _ := spec["syncPolicy"].(map[string]any)
	opts, _ := policy["syncOptions"].([]any)

	for _, o := range opts {
		if s, _ := o.(string); s == "ServerSideApply=true" {
			t.Fatalf("ServerSideApply breaks diffing on Argo v2.13 with Kubernetes 1.35 — every app goes Unknown and stops auto-syncing; got %v", opts)
		}
	}
}

// The upstream must receive the ID token as a Bearer JWT, not Basic auth.
//
// oauth2-proxy's --pass-basic-auth puts "Authorization: Basic <user:password>"
// on the request. That occupies the one header a JWT-validating application
// reads, so the app sees a credential it cannot parse and reports the session as
// invalid — which looks like an expired login rather than a misconfigured proxy.
func TestAuthProxyPassesBearerTokenNotBasic(t *testing.T) {
	ing := &shared.IngressConfig{
		AppsDomain:       "apps.example",
		OIDCIssuer:       "https://issuer.example/v2.0",
		OIDCClientID:     "client",
		OIDCClientSecret: "secret",
	}
	app := shared.DesiredApplication{ID: "insight", ExposeService: "frontend", ExposePort: 80}
	d := authDeployment("insight-auth", "insight", app, ing, "insight.apps.example")

	raw, _ := json.Marshal(d.Object)
	got := string(raw)
	if !strings.Contains(got, "--pass-authorization-header=true") {
		t.Error("the ID token must be forwarded as a Bearer JWT (--pass-authorization-header)")
	}
	if strings.Contains(got, `--pass-basic-auth=true`) {
		t.Error("--pass-basic-auth overwrites Authorization with Basic and hides the JWT from the app")
	}
}

// A caller must not be able to assert their own identity.
//
// Insight — and any app behind this proxy — treats X-Cortex-Sub as proof of who
// is calling; a probe with a hand-written header returned authenticated:true
// from its BFF. Oathkeeper forwards request headers it is not told about, so
// every header in that family must be overwritten on the way through, including
// the ones this hop has no value for. Overwriting with an empty string removes
// them.
func TestAuthzHopOverwritesEveryIdentityHeader(t *testing.T) {
	ing := &shared.IngressConfig{
		AppsDomain:       "apps.example",
		OIDCIssuer:       "https://login.microsoftonline.com/tid/v2.0",
		OIDCClientID:     "client",
		OIDCClientSecret: "secret",
	}
	app := shared.DesiredApplication{ID: "insight", Hostname: "insight", ExposeService: "frontend", ExposePort: 80}
	cm := authzHopConfigMap("insight-auth-authz", "insight", "insight", "t-1", app, ing)

	data, _ := cm.Object["data"].(map[string]any)
	rules, _ := data["rules.json"].(string)

	var parsed []map[string]any
	if err := json.Unmarshal([]byte(rules), &parsed); err != nil {
		t.Fatalf("rules are not valid JSON: %v", err)
	}
	mutators, _ := parsed[0]["mutators"].([]any)
	cfg, _ := mutators[0].(map[string]any)["config"].(map[string]any)
	headers, _ := cfg["headers"].(map[string]any)

	// Every header an app in this platform trusts for identity.
	for _, h := range []string{
		"X-Cortex-Sub", "X-Cortex-Subject", "X-Cortex-Tenant", "X-Cortex-App",
		"X-Cortex-Email", "X-Cortex-Name", "X-Cortex-Roles",
		"X-Cortex-Claim-Preferred-Username", "X-Cortex-Claim-Given-Name",
		"X-Cortex-Claim-Family-Name",
	} {
		if _, ok := headers[h]; !ok {
			t.Errorf("%s is not overwritten, so a caller can send their own and be believed", h)
		}
	}
}

// The subject must come from the verified token, not from the request.
func TestAuthzHopTakesSubjectFromTheVerifiedToken(t *testing.T) {
	ing := &shared.IngressConfig{
		AppsDomain: "apps.example", OIDCIssuer: "https://issuer.example/v2.0",
		OIDCClientID: "client", OIDCClientSecret: "secret",
	}
	app := shared.DesiredApplication{ID: "insight", Hostname: "insight", ExposeService: "frontend", ExposePort: 80}
	cm := authzHopConfigMap("insight-auth-authz", "insight", "insight", "t-1", app, ing)
	rules, _ := cm.Object["data"].(map[string]any)["rules.json"].(string)

	if !strings.Contains(rules, `"handler":"jwt"`) {
		t.Error("the token must be verified here; trusting the upstream makes anything reaching this port whoever it claims to be")
	}
	if !strings.Contains(rules, ".Extra.preferred_username") {
		t.Error("subject must come from the verified token's claims, not the request")
	}
	// The payload is itself a JSON document carried as a string, so assert on
	// the parsed value rather than the escaped text.
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(rules), &parsed); err != nil {
		t.Fatalf("rules are not valid JSON: %v", err)
	}
	authorizer, _ := parsed[0]["authorizer"].(map[string]any)
	cfg, _ := authorizer["config"].(map[string]any)
	payload, _ := cfg["payload"].(string)

	var body map[string]string
	if err := json.Unmarshal([]byte(payload), &body); err != nil {
		t.Fatalf("authorizer payload is not valid JSON: %q", payload)
	}
	if body["app"] != "insight" {
		t.Errorf("the app must be named explicitly, got %q — the authorizer's Host names the authz service, not the app", body["app"])
	}
	if body["subject"] != "{{ print .Extra.preferred_username }}" {
		t.Errorf("subject must come from the verified token's claims, got %q", body["subject"])
	}
}

// Oathkeeper validates its global handler config at startup and exits if a
// required property is missing — even when every access rule overrides it. The
// first version of this config enabled the handlers with no config block, and
// the container died at boot with a schema error rather than failing on a
// request, so nothing about the running system pointed at the cause.
func TestAuthzHopGlobalConfigCarriesRequiredProperties(t *testing.T) {
	ing := &shared.IngressConfig{
		AppsDomain: "apps.example", OIDCIssuer: "https://issuer.example/v2.0",
		OIDCClientID: "client", OIDCClientSecret: "secret",
	}
	cfg := authzHopConfig("insight", "t-1", ing)

	for _, required := range []string{
		"jwks_urls", // authenticators.jwt
		"remote:",   // authorizers.remote_json
		"payload:",  // authorizers.remote_json
		"headers:",  // mutators.header
	} {
		if !strings.Contains(cfg, required) {
			t.Errorf("global config is missing %q — Oathkeeper exits at boot without it", required)
		}
	}
}

// Entra's issuer ends in /v2.0 but its JWKS hangs off the tenant root, so
// appending the well-known suffix to the issuer produces
// /v2.0/discovery/v2.0/keys — which 404s. Every request then fails to
// authenticate, and nothing in the proxy's logs names a malformed URL.
func TestJWKSURLIsNotDoubledForEntra(t *testing.T) {
	got := jwksURLFor("https://login.microsoftonline.com/tid/v2.0")
	want := "https://login.microsoftonline.com/tid/discovery/v2.0/keys"
	if got != want {
		t.Errorf("jwks url\n got: %s\nwant: %s", got, want)
	}
	if strings.Contains(got, "/v2.0/discovery") {
		t.Error("issuer's /v2.0 must not be carried into the JWKS path")
	}
}

// Grants are written against the address an operator types into the admin UI.
// Entra's `sub` claim is an opaque pairwise identifier — different per
// application and meaningless to a human — so taking the subject from it would
// mean no login ever matched anything that had been granted, and every user
// would be denied with nothing obviously wrong.
func TestAuthzHopIdentifiesUsersByAddressNotOpaqueSub(t *testing.T) {
	ing := &shared.IngressConfig{
		AppsDomain: "apps.example", OIDCIssuer: "https://issuer.example/v2.0",
		OIDCClientID: "client", OIDCClientSecret: "secret",
	}
	app := shared.DesiredApplication{ID: "insight", Hostname: "insight", ExposeService: "frontend", ExposePort: 80}
	rules, _ := authzHopConfigMap("a", "insight", "insight", "t-1", app, ing).
		Object["data"].(map[string]any)["rules.json"].(string)

	var parsed []map[string]any
	if err := json.Unmarshal([]byte(rules), &parsed); err != nil {
		t.Fatalf("rules are not valid JSON: %v", err)
	}
	auth, _ := parsed[0]["authenticators"].([]any)
	cfg, _ := auth[0].(map[string]any)["config"].(map[string]any)

	// subject_from is not a property of the jwt authenticator: setting it makes
	// Oathkeeper reject the whole rule and answer every request with a 500.
	if _, present := cfg["subject_from"]; present {
		t.Error("jwt authenticator has no subject_from; it invalidates the rule")
	}

	authorizer, _ := parsed[0]["authorizer"].(map[string]any)
	acfg, _ := authorizer["config"].(map[string]any)
	payload, _ := acfg["payload"].(string)
	if !strings.Contains(payload, ".Extra.preferred_username") {
		t.Errorf("subject must be the user's address from the token claims, got %q", payload)
	}
	if strings.Contains(payload, ".Subject") {
		t.Error("`sub` is an opaque pairwise identifier in Entra and matches nothing that was granted")
	}
}

// An app is known to the authorization service by its hostname label — what an
// operator sees and what grants are written against — not the control plane's
// internal id. If these disagreed, the UI would offer one name while the
// gateway asked about another and every grant would appear to do nothing.
func TestAuthzAppNameIsTheHostnameLabel(t *testing.T) {
	withHost := shared.DesiredApplication{ID: "todo-app", Hostname: "todo"}
	if got := appNameForAuthz(withHost); got != "todo" {
		t.Errorf("app name = %q, want the hostname label %q", got, "todo")
	}

	// Falling back to the id keeps an app addressable when it declares no
	// hostname, rather than registering it under an empty name.
	noHost := shared.DesiredApplication{ID: "Todo-App"}
	if got := appNameForAuthz(noHost); got != "todo-app" {
		t.Errorf("app name = %q, want the lowercased id", got)
	}
}
