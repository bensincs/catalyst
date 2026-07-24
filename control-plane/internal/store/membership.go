package store

import (
	"context"
	"strings"

	"github.com/inception42/cortex/control-plane/internal/model"
)

// AddMembership assigns a user (by email) to a tenant. Idempotent: re-assigning
// updates the role. The user's Entra oid is bound later, on their first sign-in
// (BindMemberships), since it isn't known at assignment time.
func (s *Store) AddMembership(ctx context.Context, slug, email, role string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if role == "" {
		role = "admin"
	}
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO memberships (tenant_slug, email, role) VALUES ($1,$2,$3)
		 ON CONFLICT (tenant_slug, email) DO UPDATE SET role = EXCLUDED.role`,
		slug, email, role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveMembership revokes a user's assignment to a tenant.
func (s *Store) RemoveMembership(ctx context.Context, slug, email string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM memberships WHERE tenant_slug = $1 AND lower(email) = lower($2)`, slug, email)
	return err
}

// MembershipsForTenant lists a tenant's assigned users (platform/admin view).
func (s *Store) MembershipsForTenant(ctx context.Context, slug string) ([]model.Membership, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT tenant_slug, email, coalesce(oid,''), role, created_at
		 FROM memberships WHERE tenant_slug = $1 ORDER BY email`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Membership{}
	for rows.Next() {
		var m model.Membership
		if err := rows.Scan(&m.TenantSlug, &m.Email, &m.OID, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// BindMemberships binds a signed-in user's Entra oid onto any memberships that
// were created for their email before they had ever signed in. Called on /me.
func (s *Store) BindMemberships(ctx context.Context, oid, email string) error {
	if strings.TrimSpace(oid) == "" || strings.TrimSpace(email) == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE memberships SET oid = $1
		 WHERE lower(email) = lower($2) AND (oid IS NULL OR oid = '')`, oid, email)
	return err
}

// IsMember reports whether a user (by oid or, if not yet bound, email) is
// assigned to a tenant — the per-tenant authorization check for platform-hosted
// tenants (and any tenant a user is explicitly assigned to).
func (s *Store) IsMember(ctx context.Context, slug, oid, email string) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM memberships
		 WHERE tenant_slug = $1 AND ((oid IS NOT NULL AND oid = $2) OR lower(email) = lower($3))`,
		slug, oid, email).Scan(&n)
	return n > 0, err
}

// MembershipTenants returns every tenant a user is assigned to (by bound oid or
// email), for the console's tenant switcher + the access gate.
func (s *Store) MembershipTenants(ctx context.Context, oid, email string) ([]model.Tenant, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+tenantCols+` FROM tenants t
		 WHERE t.id IN (
		   SELECT tenant_slug FROM memberships
		   WHERE (oid IS NOT NULL AND oid = $1) OR lower(email) = lower($2)
		 ) ORDER BY t.name`, oid, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Tenant{}
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// HasEnabledMembership reports whether a user is assigned to at least one ENABLED
// tenant — the access gate for a platform-directory user who is not an admin.
func (s *Store) HasEnabledMembership(ctx context.Context, oid, email string) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM memberships m JOIN tenants t ON t.id = m.tenant_slug
		 WHERE t.enabled = true AND ((m.oid IS NOT NULL AND m.oid = $1) OR lower(m.email) = lower($2))`,
		oid, email).Scan(&n)
	return n > 0, err
}
