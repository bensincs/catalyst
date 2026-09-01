package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
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

// RegistryCredential returns the tenant's stored pull token for the platform
// registry, or empty strings when it has never been minted.
func (s *Store) RegistryCredential(ctx context.Context, slug string) (user, pass string, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT coalesce(registry_username,''), coalesce(registry_password,'')
		   FROM tenants WHERE id = $1`, slug).Scan(&user, &pass)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil
	}
	return user, pass, err
}

// SetRegistryCredential stores the tenant's pull token. Minting one is
// destructive — the registry returns a password only by generating it, which
// invalidates the previous — so the value is kept and reused rather than
// re-minted on every read.
func (s *Store) SetRegistryCredential(ctx context.Context, slug, user, pass string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE tenants SET registry_username = $2, registry_password = $3 WHERE id = $1`,
		slug, strings.TrimSpace(user), pass)
	return err
}

// LiveTenantSlugs lists tenants that should hold a registry credential: enabled,
// and past registration. A disabled or tombstoned tenant is skipped so a
// credential is not minted for something that should not be pulling.
func (s *Store) LiveTenantSlugs(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM tenants WHERE enabled = true`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
