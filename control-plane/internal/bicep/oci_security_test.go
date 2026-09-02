package bicep

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A Bicep module reference is author-supplied, so the registry it names — and
// therefore the 401 challenge it returns — is chosen by whoever can author
// infrastructure. Everything below is about not trusting that.

// TestCredentialNeverLeavesItsOwnHost is the important one. A registry can
// answer a 401 with any realm it likes, and this process used to follow it and
// present the platform's registry credential in a Basic header. Anyone able to
// author an infra entity could therefore point it at a registry they control and
// collect the platform's ACR pull token.
func TestCredentialNeverLeavesItsOwnHost(t *testing.T) {
	c := &ociClient{registry: "cortexcpacr6hy6uurw.azurecr.io", user: "cortex-cp-pull", pass: "tok"}

	for _, realmHost := range []string{
		"attacker.example", // an unrelated host
		"cortexcpacr6hy6uurw.azurecr.io.attacker.example", // suffix trickery
		"evil.azurecr.io", // same registry family, different tenant
		"ghcr.io",         // a real registry that is not ours
	} {
		if c.credentialsAllowedFor(realmHost) {
			t.Errorf("credential would be sent to %q", realmHost)
		}
	}
}

// The same credential IS sent when the realm is the registry's own host, or
// private packages could never be read at all.
func TestCredentialIsSentToItsOwnHost(t *testing.T) {
	var gotAuth string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/token") {
			gotAuth = r.Header.Get("Authorization")
			fmt.Fprint(w, `{"token":"t"}`)
			return
		}
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("Www-Authenticate", `Bearer realm="`+srv.URL+`/token",service="x"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"layers":[]}`)
	}))
	defer srv.Close()

	c := &ociClient{
		http:     srv.Client(),
		scheme:   "http",
		registry: strings.TrimPrefix(srv.URL, "http://"),
		user:     "u",
		pass:     "p",
	}
	_, _ = c.manifest(context.Background(), "repo", "1.0.0")

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("u:p"))
	if gotAuth != want {
		t.Fatalf("credential not sent to its own registry: got %q", gotAuth)
	}
}

// A realm on a different scheme is refused rather than followed, so a registry
// cannot downgrade the token exchange to plaintext.
func TestRealmSchemeMustMatch(t *testing.T) {
	c := &ociClient{scheme: "https", registry: "reg.example.com"}
	err := c.authorize(context.Background(), `Bearer realm="http://reg.example.com/token"`)
	if err == nil || !strings.Contains(err.Error(), "not https") {
		t.Fatalf("a plaintext realm was accepted: %v", err)
	}
}

func TestRealmMustBeParseable(t *testing.T) {
	c := &ociClient{scheme: "https", registry: "reg.example.com"}
	if err := c.authorize(context.Background(), `Bearer realm="://nonsense"`); err == nil {
		t.Fatal("an unusable realm was accepted")
	}
}

// credRegistry decides which host the configured credential belongs to. Sending
// an ACR token to ghcr.io is what produced the 403 that made every GHCR module
// unreadable, so the binding is explicit rather than implied.
func TestCredentialIsBoundToOneRegistry(t *testing.T) {
	t.Setenv("BICEP_OCI_REGISTRY", "myacr.azurecr.io")
	if got := credRegistry(); got != "myacr.azurecr.io" {
		t.Fatalf("credRegistry = %q", got)
	}
	t.Setenv("BICEP_OCI_REGISTRY", "")
	t.Setenv("HELM_OCI_REGISTRY", "fallback.azurecr.io")
	if got := credRegistry(); got != "fallback.azurecr.io" {
		t.Fatalf("credRegistry fallback = %q", got)
	}
}

func TestHostOnlyIgnoresPort(t *testing.T) {
	if hostOnly("reg.example.com:443") != "reg.example.com" {
		t.Fatal("port not stripped, so a realm on an explicit port would look foreign")
	}
	if hostOnly("reg.example.com") != "reg.example.com" {
		t.Fatal("bare host mangled")
	}
}

// The platform registry must be fetched over the plain Registry v2 API, not
// handed to Bicep's restore: the CLI needs an Azure credential it does not have
// in the control plane's container, while a registry token — which the control
// plane does hold — works over HTTP.
func TestPlatformRegistryIsFetchedDirectly(t *testing.T) {
	t.Setenv("BICEP_OCI_REGISTRY", "cortexcpacr6hy6uurw.azurecr.io")

	if !ociHosted("br:cortexcpacr6hy6uurw.azurecr.io/bicep/postgres:0.1.0") {
		t.Error("the platform ACR should be fetched directly; the CLI cannot authenticate to it here")
	}
	if !ociHosted("br:ghcr.io/acme/bicep/db:1.0.0") {
		t.Error("GHCR must be fetched directly — the CLI cannot pull it at all")
	}
	if ociHosted("br:mcr.microsoft.com/bicep/avm/x:1.0.0") {
		t.Error("MCR needs no credential and is left to the CLI")
	}
	// Someone else's ACR: we hold no token for it, so the CLI's AAD restore is
	// the only path that could work.
	if ociHosted("br:someoneelse.azurecr.io/bicep/db:1.0.0") {
		t.Error("a foreign ACR should still go through the CLI")
	}
}
