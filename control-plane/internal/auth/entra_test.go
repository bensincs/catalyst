package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/inception42/cortex/control-plane/internal/model"
)

const (
	testClientID = "client-abc"
	testPlatform = "platform-tid-0001"
	testKid      = "test-kid"
	testScope    = "access_as_user"
	issuerHost   = "https://login.microsoftonline.com/"
)

func signToken(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

// v2 access token for the API: aud == client id, v2 issuer, delegated scope.
func validClaims(tid string) jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"aud":                testClientID,
		"iss":                issuerHost + tid + "/v2.0",
		"tid":                tid,
		"oid":                "oid-123",
		"name":               "Test User",
		"preferred_username": "user@" + tid + ".example",
		"scp":                testScope,
		"iat":                now.Unix(),
		"nbf":                now.Unix(),
		"exp":                now.Add(time.Hour).Unix(),
	}
}

func serve(a *Authenticator, token string) (*httptest.ResponseRecorder, *model.Identity) {
	var captured *model.Identity
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, ok := IdentityFrom(r.Context()); ok {
			captured = &id
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr, captured
}

func TestEntraValidation(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)

	keys := StaticKeySet{testKid: &key.PublicKey}
	a := New(keys, testClientID, "", testScope, testPlatform, issuerHost)

	t.Run("valid platform token", func(t *testing.T) {
		rr, id := serve(a, signToken(t, key, testKid, validClaims(testPlatform)))
		if rr.Code != http.StatusOK || id == nil {
			t.Fatalf("expected 200 with identity, got %d", rr.Code)
		}
		if id.Role != model.RolePlatform {
			t.Fatalf("expected platform role, got %q", id.Role)
		}
	})

	t.Run("valid tenant token", func(t *testing.T) {
		rr, id := serve(a, signToken(t, key, testKid, validClaims("some-customer-tid")))
		if rr.Code != http.StatusOK || id == nil {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		if id.Role != model.RoleTenant {
			t.Fatalf("expected tenant role, got %q", id.Role)
		}
	})

	t.Run("v1 token (App ID URI aud + sts issuer) accepted", func(t *testing.T) {
		c := validClaims(testPlatform)
		c["aud"] = "api://" + testClientID
		c["iss"] = issuerHostV1 + testPlatform + "/"
		rr, id := serve(a, signToken(t, key, testKid, c))
		if rr.Code != http.StatusOK || id == nil {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("wrong audience rejected", func(t *testing.T) {
		c := validClaims(testPlatform)
		c["aud"] = "some-other-app"
		rr, _ := serve(a, signToken(t, key, testKid, c))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("missing scope rejected", func(t *testing.T) {
		c := validClaims(testPlatform)
		delete(c, "scp")
		rr, _ := serve(a, signToken(t, key, testKid, c))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("wrong scope rejected", func(t *testing.T) {
		c := validClaims(testPlatform)
		c["scp"] = "User.Read"
		rr, _ := serve(a, signToken(t, key, testKid, c))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("expired token rejected", func(t *testing.T) {
		c := validClaims(testPlatform)
		c["exp"] = time.Now().Add(-2 * time.Hour).Unix()
		rr, _ := serve(a, signToken(t, key, testKid, c))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("issuer/tenant mismatch rejected", func(t *testing.T) {
		c := validClaims(testPlatform)
		c["iss"] = issuerHost + "a-different-tenant/v2.0"
		rr, _ := serve(a, signToken(t, key, testKid, c))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("unknown signing key rejected", func(t *testing.T) {
		rr, _ := serve(a, signToken(t, otherKey, testKid, validClaims(testPlatform)))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("missing token rejected", func(t *testing.T) {
		rr, _ := serve(a, "")
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rr.Code)
		}
	})
}
