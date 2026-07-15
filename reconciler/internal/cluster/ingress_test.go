package cluster

import (
	"strings"
	"testing"

	"github.com/inception42/cortex/shared"
)

func sampleRule() shared.IngressJWTRule {
	return shared.IngressJWTRule{
		Issuer:    "https://login.microsoftonline.com/tid/v2.0",
		JWKSURI:   "https://login.microsoftonline.com/tid/discovery/v2.0/keys",
		Audiences: []string{"api://cortex"},
	}
}

func TestEnvoyConfigFailsClosed(t *testing.T) {
	cfg := envoyConfig(nil, "")
	if strings.Contains(cfg, "jwt_authn") {
		t.Fatalf("no rules should mean no jwt filter:\n%s", cfg)
	}
	if !strings.Contains(cfg, "403") {
		t.Fatalf("no rules should deny closed (403):\n%s", cfg)
	}
}

func TestEnvoyConfigWithJWT(t *testing.T) {
	cfg := envoyConfig([]shared.IngressJWTRule{sampleRule()}, "")
	for _, want := range []string{
		"jwt_authn",
		"https://login.microsoftonline.com/tid/v2.0", // issuer
		"api://cortex",                   // audience
		"jwks_login_microsoftonline_com", // generated JWKS cluster
		"UpstreamTlsContext",             // JWKS fetched over TLS
		"strict-transport-security",      // security-header Lua present
		"404",                            // authenticated but no backend wired
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("config missing %q:\n%s", want, cfg)
		}
	}
	if strings.Contains(cfg, "DownstreamTlsContext") {
		t.Fatalf("no TLS cred should mean plain HTTP listener")
	}
}

func TestEnvoyConfigTLS(t *testing.T) {
	cfg := envoyConfig([]shared.IngressJWTRule{sampleRule()}, "cortex-tls")
	for _, want := range []string{"DownstreamTlsContext", "TLSv1_2", "https_redirect", "/etc/envoy/tls/tls.crt"} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("TLS config missing %q:\n%s", want, cfg)
		}
	}
}

func TestValidAuthRules(t *testing.T) {
	auth := &shared.IngressAuth{Rules: []shared.IngressJWTRule{
		sampleRule(),
		{Issuer: "x", JWKSURI: "", Audiences: []string{"a"}}, // missing JWKS
		{Issuer: "", JWKSURI: "y", Audiences: []string{"a"}}, // missing issuer
		{Issuer: "z", JWKSURI: "w", Audiences: nil},          // missing audience
	}}
	got := validAuthRules(auth)
	if len(got) != 1 || got[0].Issuer != sampleRule().Issuer {
		t.Fatalf("expected only the complete rule, got %v", got)
	}
	if validAuthRules(nil) != nil {
		t.Fatalf("nil auth should yield nil rules")
	}
}

func TestJwksClusterName(t *testing.T) {
	if got := jwksClusterName("https://login.microsoftonline.com/tid/keys"); got != "jwks_login_microsoftonline_com" {
		t.Fatalf("cluster name: %q", got)
	}
	if got := jwksHost("not a url"); got != "login.microsoftonline.com" {
		t.Fatalf("bad url should fall back to Entra host, got %q", got)
	}
}

func TestConfigHashChanges(t *testing.T) {
	a := envoyConfig([]shared.IngressJWTRule{sampleRule()}, "")
	b := envoyConfig(nil, "")
	if configHash(a) == configHash(b) {
		t.Fatalf("different configs should hash differently")
	}
}
