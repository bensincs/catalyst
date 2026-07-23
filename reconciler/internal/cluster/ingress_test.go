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

func TestAppIngressRoutesToReleaseService(t *testing.T) {
	ing := appIngress("shop", "tenant-ns", "app-123", "shop.apps.example.com")

	if got := ing.GetAPIVersion(); got != "networking.k8s.io/v1" {
		t.Fatalf("apiVersion: %q", got)
	}
	if got := ing.GetKind(); got != "Ingress" {
		t.Fatalf("kind: %q", got)
	}
	if got := ing.GetName(); got != "shop" {
		t.Fatalf("name: %q", got)
	}
	if got := ing.GetNamespace(); got != "tenant-ns" {
		t.Fatalf("namespace: %q", got)
	}

	// Managed (so GC finds it) but NOT system (system resources are excluded
	// from the app GC selector).
	labels := ing.GetLabels()
	if labels[labelManaged] != "true" {
		t.Fatalf("expected managed label, got %v", labels)
	}
	if _, ok := labels[labelSystem]; ok {
		t.Fatalf("app ingress must not carry the system label: %v", labels)
	}
	if labels[labelAppID] != "app-123" {
		t.Fatalf("expected app-id label, got %v", labels)
	}

	// AGIC class annotation routes it through the Azure Application Gateway.
	ann := ing.GetAnnotations()
	if ann["kubernetes.io/ingress.class"] != appGatewayIngressClass {
		t.Fatalf("expected AGIC ingress class, got %v", ann)
	}

	rules, found, err := unstructured.NestedSlice(ing.Object, "spec", "rules")
	if err != nil || !found || len(rules) != 1 {
		t.Fatalf("expected one rule, got %v (found=%v err=%v)", rules, found, err)
	}
	rule := rules[0].(map[string]any)
	if rule["host"] != "shop.apps.example.com" {
		t.Fatalf("expected host on rule, got %v", rule["host"])
	}

	paths := rule["http"].(map[string]any)["paths"].([]any)
	backend := paths[0].(map[string]any)["backend"].(map[string]any)
	svc := backend["service"].(map[string]any)
	if svc["name"] != "shop" {
		t.Fatalf("backend must target the release-name Service, got %v", svc["name"])
	}
	port := svc["port"].(map[string]any)
	if port["number"] != int64(80) {
		t.Fatalf("backend port should be 80, got %v", port["number"])
	}
}

func TestAppIngressHostlessWhenNoDomain(t *testing.T) {
	ing := appIngress("shop", "tenant-ns", "app-123", "")
	rules, _, _ := unstructured.NestedSlice(ing.Object, "spec", "rules")
	rule := rules[0].(map[string]any)
	if _, ok := rule["host"]; ok {
		t.Fatalf("host-less ingress must omit the host key: %v", rule)
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

// A standard chart names its Service <release>-<chart>, which wouldn't match our
// Ingress backend (the release name). buildApplication injects fullnameOverride so
// the chart's resources take the release name and line up with the Ingress.
func TestBuildApplicationInjectsFullnameOverride(t *testing.T) {
	app := shared.DesiredApplication{
		ID: "example-app", Namespace: "example",
		RepoURL: "oci://ghcr.io/bensincs/charts", Chart: "todo-app", TargetRevision: "0.1.0",
		Values: "database:\n  host: h\n",
	}
	name := appName(app.ID)
	u := buildApplication(app, name)

	helm, found, _ := unstructured.NestedMap(u.Object, "spec", "source", "helm")
	if !found {
		t.Fatal("expected spec.source.helm")
	}
	params, _ := helm["parameters"].([]any)
	ok := false
	for _, p := range params {
		if m, is := p.(map[string]any); is && m["name"] == "fullnameOverride" && m["value"] == name {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("expected fullnameOverride=%q parameter, got %v", name, params)
	}
	if helm["values"] != app.Values {
		t.Fatalf("author values must be preserved, got %v", helm["values"])
	}
	// OCI repoURL is scheme-stripped to match the auto-registered repo secret.
	if repo, _, _ := unstructured.NestedString(u.Object, "spec", "source", "repoURL"); repo != "ghcr.io/bensincs/charts" {
		t.Fatalf("repoURL = %q", repo)
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
