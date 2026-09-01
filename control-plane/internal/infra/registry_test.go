package infra

import (
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
