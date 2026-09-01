package chart

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestBuildInterface(t *testing.T) {
	values := []byte("replicaCount: 2\nimage:\n  repository: nginx\n  tag: \"1.27\"\nservice:\n  type: ClusterIP\n  port: 80\n")
	schema := []byte(`{"$schema":"http://json-schema.org/draft-07/schema#","properties":{"replicaCount":{"type":"integer"}}}`)
	chartYAML := []byte("apiVersion: v2\nname: demo\nversion: 1.4.2\ndescription: A demo chart\n")

	iface := buildInterface(values, schema, chartYAML)
	if iface.Name != "demo" || iface.Version != "1.4.2" || iface.Description != "A demo chart" {
		t.Fatalf("metadata: %+v", iface)
	}
	// Defaults are values.yaml converted to JSON (the value tree).
	var d map[string]any
	if err := json.Unmarshal(iface.Defaults, &d); err != nil {
		t.Fatalf("defaults not json: %v", err)
	}
	if d["replicaCount"] != float64(2) {
		t.Fatalf("replicaCount: %v", d["replicaCount"])
	}
	img, _ := d["image"].(map[string]any)
	if img["repository"] != "nginx" {
		t.Fatalf("nested image.repository: %v", d["image"])
	}
	if !json.Valid(iface.Schema) || !strings.Contains(string(iface.Schema), "replicaCount") {
		t.Fatalf("schema not preserved: %s", iface.Schema)
	}
}

func TestBuildInterfaceEmptyValues(t *testing.T) {
	// A chart with no/empty values.yaml and no schema still yields a usable {} tree.
	iface := buildInterface(nil, nil, []byte("name: bare\nversion: 0.1.0\n"))
	if string(iface.Defaults) != "{}" {
		t.Fatalf("empty defaults should be {}: %s", iface.Defaults)
	}
	if iface.Schema != nil {
		t.Fatalf("no schema expected")
	}
	if iface.Name != "bare" {
		t.Fatalf("name: %s", iface.Name)
	}
}

func TestServicesFromManifests(t *testing.T) {
	// A realistic render: several documents, two Services, and a ServiceAccount
	// whose kind contains "Service" as a substring and must not be mistaken for
	// one. Names are the release's real object names, which is the whole point —
	// the reconciler routes to whatever is chosen, verbatim.
	manifests := []byte(`apiVersion: v1
kind: ServiceAccount
metadata:
  name: jenkins-ingress-nginx
---
apiVersion: v1
kind: Service
metadata:
  name: jenkins-ingress-nginx-controller
spec:
  type: LoadBalancer
  ports:
    - port: 80
      name: http
    - port: 443
      name: https
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: jenkins-ingress-nginx-controller
---
apiVersion: v1
kind: Service
metadata:
  name: jenkins-ingress-nginx-controller-admission
spec:
  type: ClusterIP
  ports:
    - port: 443
`)
	got := servicesFromManifests(manifests)
	if len(got) != 2 {
		t.Fatalf("expected 2 services, got %d: %#v", len(got), got)
	}
	// Sorted by name, so the order is stable for the picker.
	if got[0].Name != "jenkins-ingress-nginx-controller" || got[1].Name != "jenkins-ingress-nginx-controller-admission" {
		t.Fatalf("wrong names/order: %#v", got)
	}
	if got[0].Type != "LoadBalancer" || len(got[0].Ports) != 2 || got[0].Ports[0] != 80 || got[0].Ports[1] != 443 {
		t.Errorf("first service wrong: %#v", got[0])
	}
	if got[1].Type != "ClusterIP" || len(got[1].Ports) != 1 || got[1].Ports[0] != 443 {
		t.Errorf("second service wrong: %#v", got[1])
	}
}

func TestServicesFromManifestsIgnoresJunk(t *testing.T) {
	if got := servicesFromManifests(nil); len(got) != 0 {
		t.Errorf("nil manifests: %#v", got)
	}
	// A document that mentions the kind but is not one, and an unparsable doc.
	junk := []byte("kind: ServiceMonitor\nmetadata:\n  name: x\n---\nkind: Service\n  bad: [indent\n")
	if got := servicesFromManifests(junk); len(got) != 0 {
		t.Errorf("expected nothing usable, got %#v", got)
	}
}

func TestRegistryCredsOnlyForThePlatformRegistry(t *testing.T) {
	// The platform registry is the only one the control plane holds a credential
	// for: a private upstream is reached through the registry's cache, never
	// directly, so its own credential never leaves the registry.
	t.Setenv("HELM_OCI_REGISTRY", "cortexcpacr.azurecr.io")
	t.Setenv("HELM_OCI_USERNAME", "tenant-token")
	t.Setenv("HELM_OCI_PASSWORD", "s3cret")

	for _, ref := range []string{
		"oci://cortexcpacr.azurecr.io/charts",
		"cortexcpacr.azurecr.io/charts",
		"oci://CORTEXCPACR.azurecr.io/charts", // registries are case-insensitive
	} {
		if u, p := registryCreds(ref); u != "tenant-token" || p != "s3cret" {
			t.Errorf("%s: expected the platform credential, got %q/%q", ref, u, p)
		}
	}
	// Anything else is fetched anonymously — never send the credential onward.
	for _, ref := range []string{
		"https://charts.bitnami.com/bitnami",
		"oci://ghcr.io/bensincs/charts",
		"oci://evil.example.com/cortexcpacr.azurecr.io",
	} {
		if u, p := registryCreds(ref); u != "" || p != "" {
			t.Errorf("%s: credential must not be sent, got %q/%q", ref, u, p)
		}
	}
}

func TestRegistryCredsUnsetIsAnonymous(t *testing.T) {
	t.Setenv("HELM_OCI_REGISTRY", "")
	t.Setenv("HELM_OCI_USERNAME", "u")
	if u, p := registryCreds("oci://anything"); u != "" || p != "" {
		t.Errorf("no registry configured must mean anonymous, got %q/%q", u, p)
	}
}

func TestWriteRegistryConfig(t *testing.T) {
	// Helm 3 ignores --username/--password for an oci:// reference and tries an
	// anonymous token, which a private registry refuses. The registry config is
	// what it actually reads, so the auth entry must be keyed by bare host.
	dir := t.TempDir()
	path, err := writeRegistryConfig(dir, "oci://cortexcpacr.azurecr.io/charts", "tok", "pw")
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var cfg struct {
		Auths map[string]struct {
			Auth string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatalf("parse: %v", err)
	}
	entry, ok := cfg.Auths["cortexcpacr.azurecr.io"]
	if !ok {
		t.Fatalf("expected an entry keyed by bare host, got %v", cfg.Auths)
	}
	raw, _ := base64.StdEncoding.DecodeString(entry.Auth)
	if string(raw) != "tok:pw" {
		t.Errorf("auth = %q", raw)
	}
	// It holds a credential.
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("permissions = %v (%v)", fi.Mode().Perm(), err)
	}
}
