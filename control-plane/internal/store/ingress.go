package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

/* ── Tenant ingress: delegated DNS zone, wildcard TLS, app OIDC ───────────── */

// SetAppsDomain records the domain a tenant will publish apps on and resets the
// delegation state, because a new domain means a new zone and new nameservers.
// Clearing it ("") tears the configuration down: apps stop being published.
//
// The certificate is cleared too — it was issued for the old domain and is
// useless for the new one.
func (s *Store) SetAppsDomain(ctx context.Context, slug, domain string) error {
	domain = strings.ToLower(strings.Trim(strings.TrimSpace(domain), "."))
	state := "pending"
	if domain == "" {
		state = ""
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE tenants
		    SET apps_domain = $2, dns_state = $3, dns_detail = '', dns_nameservers = '{}',
		        tls_cert = '', tls_key = '', tls_expires_at = NULL, tls_detail = ''
		  WHERE id = $1 AND coalesce(apps_domain,'') IS DISTINCT FROM $2`,
		slug, domain, state)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Either the tenant is gone, or the domain is already what was asked for
		// — the latter must not wipe a working certificate.
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM tenants WHERE id = $1`, slug).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
	}
	return nil
}

// SetDNSState records progress on the zone: created and awaiting delegation,
// verified, or broken. Nameservers are only overwritten when supplied, so a
// verification pass doesn't erase them.
func (s *Store) SetDNSState(ctx context.Context, slug, state, detail string, nameservers []string) error {
	if len(nameservers) > 0 {
		_, err := s.pool.Exec(ctx,
			`UPDATE tenants SET dns_state = $2, dns_detail = $3, dns_nameservers = $4 WHERE id = $1`,
			slug, state, trunc256(detail), nameservers)
		return err
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE tenants SET dns_state = $2, dns_detail = $3 WHERE id = $1`, slug, state, trunc256(detail))
	return err
}

// SetTLSCertificate stores a freshly issued wildcard certificate.
func (s *Store) SetTLSCertificate(ctx context.Context, slug, certPEM, keyPEM string, expires time.Time) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE tenants SET tls_cert = $2, tls_key = $3, tls_expires_at = $4, tls_detail = '' WHERE id = $1`,
		slug, certPEM, keyPEM, expires)
	return err
}

// SetTLSDetail records why issuance is pending or failed, without disturbing a
// certificate that may still be valid and serving.
func (s *Store) SetTLSDetail(ctx context.Context, slug, detail string) error {
	_, err := s.pool.Exec(ctx, `UPDATE tenants SET tls_detail = $2 WHERE id = $1`, slug, trunc256(detail))
	return err
}

// ACMEAccountKey returns the tenant's stored ACME account key, creating nothing.
// Empty means no registration yet.
func (s *Store) ACMEAccountKey(ctx context.Context, slug string) (string, error) {
	var k string
	if err := s.pool.QueryRow(ctx, `SELECT coalesce(acme_account_key,'') FROM tenants WHERE id = $1`, slug).Scan(&k); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return k, nil
}

// SetACMEAccountKey persists the account key so renewals reuse one registration
// rather than registering afresh and burning rate limit.
func (s *Store) SetACMEAccountKey(ctx context.Context, slug, pemKey string) error {
	_, err := s.pool.Exec(ctx, `UPDATE tenants SET acme_account_key = $2 WHERE id = $1`, slug, pemKey)
	return err
}

// SetOIDCConfig records the customer's OIDC application. An empty secret leaves
// the stored one intact, so editing the client id doesn't require re-entering
// the secret (which is never shown back).
func (s *Store) SetOIDCConfig(ctx context.Context, slug, issuer, clientID, clientSecret string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE tenants
		    SET oidc_issuer = $2, oidc_client_id = $3,
		        oidc_client_secret = CASE WHEN $4 <> '' THEN $4 ELSE oidc_client_secret END
		  WHERE id = $1`,
		slug, strings.TrimSpace(issuer), strings.TrimSpace(clientID), strings.TrimSpace(clientSecret))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// IngressTarget is a tenant whose DNS zone and certificate the control-plane
// worker maintains.
type IngressTarget struct {
	Slug           string
	Name           string
	AppsDomain     string
	DNSState       string
	Nameservers    []string
	SubscriptionID string
	// GatewayIP is the tenant cluster's ingress address, reported by heartbeat.
	// The wildcard record points at it, so there is nothing to publish until the
	// cluster has one.
	GatewayIP    string
	HasCert      bool
	CertExpires  *time.Time
	ACMEKey      string
}

// IngressTargets returns enabled tenants that have a domain configured. The
// worker creates the zone, verifies delegation, publishes the wildcard record
// and keeps the certificate fresh.
func (s *Store) IngressTargets(ctx context.Context) ([]IngressTarget, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, apps_domain, coalesce(dns_state,''), coalesce(dns_nameservers,'{}'),
		        coalesce(subscription_id,''), coalesce(cluster_gateway_ip,''),
		        (coalesce(tls_cert,'') <> '' AND coalesce(tls_key,'') <> ''), tls_expires_at,
		        coalesce(acme_account_key,'')
		   FROM tenants
		  WHERE enabled = true AND coalesce(apps_domain,'') <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IngressTarget{}
	for rows.Next() {
		var t IngressTarget
		if err := rows.Scan(&t.Slug, &t.Name, &t.AppsDomain, &t.DNSState, &t.Nameservers,
			&t.SubscriptionID, &t.GatewayIP, &t.HasCert, &t.CertExpires, &t.ACMEKey); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ingressConfigFor loads the tenant's full ingress configuration, secrets and
// all, for the sync response.
func (s *Store) ingressConfigFor(ctx context.Context, slug string) (string, string, string, string, string, string, error) {
	var domain, cert, key, issuer, clientID, secret string
	err := s.pool.QueryRow(ctx,
		`SELECT coalesce(apps_domain,''), coalesce(tls_cert,''), coalesce(tls_key,''),
		        coalesce(oidc_issuer,''), coalesce(oidc_client_id,''), coalesce(oidc_client_secret,'')
		   FROM tenants WHERE id = $1`, slug).
		Scan(&domain, &cert, &key, &issuer, &clientID, &secret)
	return domain, cert, key, issuer, clientID, secret, err
}
