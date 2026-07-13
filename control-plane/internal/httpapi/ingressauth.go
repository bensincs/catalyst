package httpapi

import (
	"regexp"
	"strings"

	"github.com/inception42/cortex/shared"
)

// Entra issuer hosts (mirrors auth.Authenticator, which accepts both token
// versions). The v2 host is configurable per cloud; v1 tokens carry the legacy
// sts.windows.net issuer.
const (
	entraIssuerHostDefault = "https://login.microsoftonline.com/"
	entraIssuerHostV1      = "https://sts.windows.net/"
)

// tenantIDPattern is the exact shape of an Entra directory id (a GUID). Pinning
// it means a malformed tid can never be spliced into the issuer/JWKS URLs.
var tenantIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ingressAuthForTenant builds the ingress JWT rules that pin a tenant's cluster
// to the (multi-tenant) Cortex app registration while accepting only tokens
// issued by THAT tenant's Entra directory. It mirrors the audience + issuer
// checks the API itself applies (auth.Authenticator): aud ∈ {clientID,
// api://clientID}, iss ∈ {v2 endpoint, v1 endpoint} for the caller's own tid.
//
// Returns nil when no app registration is configured or the tid isn't a
// well-formed GUID, so a cluster stays open-to-config rather than being locked by
// an empty (deny-all) policy or a malformed issuer.
func (s *Server) ingressAuthForTenant(tid string) *shared.IngressAuth {
	tid = strings.ToLower(strings.TrimSpace(tid))
	if s.entraClientID == "" || !tenantIDPattern.MatchString(tid) {
		return nil
	}
	host := s.entraIssuerHost
	if host == "" {
		host = entraIssuerHostDefault
	}
	if !strings.HasSuffix(host, "/") {
		host += "/"
	}
	auds := []string{s.entraClientID, "api://" + s.entraClientID}
	return &shared.IngressAuth{Rules: []shared.IngressJWTRule{
		{
			Issuer:    host + tid + "/v2.0",
			JWKSURI:   host + tid + "/discovery/v2.0/keys",
			Audiences: auds,
		},
		{
			Issuer:    entraIssuerHostV1 + tid + "/",
			JWKSURI:   host + tid + "/discovery/keys",
			Audiences: auds,
		},
	}}
}
