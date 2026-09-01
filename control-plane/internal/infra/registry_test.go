package infra

import (
	"context"
	"strings"
	"testing"
)

func TestRegistryScopeActions(t *testing.T) {
	// Pull needs the content; metadata/read is what lets a client resolve a tag
	// to a digest. Scoping to the cached repositories is what stops a tenant
	// token reading the control plane's own images in the same registry.
	got := registryScopeActions([]string{"charts/*", " bicep/* ", "", "/x/"})
	want := []string{
		"repositories/charts/*/content/read",
		"repositories/charts/*/metadata/read",
		"repositories/bicep/*/content/read",
		"repositories/bicep/*/metadata/read",
		"repositories/x/content/read",
		"repositories/x/metadata/read",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v", got)
	}
	if len(registryScopeActions(nil)) != 0 {
		t.Error("no repositories should mean no actions")
	}
}

func TestRegistryTokenName(t *testing.T) {
	// Registry object names take only alphanumerics, dash and underscore, so a
	// slug with anything else has to be reduced rather than rejected.
	cases := map[string]string{
		"t-8f646aa4a8c3c71e": "cortex-t-8f646aa4a8c3c71e",
		"Acme Corp":          "cortex-acmecorp",
		"a.b_c":              "cortex-abc",
	}
	for in, want := range cases {
		if got := registryTokenName(in); got != want {
			t.Errorf("registryTokenName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCredentialSetName(t *testing.T) {
	// A credential set is bound to one login server, so hosts get one each, and
	// the name has to survive the registry's character rules.
	cases := map[string]string{
		"ghcr.io":          "ghcr-io",
		"index.docker.io":  "index-docker-io",
		"myreg.azurecr.io": "myreg-azurecr-io",
	}
	for in, want := range cases {
		if got := credentialSetName(in); got != want {
			t.Errorf("credentialSetName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUpstreamHost(t *testing.T) {
	for in, want := range map[string]string{
		"ghcr.io/acme/charts/*": "ghcr.io",
		"oci://ghcr.io/x":       "ghcr.io",
		"ghcr.io":               "ghcr.io",
	} {
		if got := upstreamHost(in); got != want {
			t.Errorf("upstreamHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPutUpstreamRequiresWildcard(t *testing.T) {
	// The registry accepts an exact source and then answers 403 on every pull,
	// so it is rejected up front. Verified against the live registry: the same
	// upstream cached as `ghcr.io/x/*` serves, as `ghcr.io/x/name` it does not.
	p := &Provisioner{platformACRID: "/subscriptions/s/x", platformACRRepos: []string{"images/*"}}
	err := p.PutUpstream(context.Background(), "r", "ghcr.io/acme/todoapp", "images/todoapp", "", "")
	if err == nil || !strings.Contains(err.Error(), "must end with /*") {
		t.Fatalf("an exact source must be refused, got %v", err)
	}
	if err := p.PutUpstream(context.Background(), "", "ghcr.io/acme/*", "images/*", "", ""); err == nil {
		t.Error("a missing name must be refused")
	}
}
