package store

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inception42/cortex/control-plane/internal/model"
	"github.com/inception42/cortex/shared"
)

//go:embed schema.sql
var schemaSQL string

// defToText marshals an agent definition for a jsonb column (text is cast to
// jsonb by Postgres on write).
func defToText(d shared.AgentDefinition) string {
	b, err := json.Marshal(d)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// defFromRaw unmarshals a jsonb definition (empty/invalid → zero value).
func defFromRaw(raw []byte) shared.AgentDefinition {
	var d shared.AgentDefinition
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &d)
	}
	return d
}

//go:embed seed.sql
var seedSQL string

var ErrNotFound = errors.New("not found")

type Store struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, schemaSQL)
	return err
}

func (s *Store) Seed(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, seedSQL)
	return err
}

const tenantCols = `id, name, coalesce(tenant_id,''), region, plan, enrollment, version,
	agent_count, reconciling_count, monthly_calls, drift, last_heartbeat,
	subscription_id, reconciler_identity, foundry_project, reconciler_version, installed_at`

func scanTenant(row pgx.Row) (model.Tenant, error) {
	var t model.Tenant
	var installedAt string
	err := row.Scan(&t.ID, &t.Name, &t.TenantID, &t.Region, &t.Plan, &t.Enrollment,
		&t.Version, &t.AgentCount, &t.ReconcilingCount, &t.MonthlyCalls, &t.Drift, &t.LastHeartbeat,
		&t.SubscriptionID, &t.ReconcilerIdentity, &t.FoundryProject, &t.ReconcilerVersion, &installedAt)
	if installedAt != "" {
		t.InstalledAt = &installedAt
	}
	t.Lifecycle = deriveLifecycle(t.Enrollment, t.LastHeartbeat)
	return t, err
}

// heartbeatFreshWindow is how long after its last heartbeat a bound tenant is
// still considered live; past it, the reconciler is presumed unhealthy.
const heartbeatFreshWindow = 30 * time.Second

// deriveLifecycle maps stored enrollment + heartbeat freshness to the tenant's
// operational lifecycle, surfaced as a badge in the console:
//
//	enrolling  — not bound yet, or bound but awaiting the first heartbeat
//	live       — bound and heartbeating within the freshness window
//	degraded   — bound, had heartbeated, but has now gone stale (reconciler down)
//	suspended  — administratively suspended
func deriveLifecycle(enrollment string, lastHeartbeat *time.Time) string {
	switch enrollment {
	case "suspended":
		return "suspended"
	case "bound":
		if lastHeartbeat == nil {
			return "enrolling" // installed/bound but no reconciler report yet
		}
		if time.Since(*lastHeartbeat) < heartbeatFreshWindow {
			return "live"
		}
		return "degraded"
	default:
		return "enrolling"
	}
}

// Fleet returns every customer tenant (excludes the platform tenant) + stats.
func (s *Store) Fleet(ctx context.Context) (model.FleetResponse, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+tenantCols+` FROM tenants WHERE is_platform = false ORDER BY last_heartbeat DESC NULLS LAST`)
	if err != nil {
		return model.FleetResponse{}, err
	}
	defer rows.Close()

	var tenants []model.Tenant
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return model.FleetResponse{}, err
		}
		tenants = append(tenants, t)
	}
	if err := rows.Err(); err != nil {
		return model.FleetResponse{}, err
	}
	return model.FleetResponse{Stats: computeStats(tenants), Tenants: tenants}, nil
}

func (s *Store) TenantBySlug(ctx context.Context, slug string) (model.Tenant, error) {
	t, err := scanTenant(s.pool.QueryRow(ctx, `SELECT `+tenantCols+` FROM tenants WHERE id = $1`, slug))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (s *Store) TenantByTID(ctx context.Context, tid string) (model.Tenant, error) {
	t, err := scanTenant(s.pool.QueryRow(ctx, `SELECT `+tenantCols+` FROM tenants WHERE tenant_id = $1`, tid))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

func (s *Store) Agents(ctx context.Context, slug string) ([]model.Agent, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT a.agent_id, a.name, coalesce(ca.type,'prompt'), a.version, a.channel, a.model,
		        a.health, a.publish_to, a.calls_30d, a.note,
		        coalesce((SELECT v.version FROM catalog_versions v
		                  WHERE v.agent_id = a.agent_id AND v.channel = a.channel
		                  ORDER BY v.created_at DESC LIMIT 1), a.version) AS desired_version,
		        coalesce((SELECT v.definition FROM catalog_versions v
		                  WHERE v.agent_id = a.agent_id AND v.channel = a.channel
		                  ORDER BY v.created_at DESC LIMIT 1), '{}'::jsonb) AS definition,
		        a.memory_store
		 FROM agents a LEFT JOIN catalog_agents ca ON ca.id = a.agent_id
		 WHERE a.tenant_slug = $1 ORDER BY a.sort_order, a.name`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	agents := []model.Agent{}
	for rows.Next() {
		var a model.Agent
		var defRaw []byte
		var override string
		if err := rows.Scan(&a.ID, &a.Name, &a.Type, &a.Version, &a.Channel, &a.Model,
			&a.Health, &a.PublishTo, &a.Calls30d, &a.Note,
			&a.DesiredVersion, &defRaw, &override); err != nil {
			return nil, err
		}
		a.Definition = defFromRaw(defRaw)
		a.MemoryStore = firstNonEmpty(override, a.Definition.MemoryStore)
		a.Drift = a.DesiredVersion != "" && a.DesiredVersion != a.Version
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// firstNonEmpty returns a if non-empty, else b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// UpsertUser records the authenticated user and their resolved role/tenant.
func (s *Store) UpsertUser(ctx context.Context, id model.Identity, tenantSlug *string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO users (oid, tid, name, email, role, tenant_slug, last_login)
		 VALUES ($1,$2,$3,$4,$5,$6, now())
		 ON CONFLICT (oid) DO UPDATE SET
		   tid = EXCLUDED.tid, name = EXCLUDED.name, email = EXCLUDED.email,
		   role = EXCLUDED.role, tenant_slug = EXCLUDED.tenant_slug, last_login = now()`,
		id.OID, id.TID, id.Name, id.Email, string(id.Role), tenantSlug)
	return err
}

// EnsureTenantForTID JIT-provisions a tenant row for a real signed-in directory.
func (s *Store) EnsureTenantForTID(ctx context.Context, tid, name string) (model.Tenant, error) {
	if t, err := s.TenantByTID(ctx, tid); err == nil {
		return t, nil
	} else if !errors.Is(err, ErrNotFound) {
		return model.Tenant{}, err
	}
	slug := "t-" + strings.ReplaceAll(tid, "-", "")[:12]
	if name == "" {
		name = "New tenant"
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, tenant_id, region, plan, enrollment, version)
		 VALUES ($1,$2,$3,'—','team','pending','')
		 ON CONFLICT (tenant_id) DO NOTHING`,
		slug, name, tid)
	if err != nil {
		return model.Tenant{}, err
	}
	return s.TenantByTID(ctx, tid)
}

func computeStats(tenants []model.Tenant) model.FleetStats {
	st := model.FleetStats{Tenants: len(tenants)}
	latest := ""
	for _, t := range tenants {
		if t.Enrollment == "bound" {
			st.Bound++
		}
		st.Agents += t.AgentCount
		st.CallsMonth += t.MonthlyCalls
		if t.Version != "" && compareVersions(t.Version, latest) > 0 {
			latest = t.Version
		}
	}
	st.LatestVersion = latest
	for _, t := range tenants {
		if t.Version == latest && latest != "" {
			st.OnLatest++
		}
	}
	return st
}

// compareVersions compares dotted numeric versions ("1.6.2" vs "1.6.1").
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

/* ── Catalog ────────────────────────────────────────────────────────────── */

func (s *Store) CatalogList(ctx context.Context) ([]model.CatalogAgent, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, description, coalesce(type,'prompt'), model, created_at FROM catalog_agents ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	order := []string{}
	byID := map[string]*model.CatalogAgent{}
	for rows.Next() {
		var a model.CatalogAgent
		if err := rows.Scan(&a.ID, &a.Name, &a.Description, &a.Type, &a.Model, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Versions = []model.CatalogVersion{}
		byID[a.ID] = &a
		order = append(order, a.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(order) == 0 {
		return []model.CatalogAgent{}, nil
	}

	vrows, err := s.pool.Query(ctx,
		`SELECT agent_id, version, channel, notes, rollout_percent, definition, created_at
		 FROM catalog_versions ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer vrows.Close()
	for vrows.Next() {
		var agentID string
		var defRaw []byte
		var v model.CatalogVersion
		if err := vrows.Scan(&agentID, &v.Version, &v.Channel, &v.Notes, &v.RolloutPercent, &defRaw, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.Definition = defFromRaw(defRaw)
		if a := byID[agentID]; a != nil {
			a.Versions = append(a.Versions, v)
			if a.LatestVersion == "" || compareVersions(v.Version, a.LatestVersion) > 0 {
				a.LatestVersion = v.Version
			}
		}
	}
	if err := vrows.Err(); err != nil {
		return nil, err
	}

	out := make([]model.CatalogAgent, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

// CatalogForTenant returns only the agents a tenant is entitled to, flagged with
// whether each is already enabled.
func (s *Store) CatalogForTenant(ctx context.Context, slug string) ([]model.CatalogAgent, error) {
	var entitled []string
	if err := s.pool.QueryRow(ctx,
		`SELECT entitled_agents FROM tenants WHERE id = $1`, slug).Scan(&entitled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []model.CatalogAgent{}, nil
		}
		return nil, err
	}
	entitledSet := map[string]bool{}
	for _, id := range entitled {
		entitledSet[id] = true
	}

	enabledSet := map[string]bool{}
	erows, err := s.pool.Query(ctx, `SELECT agent_id FROM agents WHERE tenant_slug = $1`, slug)
	if err != nil {
		return nil, err
	}
	for erows.Next() {
		var id string
		if err := erows.Scan(&id); err != nil {
			erows.Close()
			return nil, err
		}
		enabledSet[id] = true
	}
	erows.Close()

	all, err := s.CatalogList(ctx)
	if err != nil {
		return nil, err
	}
	out := []model.CatalogAgent{}
	for _, a := range all {
		if !entitledSet[a.ID] {
			continue
		}
		a.Entitled = true
		a.Enabled = enabledSet[a.ID]
		out = append(out, a)
	}
	return out, nil
}

func (s *Store) CreateCatalogAgent(ctx context.Context, id, name, description, agentType, agentModel, createdBy string, def shared.AgentDefinition) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO catalog_agents (id, name, description, type, model, created_by) VALUES ($1,$2,$3,$4,$5,$6)`,
		id, name, description, agentType, agentModel, createdBy); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO catalog_versions (id, agent_id, version, channel, notes, rollout_percent, definition)
		 VALUES ($1,$2,'1.0.0','stable','Initial version',100,$3)`,
		id+":1.0.0", id, defToText(def)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) PublishVersion(ctx context.Context, agentID, version, channel, notes string, rollout int, def shared.AgentDefinition) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO catalog_versions (id, agent_id, version, channel, notes, rollout_percent, definition)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		agentID+":"+version, agentID, version, channel, notes, rollout, defToText(def))
	return err
}

/* ── Memory stores ──────────────────────────────────────────────────────── */

const memoryStoreCols = `m.id, m.name, m.description, m.owner_tenant,
	m.chat_model, m.embedding_model, m.user_profile_enabled, m.user_profile_details,
	m.chat_summary_enabled, m.procedural_memory_enabled, m.ttl_seconds,
	m.created_by, m.created_at`

// memStoreScanDest is the ordered scan target for memoryStoreCols, so every read
// path decodes the typed definition columns identically.
func memStoreScanDest(m *model.MemoryStore) []any {
	d := &m.Definition
	return []any{&m.ID, &m.Name, &m.Description, &m.Owner,
		&d.ChatModel, &d.EmbeddingModel, &d.UserProfileEnabled, &d.UserProfileDetails,
		&d.ChatSummaryEnabled, &d.ProceduralMemoryEnabled, &d.TTLSeconds,
		&m.CreatedBy, &m.CreatedAt}
}

func scanMemoryStore(row pgx.Row) (model.MemoryStore, error) {
	var m model.MemoryStore
	err := row.Scan(memStoreScanDest(&m)...)
	m.Platform = m.Owner == ""
	return m, err
}

// MemoryStoreList returns every memory store (platform view), with owner names.
func (s *Store) MemoryStoreList(ctx context.Context) ([]model.MemoryStore, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+memoryStoreCols+`, coalesce(t.name,'')
		 FROM memory_stores m LEFT JOIN tenants t ON t.id = m.owner_tenant
		 ORDER BY m.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.MemoryStore{}
	for rows.Next() {
		var m model.MemoryStore
		if err := rows.Scan(append(memStoreScanDest(&m), &m.OwnerName)...); err != nil {
			return nil, err
		}
		m.Platform = m.Owner == ""
		out = append(out, m)
	}
	return out, rows.Err()
}

// MemoryStoresForTenant returns the stores a tenant can use: its own, plus the
// platform stores it's entitled to, each flagged accordingly.
func (s *Store) MemoryStoresForTenant(ctx context.Context, slug string) ([]model.MemoryStore, error) {
	var entitled []string
	if err := s.pool.QueryRow(ctx, `SELECT entitled_stores FROM tenants WHERE id = $1`, slug).Scan(&entitled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []model.MemoryStore{}, nil
		}
		return nil, err
	}
	entitledSet := map[string]bool{}
	for _, id := range entitled {
		entitledSet[id] = true
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+memoryStoreCols+` FROM memory_stores m
		 WHERE m.owner_tenant = $1 OR (m.owner_tenant = '' AND m.id = ANY($2))
		 ORDER BY m.created_at DESC`, slug, entitled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.MemoryStore{}
	for rows.Next() {
		m, err := scanMemoryStore(rows)
		if err != nil {
			return nil, err
		}
		m.Owned = m.Owner == slug
		m.Entitled = entitledSet[m.ID]
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) MemoryStoreByID(ctx context.Context, id string) (model.MemoryStore, error) {
	m, err := scanMemoryStore(s.pool.QueryRow(ctx, `SELECT `+memoryStoreCols+` FROM memory_stores m WHERE m.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

func (s *Store) CreateMemoryStore(ctx context.Context, id, name, description, owner string, def shared.MemoryStoreDefinition, createdBy string) error {
	_, err := s.pool.Exec(ctx,
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

// UpdateMemoryStore updates only the store's name + description. The definition
// (models + memory kinds) is immutable: the Foundry memory_store resource has no
// update surface (create/delete only), so Cortex mirrors that and never lets a
// definition edit silently diverge from what's provisioned.
func (s *Store) UpdateMemoryStore(ctx context.Context, id, name, description string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE memory_stores SET name = $2, description = $3 WHERE id = $1`,
		id, name, description)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteMemoryStore(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM memory_stores WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// Detach the store from tenant entitlements + agent connections.
	_, _ = s.pool.Exec(ctx, `UPDATE tenants SET entitled_stores = array_remove(entitled_stores, $1)`, id)
	_, _ = s.pool.Exec(ctx, `UPDATE agents SET memory_store = '' WHERE memory_store = $1`, id)
	return nil
}

func (s *Store) SetStoreEntitlements(ctx context.Context, slug string, storeIDs []string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE tenants SET entitled_stores = $2 WHERE id = $1`, slug, storeIDs)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

var ErrStoreNotAccessible = errors.New("memory store not accessible")

// ConnectAgentStore connects (storeID != "") or disconnects (storeID == "") an
// enabled agent to a memory store the tenant owns or is entitled to.
func (s *Store) ConnectAgentStore(ctx context.Context, slug, catalogAgentID, storeID string) error {
	if storeID != "" {
		var ok bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM memory_stores m WHERE m.id = $2 AND
			   (m.owner_tenant = $1 OR (m.owner_tenant = '' AND m.id IN
			     (SELECT unnest(entitled_stores) FROM tenants WHERE id = $1))))`,
			slug, storeID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return ErrStoreNotAccessible
		}
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE agents SET memory_store = $3 WHERE tenant_slug = $1 AND agent_id = $2`, slug, catalogAgentID, storeID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// referencedStores returns the platform memory-store ids referenced by the
// latest definition of each given catalog agent.
func (s *Store) referencedStores(ctx context.Context, agentIDs []string) ([]string, error) {
	if len(agentIDs) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT m.id FROM memory_stores m WHERE m.owner_tenant = '' AND m.id IN (
		   SELECT (SELECT v.definition->>'memoryStore' FROM catalog_versions v
		           WHERE v.agent_id = ca.id ORDER BY v.created_at DESC LIMIT 1)
		   FROM catalog_agents ca WHERE ca.id = ANY($1))`, agentIDs)
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

// autoEntitleStores ensures a tenant is entitled to every platform memory store
// referenced by the given agents — so entitling or enabling an agent also grants
// the stores it needs.
func (s *Store) autoEntitleStores(ctx context.Context, slug string, agentIDs []string) error {
	stores, err := s.referencedStores(ctx, agentIDs)
	if err != nil || len(stores) == 0 {
		return err
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE tenants SET entitled_stores =
		   (SELECT coalesce(array_agg(DISTINCT e), '{}') FROM unnest(entitled_stores || $2::text[]) e)
		 WHERE id = $1`, slug, stores)
	return err
}

/* ── Tenants registry + entitlements ────────────────────────────────────── */

func (s *Store) TenantsRegistry(ctx context.Context) ([]model.TenantRegistryRow, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+tenantCols+`, entitled_agents, entitled_stores FROM tenants WHERE is_platform = false ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.TenantRegistryRow{}
	for rows.Next() {
		var r model.TenantRegistryRow
		var installedAt string
		if err := rows.Scan(&r.ID, &r.Name, &r.TenantID, &r.Region, &r.Plan, &r.Enrollment,
			&r.Version, &r.AgentCount, &r.ReconcilingCount, &r.MonthlyCalls, &r.Drift, &r.LastHeartbeat,
			&r.SubscriptionID, &r.ReconcilerIdentity, &r.FoundryProject, &r.ReconcilerVersion, &installedAt,
			&r.EntitledAgents, &r.EntitledStores); err != nil {
			return nil, err
		}
		if installedAt != "" {
			r.InstalledAt = &installedAt
		}
		r.Lifecycle = deriveLifecycle(r.Enrollment, r.LastHeartbeat)
		if r.EntitledAgents == nil {
			r.EntitledAgents = []string{}
		}
		if r.EntitledStores == nil {
			r.EntitledStores = []string{}
		}
		r.EntitledCount = len(r.EntitledAgents)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) SetEntitlements(ctx context.Context, slug string, agentIDs []string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE tenants SET entitled_agents = $2 WHERE id = $1`, slug, agentIDs)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// Ensure the tenant is entitled to every memory store these agents need.
	return s.autoEntitleStores(ctx, slug, agentIDs)
}

/* ── Enable / disable / install (tenant desired state) ──────────────────── */

var ErrNotEntitled = errors.New("not entitled")

func (s *Store) EnableAgent(ctx context.Context, slug, catalogAgentID string, publishTo []string) error {
	var entitled bool
	if err := s.pool.QueryRow(ctx,
		`SELECT $2 = ANY(entitled_agents) FROM tenants WHERE id = $1`, slug, catalogAgentID).Scan(&entitled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if !entitled {
		return ErrNotEntitled
	}

	// Ensure the tenant is entitled to any platform memory store this agent uses.
	if err := s.autoEntitleStores(ctx, slug, []string{catalogAgentID}); err != nil {
		return err
	}

	var name, agentModel, version, channel string
	err := s.pool.QueryRow(ctx,
		`SELECT ca.name, ca.model,
		        coalesce((SELECT version FROM catalog_versions v WHERE v.agent_id = ca.id ORDER BY created_at DESC LIMIT 1),'1.0.0'),
		        coalesce((SELECT channel FROM catalog_versions v WHERE v.agent_id = ca.id ORDER BY created_at DESC LIMIT 1),'stable')
		 FROM catalog_agents ca WHERE ca.id = $1`, catalogAgentID).Scan(&name, &agentModel, &version, &channel)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if len(publishTo) == 0 {
		publishTo = []string{"api"}
	}

	// Enabling records DESIRED state. Actual health is unknown until the tenant's
	// reconciler pulls this, converges it in Foundry, and reports back — so a new
	// agent starts 'reconciling' (converging), never a fabricated 'healthy'.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO agents (id, tenant_slug, agent_id, name, version, channel, model, health, publish_to, calls_30d, sort_order)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,'reconciling',$8,0,
		         coalesce((SELECT max(sort_order)+1 FROM agents WHERE tenant_slug=$2),1))
		 ON CONFLICT (id) DO NOTHING`,
		slug+":"+catalogAgentID, slug, catalogAgentID, name, version, channel, agentModel, publishTo); err != nil {
		return err
	}
	return s.recountAgents(ctx, slug)
}

func (s *Store) DisableAgent(ctx context.Context, slug, catalogAgentID string) error {
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM agents WHERE tenant_slug = $1 AND agent_id = $2`, slug, catalogAgentID); err != nil {
		return err
	}
	return s.recountAgents(ctx, slug)
}

// MarkInstalled was a pre-reconciler stub that faked the in-tenant install by
// writing binding + identity straight into the DB. It's gone: binding now comes
// only from the reconciler's heartbeat (ApplyHeartbeat), so the control plane
// never fabricates install state. The admin launches the managed-app deployment
// from the console; the reconciler reports authoritative identity when it boots.

// recountAgents derives the tenant's rollups from its agent rows: how many
// agents it runs, and its 30-day call volume (the sum of per-agent counts the
// reconciler reports). Both are derived, never client-supplied.
func (s *Store) recountAgents(ctx context.Context, slug string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE tenants SET
		   agent_count   = (SELECT count(*) FROM agents WHERE tenant_slug = $1),
		   monthly_calls = (SELECT coalesce(sum(calls_30d), 0) FROM agents WHERE tenant_slug = $1)
		 WHERE id = $1`, slug)
	return err
}

/* ── Reconciler sync + heartbeat ─────────────────────────────────────────── */

// SyncDesired returns the desired state (enabled agents) for a tenant's
// reconciler. Unknown tenants get an empty set (they register via heartbeat).
func (s *Store) SyncDesired(ctx context.Context, tid string) (shared.DesiredState, error) {
	out := shared.DesiredState{TenantID: tid, Agents: []shared.DesiredAgent{}}
	t, err := s.TenantByTID(ctx, tid)
	if errors.Is(err, ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT a.agent_id, a.name, coalesce(ca.type,'prompt'),
		        coalesce((SELECT v.version FROM catalog_versions v
		                  WHERE v.agent_id = a.agent_id AND v.channel = a.channel
		                  ORDER BY v.created_at DESC LIMIT 1), a.version) AS desired_version,
		        a.channel, a.model,
		        coalesce((SELECT v.definition FROM catalog_versions v
		                  WHERE v.agent_id = a.agent_id AND v.channel = a.channel
		                  ORDER BY v.created_at DESC LIMIT 1), '{}'::jsonb) AS definition,
		        a.publish_to, a.memory_store
		 FROM agents a LEFT JOIN catalog_agents ca ON ca.id = a.agent_id
		 WHERE a.tenant_slug = $1 ORDER BY a.sort_order`, t.ID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	storeIDs := map[string]bool{}
	for rows.Next() {
		var a shared.DesiredAgent
		var typeStr string
		var defRaw []byte
		var override string
		if err := rows.Scan(&a.AgentID, &a.Name, &typeStr, &a.Version, &a.Channel, &a.Model, &defRaw, &a.PublishTo, &override); err != nil {
			return out, err
		}
		a.Type = shared.AgentType(typeStr)
		a.Definition = defFromRaw(defRaw)
		// The effective store is the per-tenant override, else the catalog default.
		if eff := firstNonEmpty(override, a.Definition.MemoryStore); eff != "" {
			a.Definition.MemoryStore = eff
			storeIDs[eff] = true
		}
		out.Agents = append(out.Agents, a)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	// Attach the typed definition of every memory store the desired agents
	// reference, so the reconciler can provision each store and bind agents to it.
	if len(storeIDs) > 0 {
		ids := make([]string, 0, len(storeIDs))
		for id := range storeIDs {
			ids = append(ids, id)
		}
		srows, err := s.pool.Query(ctx,
			`SELECT id, name, chat_model, embedding_model, user_profile_enabled,
			        user_profile_details, chat_summary_enabled, procedural_memory_enabled, ttl_seconds
			 FROM memory_stores WHERE id = ANY($1)`, ids)
		if err != nil {
			return out, err
		}
		defer srows.Close()
		for srows.Next() {
			var ms shared.DesiredMemoryStore
			d := &ms.Definition
			if err := srows.Scan(&ms.ID, &ms.Name, &d.ChatModel, &d.EmbeddingModel,
				&d.UserProfileEnabled, &d.UserProfileDetails, &d.ChatSummaryEnabled,
				&d.ProceduralMemoryEnabled, &d.TTLSeconds); err != nil {
				return out, err
			}
			out.MemoryStores = append(out.MemoryStores, ms)
		}
		if err := srows.Err(); err != nil {
			return out, err
		}
	}
	return out, nil
}

// ApplyHeartbeat records a reconciler heartbeat: it upserts the tenant with the
// authoritative in-tenant install details (name, region, subscription, reconciler
// identity, Foundry project) and updates each managed agent's actual health.
func (s *Store) ApplyHeartbeat(ctx context.Context, hb shared.Heartbeat) error {
	if hb.TenantID == "" {
		return errors.New("heartbeat missing tenantId")
	}
	t, err := s.EnsureTenantForTID(ctx, hb.TenantID, hb.TenantName)
	if err != nil {
		return err
	}

	reconciling := 0
	for _, a := range hb.Agents {
		if a.Health == "reconciling" {
			reconciling++
		}
	}

	if _, err := s.pool.Exec(ctx,
		`UPDATE tenants SET
		   name = coalesce(nullif($2,''), name),
		   region = coalesce(nullif($3,''), region),
		   plan = coalesce(nullif($4,''), plan),
		   enrollment = 'bound',
		   reconciling_count = $5,
		   last_heartbeat = now(),
		   version = coalesce(nullif($6,''), version),
		   reconciler_version = coalesce(nullif($6,''), reconciler_version),
		   subscription_id = coalesce(nullif($7,''), subscription_id),
		   reconciler_identity = coalesce(nullif($8,''), reconciler_identity),
		   foundry_project = coalesce(nullif($9,''), foundry_project),
		   installed_at = coalesce(nullif(installed_at,''), to_char(now(),'YYYY-MM-DD'))
		 WHERE id = $1`,
		t.ID, hb.TenantName, hb.Region, hb.Plan, reconciling,
		hb.ReconcilerVersion, hb.SubscriptionID, hb.ReconcilerIdentity, hb.FoundryProject); err != nil {
		return err
	}

	for _, a := range hb.Agents {
		if _, err := s.pool.Exec(ctx,
			`UPDATE agents SET health = $3, calls_30d = $4, version = coalesce(nullif($5,''), version)
			 WHERE tenant_slug = $1 AND agent_id = $2`,
			t.ID, a.AgentID, a.Health, a.Calls30d, a.Version); err != nil {
			return err
		}
	}
	return s.recountAgents(ctx, t.ID)
}
