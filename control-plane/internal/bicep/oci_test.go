package bicep

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOCIHosted(t *testing.T) {
	cases := map[string]bool{
		"br:ghcr.io/bensincs/bicep/postgres:0.1.0": true,
		"oci://ghcr.io/x/y:1":                      true,
		"br:myacr.azurecr.io/bicep/db:1.0.0":       false,
		"br/public:avm/res/foo:1.0.0":              false,
		"not-a-ref":                                false,
	}
	for ref, want := range cases {
		if got := ociHosted(ref); got != want {
			t.Errorf("ociHosted(%q) = %v, want %v", ref, got, want)
		}
	}
}

func TestParseChallenge(t *testing.T) {
	realm, p := parseChallenge(`Bearer realm="https://ghcr.io/token",service="ghcr.io",scope="repository:a/b:pull"`)
	if realm != "https://ghcr.io/token" {
		t.Fatalf("realm = %q", realm)
	}
	if p["service"] != "ghcr.io" || p["scope"] != "repository:a/b:pull" {
		t.Fatalf("params = %v", p)
	}
	if r, _ := parseChallenge(`Basic realm="x"`); r != "" {
		t.Fatalf("non-bearer challenge should yield no realm, got %q", r)
	}
}

// TestFetchModuleOverOCI drives the full anonymous pull: a 401 bearer challenge,
// a token exchange, then the authorized manifest + blob fetch.
func TestFetchModuleOverOCI(t *testing.T) {
	armBody := []byte(`{"parameters":{},"outputs":{"id":{"type":"string"}},"resources":[]}`)
	digest := "sha256:deadbeef"
	var srvURL string

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("scope") != "repository:repo:pull" {
			t.Errorf("token scope = %q", r.URL.Query().Get("scope"))
		}
		fmt.Fprint(w, `{"token":"abc"}`)
	})
	mux.HandleFunc("/v2/repo/manifests/1.0.0", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer abc" {
			w.Header().Set("Www-Authenticate", fmt.Sprintf(`Bearer realm="%s/token",service="reg",scope="repository:repo:pull"`, srvURL))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, `{"layers":[{"mediaType":%q,"digest":%q}]}`, bicepModuleLayerMediaType, digest)
	})
	mux.HandleFunc("/v2/repo/blobs/"+digest, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer abc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write(armBody)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	c := &ociClient{
		http:     srv.Client(),
		scheme:   "http",
		registry: strings.TrimPrefix(srv.URL, "http://"),
	}
	man, err := c.manifest(context.Background(), "repo", "1.0.0")
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(man.Layers) != 1 || man.Layers[0].Digest != digest {
		t.Fatalf("manifest layers = %+v", man.Layers)
	}
	got, err := c.blob(context.Background(), "repo", digest)
	if err != nil {
		t.Fatalf("blob: %v", err)
	}
	if !bytes.Equal(got, armBody) {
		t.Fatalf("blob = %s", got)
	}

	// The pulled module's interface is readable with no toolchain.
	params, outs := parseModuleInterface(got)
	if len(params) != 0 || len(outs) != 1 || outs[0].Name != "id" {
		t.Fatalf("interface params=%v outs=%v", params, outs)
	}
}
