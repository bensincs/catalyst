package cluster

import (
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
	gw := gateway()
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
