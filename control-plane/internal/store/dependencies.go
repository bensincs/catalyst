package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/inception42/cortex/control-plane/internal/model"
)

// Dependency graph — the typed edges between catalog entities and the enforcement
// that keeps authoring, entitlements, and per-tenant enablement consistent with
// them. Allowed edges:
//
//	infrastructure → infrastructure
//	application    → infrastructure | application | agent
//	agent          → memory_store
//	memory_store   → (leaf)
//
// Enforced at four points: author (edge allowed, target exists + accessible, no
// cycles), entitle (cascade to transitive deps), enable (auto-enable transitive
// deps), and disable (refuse while an enabled entity still depends on it).

var (
	// ErrBadDependency is an invalid dependency (disallowed edge, missing or
	// inaccessible target).
	ErrBadDependency = errors.New("invalid dependency")
	// ErrDependencyCycle is a dependency cycle.
	ErrDependencyCycle = errors.New("dependency cycle")
	// ErrInUse is returned when disabling an entity that an enabled entity still
	// depends on.
	ErrInUse = errors.New("in use by an enabled dependent")
	// ErrEntitlementInUse is returned when un-entitling an entity the tenant has
	// enabled, or that another entitled entity still depends on.
	ErrEntitlementInUse = errors.New("entitlement in use")
)

// allowedEdges maps an entity kind to the kinds it may depend on.
var allowedEdges = map[model.DepKind]map[model.DepKind]bool{
	model.DepInfrastructure: {model.DepInfrastructure: true},
	model.DepApplication:    {model.DepInfrastructure: true, model.DepApplication: true, model.DepAgent: true},
	model.DepAgent:          {model.DepMemoryStore: true},
	model.DepMemoryStore:    {},
}

func edgeAllowed(from, to model.DepKind) bool { return allowedEdges[from][to] }

// entitlementColumn maps a kind to its tenant entitlement array column.
var entitlementColumn = map[model.DepKind]string{
	model.DepInfrastructure: "entitled_infrastructure",
	model.DepApplication:    "entitled_deployments",
	model.DepAgent:          "entitled_agents",
	model.DepMemoryStore:    "entitled_stores",
}

// allKinds is the fixed set of entity kinds, for closures over the whole graph.
var allKinds = []model.DepKind{model.DepInfrastructure, model.DepApplication, model.DepAgent, model.DepMemoryStore}

/* ── jsonb (de)serialization ────────────────────────────────────────────── */

func depsFromRaw(raw []byte) []model.Dependency {
	out := []model.Dependency{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func depsJSON(d []model.Dependency) []byte {
	if len(d) == 0 {
		return []byte("[]")
	}
	b, err := json.Marshal(d)
	if err != nil {
		return []byte("[]")
	}
	return b
}

/* ── Graph reads ─────────────────────────────────────────────────────────── */

// directDeps returns the outgoing dependency edges of one entity. Infrastructure
// and applications store them in a `dependencies` jsonb column; an agent's edge
// is derived from the memory store its latest catalog version references; a
// memory store is a leaf.
func (s *Store) directDeps(ctx context.Context, kind model.DepKind, id string) ([]model.Dependency, error) {
	switch kind {
	case model.DepInfrastructure:
		return s.depsColumn(ctx, "infrastructure", id)
	case model.DepApplication:
		return s.depsColumn(ctx, "applications", id)
	case model.DepAgent:
		var store *string
		err := s.pool.QueryRow(ctx,
			`SELECT (SELECT v.definition->>'memoryStore' FROM catalog_versions v
			         WHERE v.agent_id = $1 ORDER BY v.created_at DESC LIMIT 1)`, id).Scan(&store)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, nil
			}
			return nil, err
		}
		if store != nil && *store != "" {
			return []model.Dependency{{Kind: model.DepMemoryStore, ID: *store}}, nil
		}
		return nil, nil
	default:
		return nil, nil
	}
}

func (s *Store) depsColumn(ctx context.Context, table, id string) ([]model.Dependency, error) {
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT dependencies FROM `+table+` WHERE id = $1`, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return depsFromRaw(raw), nil
}

// entityOwner returns the owner_tenant of an entity and whether it exists.
func (s *Store) entityOwner(ctx context.Context, kind model.DepKind, id string) (owner string, exists bool, err error) {
	table := map[model.DepKind]string{
		model.DepInfrastructure: "infrastructure",
		model.DepApplication:    "applications",
		model.DepAgent:          "catalog_agents",
		model.DepMemoryStore:    "memory_stores",
	}[kind]
	if table == "" {
		return "", false, fmt.Errorf("%w: unknown kind %q", ErrBadDependency, kind)
	}
	err = s.pool.QueryRow(ctx, `SELECT owner_tenant FROM `+table+` WHERE id = $1`, id).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return owner, err == nil, err
}

// ownerCanUse reports whether an entity owned by `owner` may depend on an entity
// owned by `depOwner`. A platform entity ("") may only depend on platform
// entities; a tenant entity may depend on platform entities or its own.
func ownerCanUse(owner, depOwner string) bool {
	if owner == "" {
		return depOwner == ""
	}
	return depOwner == "" || depOwner == owner
}

/* ── Author-time validation ─────────────────────────────────────────────── */

// ValidateDependencies checks that every proposed dependency of an entity is a
// legal edge, points at an existing + accessible target, and introduces no cycle.
func (s *Store) ValidateDependencies(ctx context.Context, entityKind model.DepKind, entityID, owner string, deps []model.Dependency) error {
	seen := map[string]bool{}
	for _, d := range deps {
		key := string(d.Kind) + "/" + d.ID
		if seen[key] {
			continue
		}
		seen[key] = true

		if !edgeAllowed(entityKind, d.Kind) {
			return fmt.Errorf("%w: a %s cannot depend on a %s", ErrBadDependency, entityKind, d.Kind)
		}
		if d.Kind == entityKind && d.ID == entityID {
			return fmt.Errorf("%w: %s %q cannot depend on itself", ErrDependencyCycle, entityKind, entityID)
		}
		depOwner, ok, err := s.entityOwner(ctx, d.Kind, d.ID)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: %s %q does not exist", ErrBadDependency, d.Kind, d.ID)
		}
		if !ownerCanUse(owner, depOwner) {
			return fmt.Errorf("%w: %s %q is not accessible to this owner", ErrBadDependency, d.Kind, d.ID)
		}
	}
	return s.checkNoCycle(ctx, entityKind, entityID, deps)
}

// checkNoCycle DFS-colors the graph reachable from (startKind,startID), using the
// PROPOSED deps for the start node and stored deps everywhere else; a back-edge
// to an in-progress node is a cycle.
func (s *Store) checkNoCycle(ctx context.Context, startKind model.DepKind, startID string, proposed []model.Dependency) error {
	const (
		inProgress = 1
		done       = 2
	)
	color := map[string]int{}
	var dfs func(kind model.DepKind, id string, deps []model.Dependency, useProposed bool) error
	dfs = func(kind model.DepKind, id string, deps []model.Dependency, useProposed bool) error {
		key := string(kind) + "/" + id
		switch color[key] {
		case inProgress:
			return fmt.Errorf("%w: through %s %q", ErrDependencyCycle, kind, id)
		case done:
			return nil
		}
		color[key] = inProgress
		var ds []model.Dependency
		if useProposed {
			ds = deps
		} else {
			var err error
			if ds, err = s.directDeps(ctx, kind, id); err != nil {
				return err
			}
		}
		for _, d := range ds {
			if err := dfs(d.Kind, d.ID, nil, false); err != nil {
				return err
			}
		}
		color[key] = done
		return nil
	}
	return dfs(startKind, startID, proposed, true)
}

/* ── Entitlement cascade ────────────────────────────────────────────────── */

// cascadeEntitlements extends a tenant's entitlement arrays to the transitive
// closure of dependencies of everything it's entitled to. Only platform-owned
// entities are entitled (tenant-owned deps are the tenant's own, not entitled).
// Additive: never removes an entitlement.
func (s *Store) cascadeEntitlements(ctx context.Context, slug string) error {
	ent := map[model.DepKind]map[string]bool{}
	for _, k := range allKinds {
		set, err := s.entitledSet(ctx, slug, k)
		if err != nil {
			return err
		}
		ent[k] = set
	}
	changed := true
	for changed {
		changed = false
		for _, k := range allKinds {
			for id := range ent[k] {
				deps, err := s.directDeps(ctx, k, id)
				if err != nil {
					return err
				}
				for _, d := range deps {
					owner, ok, err := s.entityOwner(ctx, d.Kind, d.ID)
					if err != nil {
						return err
					}
					if !ok || owner != "" { // only platform entities are entitled
						continue
					}
					if !ent[d.Kind][d.ID] {
						ent[d.Kind][d.ID] = true
						changed = true
					}
				}
			}
		}
	}
	for _, k := range allKinds {
		if err := s.writeEntitled(ctx, slug, k, keys(ent[k])); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) entitledSet(ctx context.Context, slug string, kind model.DepKind) (map[string]bool, error) {
	var ids []string
	err := s.pool.QueryRow(ctx, `SELECT `+entitlementColumn[kind]+` FROM tenants WHERE id = $1`, slug).Scan(&ids)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set, nil
}

func (s *Store) writeEntitled(ctx context.Context, slug string, kind model.DepKind, ids []string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE tenants SET `+entitlementColumn[kind]+` = $2 WHERE id = $1`, slug, ids)
	return err
}

// ensureEntitled adds one platform entity to a tenant's entitlement array (a
// no-op for tenant-owned entities, which need no entitlement).
func (s *Store) ensureEntitled(ctx context.Context, slug string, kind model.DepKind, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE tenants SET `+entitlementColumn[kind]+` =
		   (SELECT coalesce(array_agg(DISTINCT e),'{}') FROM unnest(`+entitlementColumn[kind]+` || ARRAY[$2]) e)
		 WHERE id = $1 AND EXISTS (SELECT 1 FROM `+tableFor(kind)+` WHERE id = $2 AND owner_tenant = '')`,
		slug, id)
	return err
}

func tableFor(kind model.DepKind) string {
	return map[model.DepKind]string{
		model.DepInfrastructure: "infrastructure",
		model.DepApplication:    "applications",
		model.DepAgent:          "catalog_agents",
		model.DepMemoryStore:    "memory_stores",
	}[kind]
}

// setEntitlements is the single write path for a tenant's entitlements of one
// kind. It enforces the un-entitle guard (symmetric with the disable guard),
// applies the new set, then cascades DOWN to keep the set dependency-closed.
func (s *Store) setEntitlements(ctx context.Context, slug string, kind model.DepKind, newIDs []string) error {
	if newIDs == nil {
		newIDs = []string{}
	}
	// Tenant must exist.
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tenants WHERE id = $1)`, slug).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	// Compute what's being removed and guard it.
	old, err := s.entitledSet(ctx, slug, kind)
	if err != nil {
		return err
	}
	next := make(map[string]bool, len(newIDs))
	for _, id := range newIDs {
		next[id] = true
	}
	for id := range old {
		if next[id] {
			continue
		}
		if err := s.guardUnentitle(ctx, slug, kind, id, next); err != nil {
			return err
		}
	}
	if err := s.writeEntitled(ctx, slug, kind, newIDs); err != nil {
		return err
	}
	return s.cascadeEntitlements(ctx, slug)
}

// guardUnentitle refuses to remove an entitlement the tenant still relies on: the
// entity is enabled, or another entity that WILL REMAIN entitled depends on it.
// `next` is the resulting entitled set for this kind (so removing an entity and
// the same-kind dependency it needs, together, is allowed).
func (s *Store) guardUnentitle(ctx context.Context, slug string, kind model.DepKind, id string, next map[string]bool) error {
	enabled, err := s.enabledInTenant(ctx, slug, kind, id)
	if err != nil {
		return err
	}
	if enabled {
		return fmt.Errorf("%w: %s %q is enabled by the tenant — disable it first", ErrEntitlementInUse, kind, id)
	}
	deps, err := s.entitledDependents(ctx, slug, kind, id)
	if err != nil {
		return err
	}
	for _, d := range deps {
		if d.Kind == kind && !next[d.ID] {
			continue // that dependent is being un-entitled in the same request
		}
		return fmt.Errorf("%w: %s %q is still required by an entitled %s — un-entitle that first", ErrEntitlementInUse, kind, id, d.Kind)
	}
	return nil
}

// enabledInTenant reports whether (kind,id) is currently enabled in the tenant.
func (s *Store) enabledInTenant(ctx context.Context, slug string, kind model.DepKind, id string) (bool, error) {
	q := map[model.DepKind]string{
		model.DepInfrastructure: `SELECT EXISTS(SELECT 1 FROM tenant_infrastructure WHERE tenant_slug=$1 AND infra_id=$2)`,
		model.DepApplication:    `SELECT EXISTS(SELECT 1 FROM tenant_deployments WHERE tenant_slug=$1 AND app_id=$2)`,
		model.DepAgent:          `SELECT EXISTS(SELECT 1 FROM agents WHERE tenant_slug=$1 AND agent_id=$2)`,
		model.DepMemoryStore:    `SELECT EXISTS(SELECT 1 FROM tenant_stores WHERE tenant_slug=$1 AND store_id=$2)`,
	}[kind]
	var ok bool
	err := s.pool.QueryRow(ctx, q, slug, id).Scan(&ok)
	return ok, err
}

// entitledDependents returns the entities ENTITLED to a tenant that depend on
// (kind,id) — the un-entitle analog of enabledDependents.
func (s *Store) entitledDependents(ctx context.Context, slug string, kind model.DepKind, id string) ([]model.Dependency, error) {
	edge := string(depsJSON([]model.Dependency{{Kind: kind, ID: id}}))
	var out []model.Dependency

	// Entitled applications depending on it (apps depend on infra/app/agent).
	if kind == model.DepInfrastructure || kind == model.DepApplication || kind == model.DepAgent {
		rows, err := s.pool.Query(ctx,
			`SELECT a.id FROM applications a
			 WHERE a.id IN (SELECT unnest(entitled_deployments) FROM tenants WHERE id=$1)
			   AND a.dependencies @> $2::jsonb AND a.id <> $3`, slug, edge, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var d string
			if err := rows.Scan(&d); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, model.Dependency{Kind: model.DepApplication, ID: d})
		}
		rows.Close()
	}
	// Entitled infrastructure depending on it (infra → infra).
	if kind == model.DepInfrastructure {
		rows, err := s.pool.Query(ctx,
			`SELECT i.id FROM infrastructure i
			 WHERE i.id IN (SELECT unnest(entitled_infrastructure) FROM tenants WHERE id=$1)
			   AND i.dependencies @> $2::jsonb AND i.id <> $3`, slug, edge, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var d string
			if err := rows.Scan(&d); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, model.Dependency{Kind: model.DepInfrastructure, ID: d})
		}
		rows.Close()
	}
	// Entitled agents that reference this memory store (agent → memory_store).
	if kind == model.DepMemoryStore {
		var dependent *string
		err := s.pool.QueryRow(ctx,
			`SELECT ca.id FROM catalog_agents ca
			 WHERE ca.id IN (SELECT unnest(entitled_agents) FROM tenants WHERE id=$1)
			   AND (SELECT v.definition->>'memoryStore' FROM catalog_versions v
			        WHERE v.agent_id = ca.id ORDER BY v.created_at DESC LIMIT 1) = $2
			 LIMIT 1`, slug, id).Scan(&dependent)
		if err == nil && dependent != nil {
			out = append(out, model.Dependency{Kind: model.DepAgent, ID: *dependent})
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	return out, nil
}

/* ── Enable cascade ─────────────────────────────────────────────────────── */

// autoEnableDeps recursively enables (as auto rows) every direct dependency of a
// just-enabled entity, so enabling an application also installs the infrastructure,
// applications, and agents it needs (and their transitive deps).
func (s *Store) autoEnableDeps(ctx context.Context, slug string, kind model.DepKind, id string) error {
	deps, err := s.directDeps(ctx, kind, id)
	if err != nil {
		return err
	}
	for _, d := range deps {
		if err := s.autoEnableOne(ctx, slug, d.Kind, d.ID); err != nil {
			return err
		}
	}
	return nil
}

// autoEnableOne entitles (if platform) + enables one dependency as an auto row,
// then recurses into its own dependencies. Accessible-but-inaccessible targets
// are skipped defensively.
func (s *Store) autoEnableOne(ctx context.Context, slug string, kind model.DepKind, id string) error {
	if err := s.ensureEntitled(ctx, slug, kind, id); err != nil {
		return err
	}
	switch kind {
	case model.DepInfrastructure:
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO tenant_infrastructure (tenant_slug, infra_id, health, auto, sort_order)
			 VALUES ($1,$2,'reconciling',true,
			         coalesce((SELECT max(sort_order)+1 FROM tenant_infrastructure WHERE tenant_slug=$1),1))
			 ON CONFLICT (tenant_slug, infra_id) DO NOTHING`, slug, id); err != nil {
			return err
		}
	case model.DepApplication:
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO tenant_deployments (tenant_slug, app_id, health, auto, sort_order)
			 VALUES ($1,$2,'reconciling',true,
			         coalesce((SELECT max(sort_order)+1 FROM tenant_deployments WHERE tenant_slug=$1),1))
			 ON CONFLICT (tenant_slug, app_id) DO NOTHING`, slug, id); err != nil {
			return err
		}
	case model.DepAgent:
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO agents (id, tenant_slug, agent_id, name, version, channel, model, health, publish_to, calls_30d, auto, sort_order)
			 SELECT $1||':'||ca.id, $1, ca.id, ca.name,
			        coalesce((SELECT version FROM catalog_versions v WHERE v.agent_id=ca.id ORDER BY created_at DESC LIMIT 1),'1.0.0'),
			        coalesce((SELECT channel FROM catalog_versions v WHERE v.agent_id=ca.id ORDER BY created_at DESC LIMIT 1),'stable'),
			        ca.model,'reconciling','{api}',0,true,
			        coalesce((SELECT max(sort_order)+1 FROM agents WHERE tenant_slug=$1),1)
			 FROM catalog_agents ca WHERE ca.id = $2
			 ON CONFLICT (id) DO NOTHING`, slug, id); err != nil {
			return err
		}
		if err := s.recountAgents(ctx, slug); err != nil {
			return err
		}
	case model.DepMemoryStore:
		if err := s.autoEnableStores(ctx, slug, []string{id}); err != nil {
			return err
		}
	}
	return s.autoEnableDeps(ctx, slug, kind, id)
}

/* ── Disable in-use guard + auto prune ──────────────────────────────────── */

// enabledDependents returns the entities enabled in a tenant that directly depend
// on (kind,id) — the guard that refuses to disable something still in use.
func (s *Store) enabledDependents(ctx context.Context, slug string, kind model.DepKind, id string) ([]model.Dependency, error) {
	var out []model.Dependency
	edge := string(depsJSON([]model.Dependency{{Kind: kind, ID: id}}))

	// Enabled applications that depend on it (apps may depend on infra/app/agent).
	if kind == model.DepInfrastructure || kind == model.DepApplication || kind == model.DepAgent {
		rows, err := s.pool.Query(ctx,
			`SELECT a.id FROM applications a
			 JOIN tenant_deployments td ON td.app_id = a.id AND td.tenant_slug = $1
			 WHERE a.dependencies @> $2::jsonb AND a.id <> $3`, slug, edge, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var dep string
			if err := rows.Scan(&dep); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, model.Dependency{Kind: model.DepApplication, ID: dep})
		}
		rows.Close()
	}
	// Enabled infrastructure that depends on it (infra → infra).
	if kind == model.DepInfrastructure {
		rows, err := s.pool.Query(ctx,
			`SELECT i.id FROM infrastructure i
			 JOIN tenant_infrastructure ti ON ti.infra_id = i.id AND ti.tenant_slug = $1
			 WHERE i.dependencies @> $2::jsonb AND i.id <> $3`, slug, edge, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var dep string
			if err := rows.Scan(&dep); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, model.Dependency{Kind: model.DepInfrastructure, ID: dep})
		}
		rows.Close()
	}
	// Enabled agents that bind this memory store (agent → memory_store).
	if kind == model.DepMemoryStore {
		refs, err := s.storesReferencedByEnabledAgents(ctx, slug)
		if err != nil {
			return nil, err
		}
		if refs[id] {
			out = append(out, model.Dependency{Kind: model.DepAgent, ID: id})
		}
	}
	return out, nil
}

// pruneAutoDeps removes auto-enabled infra/app rows that are no longer required by
// any enabled entity, plus the existing store prune — the symmetric cleanup after
// a disable. Manually-enabled rows (auto=false) are always kept.
func (s *Store) pruneAutoDeps(ctx context.Context, slug string) error {
	// Fixpoint: an auto row survives only if a still-enabled entity depends on it.
	for {
		removed := false

		// Auto infrastructure no longer depended on by an enabled app/infra.
		tag, err := s.pool.Exec(ctx,
			`DELETE FROM tenant_infrastructure ti WHERE ti.tenant_slug = $1 AND ti.auto = true
			 AND NOT EXISTS (
			   SELECT 1 FROM applications a JOIN tenant_deployments td ON td.app_id=a.id AND td.tenant_slug=$1
			   WHERE a.dependencies @> jsonb_build_array(jsonb_build_object('kind','infrastructure','id',ti.infra_id)))
			 AND NOT EXISTS (
			   SELECT 1 FROM infrastructure i JOIN tenant_infrastructure t2 ON t2.infra_id=i.id AND t2.tenant_slug=$1
			   WHERE i.id <> ti.infra_id AND i.dependencies @> jsonb_build_array(jsonb_build_object('kind','infrastructure','id',ti.infra_id)))`,
			slug)
		if err != nil {
			return err
		}
		removed = removed || tag.RowsAffected() > 0

		// Auto applications no longer depended on by an enabled app.
		tag, err = s.pool.Exec(ctx,
			`DELETE FROM tenant_deployments td WHERE td.tenant_slug = $1 AND td.auto = true
			 AND NOT EXISTS (
			   SELECT 1 FROM applications a JOIN tenant_deployments t2 ON t2.app_id=a.id AND t2.tenant_slug=$1
			   WHERE a.id <> td.app_id AND a.dependencies @> jsonb_build_array(jsonb_build_object('kind','application','id',td.app_id)))`,
			slug)
		if err != nil {
			return err
		}
		removed = removed || tag.RowsAffected() > 0

		if !removed {
			break
		}
	}
	// Auto agents no longer depended on by an enabled application.
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM agents ag WHERE ag.tenant_slug = $1 AND ag.auto = true
		 AND NOT EXISTS (
		   SELECT 1 FROM applications a JOIN tenant_deployments td ON td.app_id=a.id AND td.tenant_slug=$1
		   WHERE a.dependencies @> jsonb_build_array(jsonb_build_object('kind','agent','id',ag.agent_id)))`,
		slug); err != nil {
		return err
	}
	// Stores keep their own prune (agent-reference based).
	if err := s.pruneAutoStores(ctx, slug); err != nil {
		return err
	}
	return s.recountAgents(ctx, slug)
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
