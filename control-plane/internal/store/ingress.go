package store

import (
	"context"
	"strings"
)

/* ── Tenant ingress: delegated DNS zone, wildcard TLS, app OIDC ───────────── */

// SetAppsDomain records the domain a tenant will publish apps on and resets the
// delegation state, because a new domain means a new zone and new nameservers.
// Clearing it ("") tears the configuration down: apps stop being published.
//
// The recorded certificate expiry is cleared too — it described a certificate
// for the old domain. The certificate itself lives in the tenant's cluster and
// the reconciler replaces it once the new zone is delegated.
func (s *Store) SetAppsDomain(ctx context.Context, slug, domain string) error {
	domain = strings.ToLower(strings.Trim(strings.TrimSpace(domain), "."))
	state := "pending"
	if domain == "" {
		state = ""
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE tenants
		    SET apps_domain = $2, dns_state = $3, dns_detail = '', dns_nameservers = '{}',
		        tls_expires_at = NULL, tls_detail = ''
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

// ingressConfigFor loads the tenant's publishing intent for the sync response:
// the domain and the customer's OIDC application. No certificate — the
// reconciler holds that, having obtained it against its own zone.
func (s *Store) ingressConfigFor(ctx context.Context, slug string) (string, string, string, string, error) {
	var domain, issuer, clientID, secret string
	err := s.pool.QueryRow(ctx,
		`SELECT coalesce(apps_domain,''), coalesce(oidc_issuer,''),
		        coalesce(oidc_client_id,''), coalesce(oidc_client_secret,'')
		   FROM tenants WHERE id = $1`, slug).
		Scan(&domain, &issuer, &clientID, &secret)
	return domain, issuer, clientID, secret, err
}
