package cluster

import (
	"testing"

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
