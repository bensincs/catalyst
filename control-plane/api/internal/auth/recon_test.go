package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// reconAppClaims is a managed-identity app token for the Cortex API: addressed
// to this app, issued by the caller's own tenant, no delegated scope.
func reconAppClaims(tid string) jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"aud": testClientID,
		"iss": issuerHost + tid + "/v2.0",
		"tid": tid,
		"oid": "mi-oid-9",
		"iat": now.Unix(),
		"nbf": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
}

func serveRecon(a *ReconAuthenticator, token string) (*httptest.ResponseRecorder, *ReconIdentity) {
	var captured *ReconIdentity
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := ReconIdentityFrom(r.Context()); ok {
			captured = &id
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/recon/sync", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr, captured
}

func TestReconAuth(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	keys := StaticKeySet{testKid: &key.PublicKey}
	a := NewRecon(keys, testClientID, "", issuerHost)

	t.Run("managed-identity app token (no scope) accepted", func(t *testing.T) {
		rr, id := serveRecon(a, signToken(t, key, testKid, reconAppClaims("customer-tid-7")))
		if rr.Code != http.StatusOK || id == nil {
			t.Fatalf("expected 200 with identity, got %d", rr.Code)
		}
		if id.TID != "customer-tid-7" || id.OID != "mi-oid-9" {
			t.Fatalf("unexpected identity %+v", id)
		}
	})

	t.Run("v1 app token (App ID URI aud + sts issuer) accepted", func(t *testing.T) {
		c := reconAppClaims("customer-tid-7")
		c["aud"] = "api://" + testClientID
		c["iss"] = issuerHostV1 + "customer-tid-7/"
		rr, id := serveRecon(a, signToken(t, key, testKid, c))
		if rr.Code != http.StatusOK || id == nil {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("oid falls back to sub", func(t *testing.T) {
		c := reconAppClaims("customer-tid-7")
		delete(c, "oid")
		c["sub"] = "sub-42"
		_, id := serveRecon(a, signToken(t, key, testKid, c))
		if id == nil || id.OID != "sub-42" {
			t.Fatalf("expected oid from sub, got %+v", id)
		}
	})

	t.Run("wrong audience rejected", func(t *testing.T) {
		c := reconAppClaims("customer-tid-7")
		c["aud"] = "some-other-app"
		rr, _ := serveRecon(a, signToken(t, key, testKid, c))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("issuer/tenant mismatch rejected", func(t *testing.T) {
		c := reconAppClaims("customer-tid-7")
		c["iss"] = issuerHost + "a-different-tenant/v2.0"
		rr, _ := serveRecon(a, signToken(t, key, testKid, c))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("missing tid rejected", func(t *testing.T) {
		c := reconAppClaims("customer-tid-7")
		delete(c, "tid")
		rr, _ := serveRecon(a, signToken(t, key, testKid, c))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("HS256 token rejected (no shared secret path)", func(t *testing.T) {
		now := time.Now()
		hs, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"tid": "customer-tid-7", "exp": now.Add(time.Hour).Unix(),
		}).SignedString([]byte("any-secret"))
		rr, _ := serveRecon(a, hs)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 (HS256 not accepted), got %d", rr.Code)
		}
	})

	t.Run("unknown signing key rejected", func(t *testing.T) {
		rr, _ := serveRecon(a, signToken(t, otherKey, testKid, reconAppClaims("customer-tid-7")))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("expired token rejected", func(t *testing.T) {
		c := reconAppClaims("customer-tid-7")
		c["exp"] = time.Now().Add(-2 * time.Hour).Unix()
		rr, _ := serveRecon(a, signToken(t, key, testKid, c))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("missing token rejected", func(t *testing.T) {
		rr, _ := serveRecon(a, "")
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})
}
