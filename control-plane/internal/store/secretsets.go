package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/inception42/cortex/control-plane/internal/model"
	"github.com/inception42/cortex/shared"
)

// Secret sets — a declared collection of secret KEYS, and which of them a tenant
// has supplied a value for.
//
// No function in this file accepts or returns a secret value, and that is the
// point rather than an omission. Values go straight from the request handler to
// the tenant's own Key Vault through the ARM management plane, which can write a
// secret but cannot read one back. The database records only key NAMES, so the
// worst a compromise of this control plane yields is the shape of a tenant's
// secrets, never their contents.
//
// EnableSecretSet therefore takes `keysSet` — the keys the caller has already
// successfully written to the vault — not the values it wrote.

var (
	// ErrSecretSetNotAccessible is a set the tenant neither owns nor is entitled to.
	ErrSecretSetNotAccessible = errors.New("secret set not accessible")
	// ErrSecretSetInUse is a set an enabled entity still depends on.
	ErrSecretSetInUse = errors.New("secret set in use")
	// ErrUndeclaredKey is a value supplied for a key the set does not declare.
	ErrUndeclaredKey = errors.New("undeclared key")
)

const secretSetCols = `s.id, s.name, s.description, s.owner_tenant, s.keys, s.created_by, s.created_at`

func secretSetScanDest(x *model.SecretSet) []any {
	return []any{&x.ID, &x.Name, &x.Description, &x.Owner, &x.Keys, &x.CreatedBy, &x.CreatedAt}
}

/* ── Reads ───────────────────────────────────────────────────────────────── */

// SecretSetList is the platform view: every set, with its owner's display name.
func (s *Store) SecretSetList(ctx context.Context) ([]model.SecretSet, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+secretSetCols+`, coalesce(t.name,'')
		   FROM secret_sets s
		   LEFT JOIN tenants t ON t.id = s.owner_tenant
		  ORDER BY s.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.SecretSet{}
	for rows.Next() {
		var x model.SecretSet
		dest := append(secretSetScanDest(&x), &x.OwnerName)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		x.Platform = x.Owner == ""
		out = append(out, x)
	}
	return out, rows.Err()
}

// SecretSetsForTenant is the tenant view: the sets it owns or is entitled to,
// with which keys it has filled in. Never the values.
func (s *Store) SecretSetsForTenant(ctx context.Context, slug string) ([]model.SecretSet, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+secretSetCols+`,
		        (ts.tenant_slug IS NOT NULL) AS enabled,
		        coalesce(ts.health,''), coalesce(ts.keys_set,'{}'), coalesce(ts.detail,'')
		   FROM secret_sets s
		   LEFT JOIN tenant_secret_sets ts ON ts.set_id = s.id AND ts.tenant_slug = $1
		  WHERE s.owner_tenant = $1
		     OR (s.owner_tenant = '' AND s.id = ANY(
		           SELECT unnest(entitled_secret_sets) FROM tenants WHERE id = $1))
		  ORDER BY s.name`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.SecretSet{}
	for rows.Next() {
		var x model.SecretSet
		dest := append(secretSetScanDest(&x), &x.Enabled, &x.Health, &x.KeysSet, &x.Detail)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		x.Platform = x.Owner == ""
		x.Owned = x.Owner == slug
		x.Entitled = x.Platform
		out = append(out, x)
	}
	return out, rows.Err()
}

// SecretSetByID reads one set.
func (s *Store) SecretSetByID(ctx context.Context, id string) (model.SecretSet, error) {
	var x model.SecretSet
	err := s.pool.QueryRow(ctx, `SELECT `+secretSetCols+` FROM secret_sets s WHERE s.id = $1`, id).
		Scan(secretSetScanDest(&x)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return x, ErrNotFound
	}
	x.Platform = x.Owner == ""
	return x, err
}

/* ── Writes ──────────────────────────────────────────────────────────────── */

// UpdateSecretSet edits a set's name, description and declared keys.
//
// Removing a key does not remove the value from any tenant's vault: the control
// plane cannot read it to confirm what it would be destroying, and a key removed
// by mistake would be unrecoverable. The orphaned vault secret stops being
// delivered, which is the observable effect that matters, and is reclaimed when
// the set is disabled.
func (s *Store) UpdateSecretSet(ctx context.Context, x model.SecretSet) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE secret_sets SET name = $2, description = $3, keys = $4 WHERE id = $1`,
		x.ID, x.Name, x.Description, x.Keys)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// A key that no longer exists must stop counting as "set", or the console
	// would keep reporting a set as complete on the strength of a key nobody
	// declares any more.
	_, err = s.pool.Exec(ctx,
		`UPDATE tenant_secret_sets ts
		    SET keys_set = coalesce(ARRAY(SELECT unnest(ts.keys_set) INTERSECT SELECT unnest($2::text[])),'{}')
		  WHERE ts.set_id = $1`, x.ID, x.Keys)
	return err
}

// DeleteSecretSet removes a set from the catalog and every tenant that had it.
func (s *Store) DeleteSecretSet(ctx context.Context, id string) error {
	return s.deleteCascade(ctx, "secret_sets", id,
		`DELETE FROM tenant_secret_sets WHERE set_id = $1`,
		`UPDATE tenants SET entitled_secret_sets = array_remove(entitled_secret_sets, $1)`,
		`UPDATE applications SET dependencies = dependencies - jsonb_build_object('kind','secret_set','id',$1::text)::text`,
		`UPDATE infrastructure SET dependencies = dependencies - jsonb_build_object('kind','secret_set','id',$1::text)::text`,
	)
}

// SetSecretSetEntitlements replaces which platform sets a tenant may use.
func (s *Store) SetSecretSetEntitlements(ctx context.Context, slug string, ids []string) error {
	return s.setEntitlements(ctx, slug, model.DepSecretSet, ids)
}

// secretSetAccessible reports whether a tenant owns or is entitled to a set.
func (s *Store) secretSetAccessible(ctx context.Context, slug, id string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(
		   SELECT 1 FROM secret_sets s WHERE s.id = $2 AND (
		     s.owner_tenant = $1 OR
		     (s.owner_tenant = '' AND s.id = ANY(SELECT unnest(entitled_secret_sets) FROM tenants WHERE id = $1))))`,
		slug, id).Scan(&ok)
	return ok, err
}

// DeclaredKeys returns a set's declared key names, for validating that a caller
// is not trying to store something the author never asked for.
func (s *Store) DeclaredKeys(ctx context.Context, id string) ([]string, error) {
	var keys []string
	err := s.pool.QueryRow(ctx, `SELECT keys FROM secret_sets WHERE id = $1`, id).Scan(&keys)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return keys, err
}

// EnableSecretSet marks a set enabled for a tenant and records which keys now
// have a value in the tenant's vault.
//
// `keysSet` is the keys the caller has ALREADY written to the vault — this
// function does not touch the vault and never sees a value. Keys already set by
// an earlier call are preserved, so supplying one key later does not appear to
// unset the others.
func (s *Store) EnableSecretSet(ctx context.Context, slug, id string, keysSet []string, vaultURI string) error {
	ok, err := s.secretSetAccessible(ctx, slug, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrSecretSetNotAccessible
	}
	declared, err := s.DeclaredKeys(ctx, id)
	if err != nil {
		return err
	}
	if keysSet == nil {
		keysSet = []string{}
	}
	// Health is derived, not asserted: a set is only live once every declared
	// key has a value, because a half-filled set produces a Secret that silently
	// lacks the key a chart is about to ask for.
	_, err = s.pool.Exec(ctx,
		`INSERT INTO tenant_secret_sets (tenant_slug, set_id, health, auto, keys_set, vault_uri, detail, sort_order)
		 VALUES ($1,$2,'reconciling',false,
		         coalesce(ARRAY(SELECT DISTINCT unnest($3::text[]) INTERSECT SELECT unnest($4::text[])),'{}'),
		         $5,'',
		         coalesce((SELECT max(sort_order)+1 FROM tenant_secret_sets WHERE tenant_slug=$1),1))
		 ON CONFLICT (tenant_slug, set_id) DO UPDATE SET
		   auto = false,
		   vault_uri = $5,
		   keys_set = coalesce(ARRAY(
		     SELECT DISTINCT unnest(tenant_secret_sets.keys_set || $3::text[])
		     INTERSECT SELECT unnest($4::text[])),'{}')`,
		slug, id, keysSet, declared, vaultURI)
	if err != nil {
		return err
	}
	if err := s.refreshSecretSetHealth(ctx, slug, id); err != nil {
		return err
	}
	return s.autoEnableDeps(ctx, slug, model.DepSecretSet, id)
}

// refreshSecretSetHealth derives health from completeness: blocked while a
// declared key is outstanding, reconciling once they all have values (the
// reconciler then reports live when the Secret exists in the cluster).
func (s *Store) refreshSecretSetHealth(ctx context.Context, slug, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE tenant_secret_sets ts SET
		   health = CASE WHEN outstanding.n > 0 THEN 'blocked' ELSE 'reconciling' END,
		   detail = CASE WHEN outstanding.n > 0
		                 THEN outstanding.n || ' key' || CASE WHEN outstanding.n = 1 THEN '' ELSE 's' END || ' still need a value.'
		                 ELSE '' END
		  FROM (
		    SELECT count(*) AS n
		      FROM secret_sets s, unnest(s.keys) k
		     WHERE s.id = $2
		       AND k <> ALL(coalesce((SELECT keys_set FROM tenant_secret_sets WHERE tenant_slug=$1 AND set_id=$2),'{}'))
		  ) outstanding
		 WHERE ts.tenant_slug = $1 AND ts.set_id = $2 AND ts.health <> 'live'`,
		slug, id)
	return err
}

// DisableSecretSet turns a set off for a tenant, refusing while an enabled
// entity still depends on it.
func (s *Store) DisableSecretSet(ctx context.Context, slug, id string) error {
	deps, err := s.enabledDependents(ctx, slug, model.DepSecretSet, id)
	if err != nil {
		return err
	}
	if len(deps) > 0 {
		return fmt.Errorf("%w: %s %q still depends on it", ErrSecretSetInUse, deps[0].Kind, deps[0].ID)
	}
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM tenant_secret_sets WHERE tenant_slug = $1 AND set_id = $2`, slug, id); err != nil {
		return err
	}
	return s.pruneAutoDeps(ctx, slug)
}

// SetSecretSetHealth records what the reconciler observed in the cluster.
func (s *Store) SetSecretSetHealth(ctx context.Context, slug, id, health, detail string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE tenant_secret_sets SET health = $3, detail = $4
		  WHERE tenant_slug = $1 AND set_id = $2`, slug, id, health, trunc256(detail))
	return err
}

/* ── Sync ────────────────────────────────────────────────────────────────── */

// desiredSecretSets builds the sync payload for a tenant's enabled sets. It
// carries key NAMES and the vault to read them from — never a value. The
// reconciler, which runs inside the tenant's own subscription, does the reading.
func (s *Store) desiredSecretSets(ctx context.Context, slug string) ([]shared.DesiredSecretSet, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT s.id, s.name, s.keys, coalesce(ts.keys_set,'{}'), coalesce(ts.vault_uri,'')
		   FROM tenant_secret_sets ts
		   JOIN secret_sets s ON s.id = ts.set_id
		  WHERE ts.tenant_slug = $1
		  ORDER BY ts.sort_order`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []shared.DesiredSecretSet
	for rows.Next() {
		var d shared.DesiredSecretSet
		var declared, set []string
		if err := rows.Scan(&d.ID, &d.Name, &declared, &set, &d.VaultURI); err != nil {
			return nil, err
		}
		// Only keys that actually have a value are worth asking the reconciler
		// to fetch; an outstanding key would just be a guaranteed 404.
		have := make(map[string]bool, len(set))
		for _, k := range set {
			have[k] = true
		}
		for _, k := range declared {
			if have[k] {
				d.Keys = append(d.Keys, k)
			}
		}
		d.SecretName = model.SecretSet{ID: d.ID}.SecretName()
		d.Complete = len(d.Keys) == len(declared)
		if d.VaultURI != "" && len(d.Keys) > 0 {
			out = append(out, d)
		}
	}
	return out, rows.Err()
}

// secretSetSources exposes a set's non-secret facts for wiring: the name of the
// Kubernetes Secret it materialises as, and each key's name.
//
// This is the whole reason a secret can be wired into a chart without ever
// appearing in its values. The author binds `secretName` into whatever the chart
// calls its existingSecret option; the value itself is never part of the wiring
// vocabulary, so there is no path by which it could reach the Argo Application.
func secretSetSources(sets []shared.DesiredSecretSet) map[string]map[string]any {
	out := make(map[string]map[string]any, len(sets))
	for _, s := range sets {
		m := map[string]any{"secretName": s.SecretName}
		for _, k := range s.Keys {
			m["key:"+k] = k
		}
		out["secret_set:"+s.ID] = m
	}
	return out
}

/* ── The tenant's vault ──────────────────────────────────────────────────── */

// SetTenantVault records where a tenant's secret values live, from its footprint
// outputs.
func (s *Store) SetTenantVault(ctx context.Context, slug, name, uri, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE tenants SET vault_name = $2, vault_uri = $3, vault_id = $4 WHERE id = $1`,
		slug, name, uri, id)
	return err
}

// SetTenantNetwork records the private-networking facts a footprint created, so
// a service's module can be given them without discovering them itself.
func (s *Store) SetTenantNetwork(ctx context.Context, slug, peSubnetID, oidcIssuer, dnsZoneRG string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE tenants SET pe_subnet_id = $2, aks_oidc_issuer = $3, dns_zone_rg = $4 WHERE id = $1`,
		slug, peSubnetID, oidcIssuer, dnsZoneRG)
	return err
}

// TenantVault returns the ARM resource id and URI of a tenant's own vault. Both
// empty until the footprint that creates it has finished deploying.
func (s *Store) TenantVault(ctx context.Context, slug string) (vaultID, vaultURI string, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT coalesce(vault_id,''), coalesce(vault_uri,'') FROM tenants WHERE id = $1`, slug).
		Scan(&vaultID, &vaultURI)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return vaultID, vaultURI, err
}

// SecretSetKeysSet returns the keys a tenant has supplied a value for, so a
// disable knows which vault secrets to reclaim.
func (s *Store) SecretSetKeysSet(ctx context.Context, slug, id string) ([]string, error) {
	var keys []string
	err := s.pool.QueryRow(ctx,
		`SELECT coalesce(keys_set,'{}') FROM tenant_secret_sets WHERE tenant_slug = $1 AND set_id = $2`,
		slug, id).Scan(&keys)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return keys, err
}
