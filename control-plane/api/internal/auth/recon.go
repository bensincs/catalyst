package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var errUnauthorized = errors.New("unauthorized")

// ReconIdentity is a validated reconciler caller: the tenant it runs in (tid)
// and its principal (oid — the managed identity, for display/audit).
type ReconIdentity struct {
	TID string
	OID string
}

type reconCtxKey int

const reconIdentityKey reconCtxKey = iota

// ReconAuthenticator authenticates the in-tenant reconciler. It presents its
// Azure identity's Entra token for the Cortex API (RS256, JWKS-validated,
// audience = this app, per-tenant issuer — the same multi-tenant machinery the
// console uses, minus the delegated-scope check since it's an app/identity
// token). The tenant is identified by the token's own `tid`. No shared secret.
type ReconAuthenticator struct {
	keys       KeySet
	audiences  []string
	issuerHost string
}

func NewRecon(keys KeySet, clientID, extraAudience, issuerHost string) *ReconAuthenticator {
	if issuerHost == "" {
		issuerHost = defaultIssuerHost
	}
	auds := []string{}
	if clientID != "" {
		auds = append(auds, clientID, "api://"+clientID)
	}
	if extraAudience != "" {
		auds = append(auds, extraAudience)
	}
	return &ReconAuthenticator{keys: keys, audiences: auds, issuerHost: issuerHost}
}

func (a *ReconAuthenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := a.validate(bearer(r))
		if err != nil {
			unauthorized(w, "invalid reconciler token")
			return
		}
		ctx := context.WithValue(r.Context(), reconIdentityKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *ReconAuthenticator) validate(raw string) (ReconIdentity, error) {
	var zero ReconIdentity
	if raw == "" {
		return zero, errUnauthorized
	}
	claims := jwt.MapClaims{}
	tok, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, errUnauthorized
		}
		kid, _ := t.Header["kid"].(string)
		return a.keys.Key(kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithExpirationRequired(), jwt.WithLeeway(60*time.Second))
	if err != nil || !tok.Valid {
		return zero, errUnauthorized
	}

	// The token must be addressed to this API and issued by the token's own
	// tenant (multi-tenant issuer), so it can't claim a tenant it wasn't for.
	if !audiencesContain(a.audiences, claims) {
		return zero, errUnauthorized
	}
	tid := strings.ToLower(str(claims, "tid"))
	iss := str(claims, "iss")
	if tid == "" || (iss != a.issuerHost+tid+"/v2.0" && iss != issuerHostV1+tid+"/") {
		return zero, errUnauthorized
	}
	oid := str(claims, "oid")
	if oid == "" {
		oid = str(claims, "sub")
	}
	return ReconIdentity{TID: tid, OID: oid}, nil
}

func audiencesContain(allowed []string, claims jwt.MapClaims) bool {
	for _, aud := range audienceList(claims["aud"]) {
		for _, a := range allowed {
			if aud == a {
				return true
			}
		}
	}
	return false
}

// ReconIdentityFrom returns the validated reconciler identity from context.
func ReconIdentityFrom(ctx context.Context) (ReconIdentity, bool) {
	id, ok := ctx.Value(reconIdentityKey).(ReconIdentity)
	return id, ok
}
