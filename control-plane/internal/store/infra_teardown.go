package store

import "context"

// InfraTeardown is a queued cross-tenant teardown: the Azure resources an
// infrastructure entity's ARM deployment created in a tenant's subscription must
// be deleted, then the queue row cleared.
type InfraTeardown struct {
	TenantSlug     string
	SubscriptionID string
	InfraID        string
}

// enqueueInfraTeardown captures a teardown for (slug, infraID) — but only if it
// was actually provisioned (has a deployment) into a known subscription. There's
// nothing in Azure to remove otherwise. Idempotent.
func enqueueInfraTeardown(ctx context.Context, q querier, slug, infraID string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO infra_teardowns (tenant_slug, subscription_id, infra_id)
		 SELECT ti.tenant_slug, t.subscription_id, ti.infra_id
		 FROM tenant_infrastructure ti JOIN tenants t ON t.id = ti.tenant_slug
		 WHERE ti.tenant_slug = $1 AND ti.infra_id = $2
		   AND ti.infra_state <> '' AND coalesce(t.subscription_id, '') <> ''
		 ON CONFLICT (tenant_slug, infra_id) DO NOTHING`,
		slug, infraID)
	return err
}

// enqueueInfraTeardownsForDefinition captures a teardown for every tenant that
// provisioned infraID — used when the definition itself is deleted (it may be
// live in many tenants).
func enqueueInfraTeardownsForDefinition(ctx context.Context, q querier, infraID string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO infra_teardowns (tenant_slug, subscription_id, infra_id)
		 SELECT ti.tenant_slug, t.subscription_id, ti.infra_id
		 FROM tenant_infrastructure ti JOIN tenants t ON t.id = ti.tenant_slug
		 WHERE ti.infra_id = $1
		   AND ti.infra_state <> '' AND coalesce(t.subscription_id, '') <> ''
		 ON CONFLICT (tenant_slug, infra_id) DO NOTHING`,
		infraID)
	return err
}

// InfraTeardowns returns every queued teardown, oldest first, for the provisioner.
func (s *Store) InfraTeardowns(ctx context.Context) ([]InfraTeardown, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT tenant_slug, subscription_id, infra_id FROM infra_teardowns ORDER BY requested_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InfraTeardown
	for rows.Next() {
		var td InfraTeardown
		if err := rows.Scan(&td.TenantSlug, &td.SubscriptionID, &td.InfraID); err != nil {
			return nil, err
		}
		out = append(out, td)
	}
	return out, rows.Err()
}

// ClearInfraTeardown removes a queued teardown once its Azure resources are gone
// (or were never there). Also used to cancel a teardown when the infra is
// re-enabled before the sweep runs.
func (s *Store) ClearInfraTeardown(ctx context.Context, slug, infraID string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM infra_teardowns WHERE tenant_slug = $1 AND infra_id = $2`, slug, infraID)
	return err
}
