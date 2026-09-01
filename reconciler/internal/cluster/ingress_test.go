package cluster

import (
	"encoding/base64"
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
