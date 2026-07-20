package store

import "context"

// InfraTeardown is a provisioned tenant instance whose Azure resources must be
// removed (its row is marked pending_delete).
type InfraTeardown struct {
	TenantSlug     string
	SubscriptionID string
	InfraID        string
}

// InfraTeardownTargets returns every instance marked for teardown that has a
// subscription to delete resources from. Rows without a subscription were never
// provisioned cross-tenant, so the provisioner finalizes them without any Azure
// work.
func (s *Store) InfraTeardownTargets(ctx context.Context) ([]InfraTeardown, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT ti.tenant_slug, coalesce(t.subscription_id, ''), ti.infra_id
		 FROM tenant_infrastructure ti JOIN tenants t ON t.id = ti.tenant_slug
		 WHERE ti.pending_delete = true
		 ORDER BY ti.tenant_slug, ti.infra_id`)
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

// FinalizeInfraTeardown removes a torn-down instance and — if that was the last
// instance of a definition that is itself being deleted — removes the definition.
// Called by the provisioner once the Azure resources are gone.
func (s *Store) FinalizeInfraTeardown(ctx context.Context, slug, infraID string) error {
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM tenant_infrastructure WHERE tenant_slug = $1 AND infra_id = $2`, slug, infraID); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx,
		`DELETE FROM infrastructure WHERE id = $1 AND pending_delete = true
		   AND NOT EXISTS (SELECT 1 FROM tenant_infrastructure WHERE infra_id = $1)`, infraID)
	return err
}
