package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/inception42/cortex/control-plane/internal/model"
	"github.com/inception42/cortex/shared"
)

// depErrorf wraps a sentinel dependency error with a formatted message.
func depErrorf(base error, format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{base}, args...)...)
}

// querier is the subset of pgxpool.Pool / pgx.Tx that the insert helpers need, so
// the same INSERT can run either directly on the pool (single-resource create) or
// inside a transaction (the unified Apply).
type querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

/* ── Transaction-safe inserts (shared by the single-create methods + Apply) ── */

func insertInfrastructure(ctx context.Context, q querier, i model.Infrastructure, createdBy string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO infrastructure (id, name, description, owner_tenant, bicep, arm_template, bicep_params, bicep_outputs, dependencies, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		i.ID, i.Name, i.Description, i.Owner, i.BicepModule, i.ArmTemplate, paramsJSON(i.BicepParams), depsArray(i.BicepOutputs), depsJSON(i.Dependencies), createdBy)
	return err
}

func insertApplication(ctx context.Context, q querier, a model.Application, createdBy string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO applications (id, name, description, owner_tenant, namespace, repo_url, chart, target_revision, values, wiring, dependencies, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		a.ID, a.Name, a.Description, a.Owner, a.Namespace, a.RepoURL, a.Chart, a.TargetRevision, a.Values,
		wiringJSON(a.Wiring), depsJSON(a.Dependencies), createdBy)
	return err
}

func insertMemoryStore(ctx context.Context, q querier, id, name, description, owner string, def shared.MemoryStoreDefinition, createdBy string) error {
	_, err := q.Exec(ctx,
		`INSERT INTO memory_stores
		   (id, name, description, owner_tenant,
		    chat_model, embedding_model, user_profile_enabled, user_profile_details,
		    chat_summary_enabled, procedural_memory_enabled, ttl_seconds, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		id, name, description, owner,
		def.ChatModel, def.EmbeddingModel, def.UserProfileEnabled, def.UserProfileDetails,
		def.ChatSummaryEnabled, def.ProceduralMemoryEnabled, def.TTLSeconds, createdBy)
	return err
}

func insertCatalogAgent(ctx context.Context, q querier, id, name, description, agentType, agentModel, owner, createdBy string, def shared.AgentDefinition) error {
	if _, err := q.Exec(ctx,
		`INSERT INTO catalog_agents (id, name, description, type, model, owner_tenant, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		id, name, description, agentType, agentModel, owner, createdBy); err != nil {
		return err
	}
	// catalog_versions is kept as the internal single-definition store.
	_, err := q.Exec(ctx,
		`INSERT INTO catalog_versions (id, agent_id, version, channel, notes, rollout_percent, definition)
		 VALUES ($1,$2,'1.0.0','stable','Initial version',100,$3)`,
		id+":1.0.0", id, defToText(def))
	return err
}

/* ── Unified transactional apply ─────────────────────────────────────────── */

// ApplyAgent is a catalog agent to create (the create-time fields only).
type ApplyAgent struct {
	ID          string
	Name        string
	Description string
	Type        string
	Model       string
	Owner       string
	Definition  shared.AgentDefinition
}

// ApplyBatch is a set of resources to create together, in one transaction. IDs,
// ownership, and (for infrastructure) the resolved ARM template are assigned by
// the caller (the HTTP handler) before Apply; Apply validates the dependency graph
// across the whole batch, then inserts everything atomically.
type ApplyBatch struct {
	Infrastructure []model.Infrastructure
	MemoryStores   []model.MemoryStore
	Agents         []ApplyAgent
	Applications   []model.Application
}

// ApplyResult is the ids of the created resources, by kind.
type ApplyResult struct {
	Infrastructure []string `json:"infrastructure"`
	MemoryStores   []string `json:"memoryStores"`
	Agents         []string `json:"agents"`
	Applications   []string `json:"applications"`
}

type applyItem struct {
	kind  model.DepKind
	id    string
	owner string
	deps  []model.Dependency
}

// Apply validates + creates a batch of resources in a single transaction. It's the
// one write path for creation (the single-resource create endpoints send a batch
// of one; a future CLI can send a whole application graph). Resources are inserted
// in dependency order (infrastructure → memory stores → agents → applications), and
// the batch's own members satisfy each other's dependencies.
func (s *Store) Apply(ctx context.Context, createdBy string, b ApplyBatch) (ApplyResult, error) {
	items := make([]applyItem, 0, len(b.Infrastructure)+len(b.MemoryStores)+len(b.Agents)+len(b.Applications))
	for _, i := range b.Infrastructure {
		items = append(items, applyItem{model.DepInfrastructure, i.ID, i.Owner, i.Dependencies})
	}
	for _, m := range b.MemoryStores {
		items = append(items, applyItem{model.DepMemoryStore, m.ID, m.Owner, nil})
	}
	for _, a := range b.Agents {
		var deps []model.Dependency
		if a.Definition.MemoryStore != "" {
			deps = []model.Dependency{{Kind: model.DepMemoryStore, ID: a.Definition.MemoryStore}}
		}
		items = append(items, applyItem{model.DepAgent, a.ID, a.Owner, deps})
	}
	for _, ap := range b.Applications {
		items = append(items, applyItem{model.DepApplication, ap.ID, ap.Owner, ap.Dependencies})
	}
	if err := s.validateBatch(ctx, items); err != nil {
		return ApplyResult{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ApplyResult{}, err
	}
	defer tx.Rollback(ctx)

	var res ApplyResult
	for _, i := range b.Infrastructure {
		if err := insertInfrastructure(ctx, tx, i, createdBy); err != nil {
			return ApplyResult{}, err
		}
		res.Infrastructure = append(res.Infrastructure, i.ID)
	}
	for _, m := range b.MemoryStores {
		if err := insertMemoryStore(ctx, tx, m.ID, m.Name, m.Description, m.Owner, m.Definition, createdBy); err != nil {
			return ApplyResult{}, err
		}
		res.MemoryStores = append(res.MemoryStores, m.ID)
	}
	for _, a := range b.Agents {
		if err := insertCatalogAgent(ctx, tx, a.ID, a.Name, a.Description, a.Type, a.Model, a.Owner, createdBy, a.Definition); err != nil {
			return ApplyResult{}, err
		}
		res.Agents = append(res.Agents, a.ID)
	}
	for _, ap := range b.Applications {
		if err := insertApplication(ctx, tx, ap, createdBy); err != nil {
			return ApplyResult{}, err
		}
		res.Applications = append(res.Applications, ap.ID)
	}
	if err := tx.Commit(ctx); err != nil {
		return ApplyResult{}, err
	}
	return res, nil
}

// validateBatch validates the dependency graph of a create batch: every edge is
// allowed, every target exists + is accessible (a target already in the DB, or a
// sibling in this same batch), and the combined graph (DB ∪ batch) has no cycle.
func (s *Store) validateBatch(ctx context.Context, items []applyItem) error {
	pending := make(map[string]applyItem, len(items))
	for _, it := range items {
		pending[string(it.kind)+"/"+it.id] = it
	}
	for _, it := range items {
		seen := map[string]bool{}
		for _, d := range it.deps {
			k := string(d.Kind) + "/" + d.ID
			if seen[k] {
				continue
			}
			seen[k] = true
			if !edgeAllowed(it.kind, d.Kind) {
				return depErrorf(ErrBadDependency, "a %s cannot depend on a %s", it.kind, d.Kind)
			}
			if d.Kind == it.kind && d.ID == it.id {
				return depErrorf(ErrDependencyCycle, "%s %q cannot depend on itself", it.kind, it.id)
			}
			depOwner, exists := "", false
			if p, ok := pending[k]; ok {
				depOwner, exists = p.owner, true
			} else {
				o, ok2, err := s.entityOwner(ctx, d.Kind, d.ID)
				if err != nil {
					return err
				}
				depOwner, exists = o, ok2
			}
			if !exists {
				return depErrorf(ErrBadDependency, "%s %q does not exist", d.Kind, d.ID)
			}
			if !ownerCanUse(it.owner, depOwner) {
				return depErrorf(ErrBadDependency, "%s %q is not accessible to this owner", d.Kind, d.ID)
			}
		}
	}
	// Cycle check across the combined graph: a batch member uses its own proposed
	// deps; anything else uses its stored deps.
	const inProgress, done = 1, 2
	color := map[string]int{}
	var dfs func(kind model.DepKind, id string) error
	dfs = func(kind model.DepKind, id string) error {
		k := string(kind) + "/" + id
		switch color[k] {
		case inProgress:
			return depErrorf(ErrDependencyCycle, "through %s %q", kind, id)
		case done:
			return nil
		}
		color[k] = inProgress
		var ds []model.Dependency
		if p, ok := pending[k]; ok {
			ds = p.deps
		} else {
			var err error
			if ds, err = s.directDeps(ctx, kind, id); err != nil {
				return err
			}
		}
		for _, d := range ds {
			if err := dfs(d.Kind, d.ID); err != nil {
				return err
			}
		}
		color[k] = done
		return nil
	}
	for _, it := range items {
		if err := dfs(it.kind, it.id); err != nil {
			return err
		}
	}
	return nil
}
