package store

import (
	"context"

	"github.com/inception42/cortex/control-plane/internal/model"
)

// How widely a catalog entity is in use, so the console can say what deleting it
// costs before it happens.
//
// Deleting a catalog entity is unguarded and cascades: it strips the entity from
// every tenant's entitlements and drops every per-tenant enablement row. The
// reconciler then prunes the Argo Application, whose finalizer cascade-deletes
// the workloads. So one click can uninstall a running application from every
// tenant that had it — which the operator has to be told before, not after.

// Usage counts the tenants entitled to an entity and the tenants running it.
type Usage struct {
	Entitled int `json:"entitled"`
	Enabled  int `json:"enabled"`
}

// entitlementColumnFor maps a kind to the tenants column holding its
// entitlements, and the table holding per-tenant enablement.
func usageQueryFor(kind model.DepKind) (entitleCol, enabledSQL string, ok bool) {
	switch kind {
	case model.DepApplication:
		return "entitled_deployments", `SELECT count(*) FROM tenant_deployments WHERE app_id = $1`, true
	case model.DepInfrastructure:
		return "entitled_infrastructure", `SELECT count(*) FROM tenant_infrastructure WHERE infra_id = $1`, true
	case model.DepAgent:
		return "entitled_agents", `SELECT count(*) FROM agents WHERE agent_id = $1`, true
	case model.DepMemoryStore:
		return "entitled_stores", `SELECT count(*) FROM tenant_stores WHERE store_id = $1`, true
	case model.DepSecretSet:
		return "entitled_secret_sets", `SELECT count(*) FROM tenant_secret_sets WHERE set_id = $1`, true
	}
	return "", "", false
}

// UsageOf reports how many tenants are entitled to an entity and how many have
// it enabled. Both zero means deleting it affects nobody.
func (s *Store) UsageOf(ctx context.Context, kind model.DepKind, id string) (Usage, error) {
	col, enabledSQL, ok := usageQueryFor(kind)
	if !ok {
		return Usage{}, nil
	}
	var u Usage
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM tenants WHERE $1 = ANY(`+col+`)`, id).Scan(&u.Entitled); err != nil {
		return Usage{}, err
	}
	if err := s.pool.QueryRow(ctx, enabledSQL, id).Scan(&u.Enabled); err != nil {
		return Usage{}, err
	}
	return u, nil
}
