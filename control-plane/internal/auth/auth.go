package auth

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/inception42/cortex/control-plane/internal/model"
)

const (
	defaultIssuerHost = "https://login.microsoftonline.com/"
	issuerHostV1      = "https://sts.windows.net/"
)

type ctxKey int

const identityKey ctxKey = iota

// Authenticator validates Microsoft Entra ID access tokens minted for this API:
// RS256 signature via JWKS, an audience that is this app registration, an
// issuer that matches the token's own tenant (multi-tenant), and the delegated
// scope the API exposes. Role is derived from the tenant id.
type Authenticator struct {
	keys          KeySet
	audiences     []string
	requiredScope string
	platformTID   string
	issuerHost    string
	// platformAdmins, when non-empty, restricts platform-admin status to these
	// principals — each entry is an email or an Entra object id (oid). Matching
	// either lets it work for both console tokens (which carry an email) and
	// service/CLI tokens (which may only carry an oid). Platform-hosted tenants put
	// ordinary users in the platform directory, so a platform-directory sign-in is
	// no longer sufficient to be an admin. Empty ⇒ any platform-directory user is
	// an admin (back-compat).
	platformAdmins map[string]bool
}

func New(keys KeySet, clientID, extraAudience, requiredScope, platformTID, issuerHost string, platformAdmins []string) *Authenticator {
	if issuerHost == "" {
		issuerHost = defaultIssuerHost
	}
	// A v2 access token for this API carries aud == client id; a v1 token
	// carries aud == the App ID URI. Accept both, plus any explicit override.
	auds := []string{}
	if clientID != "" {
		auds = append(auds, clientID, "api://"+clientID)
	}
	if extraAudience != "" {
		auds = append(auds, extraAudience)
	}
	admins := map[string]bool{}
	for _, e := range platformAdmins {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			admins[e] = true
		}
	}
	return &Authenticator{
		keys:           keys,
		audiences:      auds,
		requiredScope:  requiredScope,
		platformTID:    strings.ToLower(platformTID),
		issuerHost:     issuerHost,
		platformAdmins: admins,
	}
}

func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearer(r)
		if raw == "" {
			unauthorized(w, "missing bearer token")
			return
		}

		claims := jwt.MapClaims{}
		tok, err := jwt.ParseWithClaims(raw, claims, a.keyfunc,
			jwt.WithValidMethods([]string{"RS256"}),
			jwt.WithExpirationRequired(),
			jwt.WithLeeway(60*time.Second),
		)
		if err != nil || !tok.Valid {
			unauthorized(w, "invalid token")
			return
		}

		// Audience: the token must be addressed to this API.
		if !a.audienceOK(claims) {
			unauthorized(w, "wrong audience")
			return
		}

		tid := strings.ToLower(str(claims, "tid"))
		if tid == "" {
			unauthorized(w, "token missing tenant id")
			return
		}
		// Multi-tenant issuer: must be this exact tenant's endpoint (v1 or v2),
		// so a token can't claim a tenant it wasn't issued for.
		iss := str(claims, "iss")
		if iss != a.issuerHost+tid+"/v2.0" && iss != issuerHostV1+tid+"/" {
			unauthorized(w, "issuer / tenant mismatch")
			return
		}

		// Delegated scope: proves the token was authorized to call this API.
		if a.requiredScope != "" && !slices.Contains(strings.Fields(str(claims, "scp")), a.requiredScope) {
			unauthorized(w, "missing required scope")
			return
		}

		oid := str(claims, "oid")
		if oid == "" {
			oid = str(claims, "sub")
		}
		if oid == "" {
			unauthorized(w, "token missing subject")
			return
		}
		email := str(claims, "email")
		if email == "" {
			email = str(claims, "preferred_username")
		}

		id := model.Identity{
			OID:   oid,
			TID:   tid,
			Name:  str(claims, "name"),
			Email: email,
			Role:  model.RoleTenant,
		}
		// Platform admin: in the platform directory AND (no allowlist configured,
		// or the caller's email or object id is allowlisted). The allowlist lets
		// platform-hosted tenants put ordinary users in the platform directory
		// without making them admins; matching oid (not just email) keeps it robust
		// for tokens that omit the email claim.
		if a.platformTID != "" && tid == a.platformTID {
			if len(a.platformAdmins) == 0 || a.platformAdmins[strings.ToLower(email)] || a.platformAdmins[strings.ToLower(oid)] {
				id.Role = model.RolePlatform
			}
		}

		ctx := context.WithValue(r.Context(), identityKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *Authenticator) audienceOK(claims jwt.MapClaims) bool {
	for _, aud := range audienceList(claims["aud"]) {
		if slices.Contains(a.audiences, aud) {
			return true
		}
	}
	return false
}

func audienceList(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func (a *Authenticator) keyfunc(t *jwt.Token) (any, error) {
	kid, _ := t.Header["kid"].(string)
	return a.keys.Key(kid)
}

// IdentityFrom returns the authenticated identity from the request context.
func IdentityFrom(ctx context.Context) (model.Identity, bool) {
	id, ok := ctx.Value(identityKey).(model.Identity)
	return id, ok
}

func bearer(r *http.Request) string {
	if after, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

func str(m jwt.MapClaims, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"` + msg + `"}`))
}
