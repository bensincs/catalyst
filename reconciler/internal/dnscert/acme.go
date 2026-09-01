package dnscert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
)

// LetsEncryptProduction is the default ACME directory.
const LetsEncryptProduction = "https://acme-v02.api.letsencrypt.org/directory"

// LetsEncryptStaging issues untrusted certificates but has far looser rate
// limits — the right choice while proving the flow out.
const LetsEncryptStaging = "https://acme-staging-v02.api.letsencrypt.org/directory"

// renewBefore is how long before expiry a certificate is replaced. Let's Encrypt
// issues for 90 days; 30 leaves room for repeated failures without an outage.
const renewBefore = 30 * 24 * time.Hour

// issueBackoff is how long to wait after a failed issuance. The reconcile loop
// runs every poll interval and ACME rate limits are unforgiving, so a failing
// tenant must back off rather than retry on every pass.
const issueBackoff = 15 * time.Minute

// IssueWildcard obtains a wildcard certificate for *.<zone> over ACME DNS-01,
// answering the challenge by writing TXT records into the zone we hold.
//
// DNS-01 specifically: it is the only challenge type that can produce a wildcard
// (HTTP-01 cannot), and it needs no inbound connectivity, so a certificate can
// be issued before the tenant's cluster or apps exist.
//
// Returns the PEM certificate chain, the PEM private key, and the expiry.
func (c *Client) IssueWildcard(ctx context.Context, zone, accountKeyPEM string) (certPEM, keyPEM string, notAfter time.Time, accountKeyOut string, err error) {
	accountKey, accountKeyOut, err := loadOrCreateAccountKey(accountKeyPEM)
	if err != nil {
		return "", "", time.Time{}, "", fmt.Errorf("acme account key: %w", err)
	}

	client := &acme.Client{Key: accountKey, DirectoryURL: c.acmeDirectory}

	// Registration is idempotent in effect: an existing account for this key
	// comes back as ErrAccountAlreadyExists, which is success for our purposes.
	if _, err := client.Register(ctx, &acme.Account{Contact: contacts(c.acmeEmail)}, acme.AcceptTOS); err != nil &&
		!errors.Is(err, acme.ErrAccountAlreadyExists) {
		return "", "", time.Time{}, "", fmt.Errorf("acme register: %w", err)
	}

	// A wildcard authorizes the bare domain too, so ask for both: the cert then
	// covers apps.contoso.com as well as *.apps.contoso.com.
	wildcard := "*." + zone
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(wildcard, zone))
	if err != nil {
		return "", "", time.Time{}, "", fmt.Errorf("acme order: %w", err)
	}

	// Both identifiers challenge the SAME TXT name (_acme-challenge.<zone>), so
	// their values must be published together rather than overwriting each other.
	const challengeName = "_acme-challenge"
	var values []string
	type pending struct{ authzURL string; chal *acme.Challenge }
	var todo []pending

	for _, authzURL := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, authzURL)
		if err != nil {
			return "", "", time.Time{}, "", fmt.Errorf("acme authz: %w", err)
		}
		if authz.Status == acme.StatusValid {
			continue // already authorized from an earlier attempt
		}
		var chal *acme.Challenge
		for _, c := range authz.Challenges {
			if c.Type == "dns-01" {
				chal = c
				break
			}
		}
		if chal == nil {
			return "", "", time.Time{}, "", errors.New("acme: no dns-01 challenge offered")
		}
		v, err := client.DNS01ChallengeRecord(chal.Token)
		if err != nil {
			return "", "", time.Time{}, "", fmt.Errorf("acme challenge record: %w", err)
		}
		values = append(values, v)
		todo = append(todo, pending{authzURL: authzURL, chal: chal})
	}

	if len(todo) > 0 {
		if err := c.upsertTXT(ctx, zone, challengeName, values); err != nil {
			return "", "", time.Time{}, "", fmt.Errorf("publish dns-01 record: %w", err)
		}
		// Always clean up: a stale challenge record is confusing and, once the
		// order is done, useless.
		defer func() { _ = c.deleteTXT(context.WithoutCancel(ctx), zone, challengeName) }()

		// Azure DNS is authoritative immediately, but Let's Encrypt's resolvers
		// need a moment to see it. Accepting too early wastes an attempt.
		select {
		case <-ctx.Done():
			return "", "", time.Time{}, "", ctx.Err()
		case <-time.After(15 * time.Second):
		}

		for _, p := range todo {
			if _, err := client.Accept(ctx, p.chal); err != nil {
				return "", "", time.Time{}, "", fmt.Errorf("acme accept: %w", err)
			}
			if _, err := client.WaitAuthorization(ctx, p.authzURL); err != nil {
				return "", "", time.Time{}, "", fmt.Errorf("acme authorization failed: %w", err)
			}
		}
	}

	// ECDSA P-256: smaller and faster than RSA, and AGC supports it explicitly.
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", time.Time{}, "", err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: wildcard},
		DNSNames: []string{wildcard, zone},
	}, certKey)
	if err != nil {
		return "", "", time.Time{}, "", err
	}
	chain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csrDER, true)
	if err != nil {
		return "", "", time.Time{}, "", fmt.Errorf("acme finalize: %w", err)
	}
	if len(chain) == 0 {
		return "", "", time.Time{}, "", errors.New("acme: empty certificate chain")
	}

	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return "", "", time.Time{}, "", fmt.Errorf("parse issued certificate: %w", err)
	}

	var certOut strings.Builder
	for _, der := range chain {
		if err := pem.Encode(&certOut, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
			return "", "", time.Time{}, "", err
		}
	}
	keyDER, err := x509.MarshalECPrivateKey(certKey)
	if err != nil {
		return "", "", time.Time{}, "", err
	}
	keyOut := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return certOut.String(), string(keyOut), leaf.NotAfter, accountKeyOut, nil
}

// loadOrCreateAccountKey reuses the tenant's stored ACME account key, or mints
// one. Reusing it matters: registering a fresh account per renewal burns the
// "new account" rate limit and loses the issuance history.
func loadOrCreateAccountKey(pemKey string) (*ecdsa.PrivateKey, string, error) {
	if strings.TrimSpace(pemKey) != "" {
		block, _ := pem.Decode([]byte(pemKey))
		if block != nil {
			if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
				return k, pemKey, nil
			}
		}
		// A corrupt stored key must not wedge renewals forever; fall through and
		// mint a new one.
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", err
	}
	der, err := x509.MarshalECPrivateKey(k)
	if err != nil {
		return nil, "", err
	}
	return k, string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})), nil
}

func contacts(email string) []string {
	if strings.TrimSpace(email) == "" {
		return nil
	}
	return []string{"mailto:" + strings.TrimSpace(email)}
}

// needsRenewal reports whether a certificate should be (re)issued.
func needsRenewal(has bool, expires *time.Time) bool {
	if !has || expires == nil {
		return true
	}
	return time.Until(*expires) < renewBefore
}
