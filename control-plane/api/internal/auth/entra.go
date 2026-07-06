package auth

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// KeySet resolves an RSA public key by JWK "kid".
type KeySet interface {
	Key(kid string) (*rsa.PublicKey, error)
}

// JWKS fetches and caches a remote JSON Web Key Set (Entra's signing keys).
type JWKS struct {
	url   string
	ttl   time.Duration
	httpc *http.Client

	mu      sync.RWMutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

func NewJWKS(url string) *JWKS {
	return &JWKS{
		url:   url,
		ttl:   6 * time.Hour,
		httpc: &http.Client{Timeout: 10 * time.Second},
		keys:  map[string]*rsa.PublicKey{},
	}
}

func (j *JWKS) Key(kid string) (*rsa.PublicKey, error) {
	j.mu.RLock()
	k := j.keys[kid]
	stale := time.Since(j.fetched) > j.ttl
	j.mu.RUnlock()

	if k != nil && !stale {
		return k, nil
	}
	// Refresh on a miss or when stale; if refresh fails but we have a cached
	// key, keep serving it.
	if err := j.refresh(); err != nil {
		if k != nil {
			return k, nil
		}
		return nil, err
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	if k := j.keys[kid]; k != nil {
		return k, nil
	}
	return nil, fmt.Errorf("signing key %q not found in JWKS", kid)
}

type jwkKey struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
	Use string `json:"use"`
}

func (j *JWKS) refresh() error {
	resp, err := j.httpc.Get(j.url)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch jwks: status %d", resp.StatusCode)
	}
	var doc struct {
		Keys []jwkKey `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("decode jwks: %w", err)
	}
	next := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		pub, err := rsaPublicKey(k.N, k.E)
		if err != nil {
			continue
		}
		next[k.Kid] = pub
	}
	if len(next) == 0 {
		return errors.New("jwks contained no usable RSA keys")
	}
	j.mu.Lock()
	j.keys = next
	j.fetched = time.Now()
	j.mu.Unlock()
	return nil
}

// StaticKeySet is an in-memory KeySet (used in tests).
type StaticKeySet map[string]*rsa.PublicKey

func (s StaticKeySet) Key(kid string) (*rsa.PublicKey, error) {
	if k, ok := s[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("signing key %q not found", kid)
}

func rsaPublicKey(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, fmt.Errorf("decode modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, fmt.Errorf("decode exponent: %w", err)
	}
	e := 0
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	if e == 0 {
		return nil, errors.New("invalid exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}
