package store

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"sigs.k8s.io/yaml"

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
	subscription_id, reconciler_identity, foundry_project, reconciler_version, installed_at, enabled,
	cluster_name, cluster_phase, cluster_k8s_version, cluster_argo_installed, cluster_node_count, cluster_detail,
	cluster_ingress_installed, cluster_gateway_ip, cluster_ingress_issuer, infra_delegated, infra_detail, footprint_state, footprint_detail,
	coalesce(hosting_mode,'delegated'), coalesce(resource_group,''), coalesce(reconciler_principal_id,'')`

func scanTenant(row pgx.Row) (model.Tenant, error) {
	var t model.Tenant
	var installedAt string
	err := row.Scan(&t.ID, &t.Name, &t.TenantID, &t.Region, &t.Plan, &t.Enrollment,
		&t.Version, &t.AgentCount, &t.ReconcilingCount, &t.MonthlyCalls, &t.Drift, &t.LastHeartbeat,
		&t.SubscriptionID, &t.ReconcilerIdentity, &t.FoundryProject, &t.ReconcilerVersion, &installedAt, &t.Enabled,
		&t.Cluster.Name, &t.Cluster.Phase, &t.Cluster.K8sVersion, &t.Cluster.ArgoInstalled, &t.Cluster.NodeCount, &t.Cluster.Detail,
		&t.Cluster.IngressInstalled, &t.Cluster.GatewayIP, &t.Cluster.IngressIssuer, &t.Cluster.InfraDelegated, &t.Cluster.InfraDetail, &t.Cluster.FootprintState, &t.Cluster.FootprintDetail,
		&t.HostingMode, &t.ResourceGroup, &t.ReconcilerPrincipalID)
	if installedAt != "" {
		t.InstalledAt = &installedAt
	}
	t.Lifecycle = deriveLifecycle(t.Enrollment, t.LastHeartbeat, t.Cluster.InfraDelegated, t.Cluster.FootprintState)
	return t, err
}

// heartbeatFreshWindow is how long after its last heartbeat a bound tenant is
// still considered live; past it, the reconciler is presumed unhealthy.
const heartbeatFreshWindow = 30 * time.Second

// deriveLifecycle maps the install flow to the tenant's operational lifecycle,
// surfaced as a badge throughout the console:
//
//	pending      — not delegated yet (awaiting the Lighthouse delegation)
//	provisioning — delegated; Cortex is provisioning the footprint (reconciler + Foundry + AKS)
//	enrolling    — footprint ready, awaiting the reconciler's first heartbeat
//	live         — reconciler heartbeating within the freshness window
//	degraded     — a bound reconciler has gone stale, or provisioning failed
//	suspended    — administratively suspended
func deriveLifecycle(enrollment string, lastHeartbeat *time.Time, delegated bool, footprintState string) string {
	if enrollment == "suspended" {
		return "suspended"
	}
	// A fresh heartbeat means the reconciler is up — live, regardless of the rest.
	if lastHeartbeat != nil && time.Since(*lastHeartbeat) < heartbeatFreshWindow {
		return "live"
	}
	// Had a reconciler that has now gone stale.
	if lastHeartbeat != nil {
		return "degraded"
	}
	// Environment provisioning failed.
	if footprintState == "failed" {
		return "degraded"
	}
	if delegated {
		if footprintState == "ready" {
			return "enrolling" // provisioned; awaiting the reconciler's first heartbeat
		}
		return "provisioning" // delegated; Cortex is provisioning the footprint
	}
	return "pending" // awaiting the Lighthouse delegation
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
		`SELECT a.agent_id, a.name, coalesce(ca.type,'prompt'), a.model,
		        a.health, a.publish_to, a.calls_30d, a.note,
		        coalesce((SELECT v.definition FROM catalog_versions v
		                  WHERE v.agent_id = a.agent_id
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
		if err := rows.Scan(&a.ID, &a.Name, &a.Type, &a.Model,
			&a.Health, &a.PublishTo, &a.Calls30d, &a.Note,
			&defRaw, &override); err != nil {
			return nil, err
		}
		a.Definition = defFromRaw(defRaw)
		a.MemoryStore = firstNonEmpty(override, a.Definition.MemoryStore)
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
// If the tenant already exists but still carries the placeholder name, a better
// name learned later — the signed-in directory (sign-in), or a delegated
// subscription's display name (Lighthouse discovery) — replaces it. A real name
// already on the row is never clobbered.
func (s *Store) EnsureTenantForTID(ctx context.Context, tid, name string) (model.Tenant, error) {
	if t, err := s.TenantByTID(ctx, tid); err == nil {
		if better := strings.TrimSpace(name); better != "" && better != t.Name && isPlaceholderName(t.Name) {
			if _, e := s.pool.Exec(ctx, `UPDATE tenants SET name = $2 WHERE id = $1`, t.ID, better); e == nil {
				t.Name = better
			}
		}
		return t, nil
	} else if !errors.Is(err, ErrNotFound) {
		return model.Tenant{}, err
	}
	slug := "t-" + strings.ReplaceAll(tid, "-", "")[:12]
	if name == "" {
		name = "New tenant"
	}
	// New directories are created DISABLED — a platform admin must enable a
	// tenant before its users can sign in or its reconciler can sync.
	_, err := s.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, tenant_id, region, plan, enrollment, version, enabled, hosting_mode)
		 VALUES ($1,$2,$3,'—','team','pending','',false,'delegated')
		 ON CONFLICT DO NOTHING`,
		slug, name, tid)
	if err != nil {
		return model.Tenant{}, err
	}
	return s.TenantByTID(ctx, tid)
}

// randomSlug generates a stable, unique tenant slug for a platform-hosted tenant
// (which has no Entra directory id to derive one from). Same "t-" prefix shape as
// delegated slugs so downstream code (ownership, RGs, …) is uniform.
func randomSlug() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "t-" + hex.EncodeToString(b[:])
}

// CreatePlatformTenant creates a tenant hosted in the platform's OWN subscription
// (a dedicated resource group per tenant), with no Entra directory of its own —
// users are assigned to it via memberships. Enabled immediately (a platform admin
// created it deliberately), so the provisioner picks up its footprint next sweep.
func (s *Store) CreatePlatformTenant(ctx context.Context, name, region, plan, subscriptionID string) (model.Tenant, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "New tenant"
	}
	if strings.TrimSpace(region) == "" {
		region = "—"
	}
	if strings.TrimSpace(plan) == "" {
		plan = "team"
	}
	slug := randomSlug()
	rg := "cortex-" + slug // dedicated RG per tenant in the platform subscription
	_, err := s.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, tenant_id, region, plan, enrollment, version, enabled,
		   hosting_mode, subscription_id, resource_group, infra_delegated, infra_detail)
		 VALUES ($1,$2,NULL,$3,$4,'pending','',true,'platform',$5,$6,true,'Hosted in the platform subscription.')`,
		slug, name, region, plan, subscriptionID, rg)
	if err != nil {
		return model.Tenant{}, err
	}
	return s.TenantBySlug(ctx, slug)
}

// SetReconcilerPrincipal records the object id of a platform-hosted tenant's
// pre-created reconciler managed identity, so its /recon calls (whose token tid is
// the shared platform directory) resolve to this tenant by identity.
func (s *Store) SetReconcilerPrincipal(ctx context.Context, slug, principalID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE tenants SET reconciler_principal_id = $2 WHERE id = $1`, slug, principalID)
	return err
}

// TenantByReconcilerOID resolves a tenant by its reconciler's managed-identity
// object id (platform-hosted tenants, whose token tid is the shared platform
// directory and can't identify them).
func (s *Store) TenantByReconcilerOID(ctx context.Context, oid string) (model.Tenant, error) {
	if strings.TrimSpace(oid) == "" {
		return model.Tenant{}, ErrNotFound
	}
	t, err := scanTenant(s.pool.QueryRow(ctx,
		`SELECT `+tenantCols+` FROM tenants WHERE reconciler_principal_id = $1`, oid))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// isPlaceholderName reports whether a tenant name is only the default placeholder
// (or empty) — safe to replace once a real name is known.
func isPlaceholderName(n string) bool {
	n = strings.TrimSpace(n)
	return n == "" || n == "New tenant"
}

// SetTenantEnabled enables or disables a tenant's access (console/API sign-in +
// reconciler sync). Platform admins call this to approve or cut off a tenant.
func (s *Store) SetTenantEnabled(ctx context.Context, slug string, enabled bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE tenants SET enabled = $2 WHERE id = $1`, slug, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteTenant removes a tenant row and everything owned by it (agents, apps,
// memberships, … cascade). The Azure footprint is torn down separately.
func (s *Store) DeleteTenant(ctx context.Context, slug string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, slug)
	return err
}

// RenameTenant sets a tenant's display name (platform admins).
func (s *Store) RenameTenant(ctx context.Context, slug, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrNotFound
	}
	tag, err := s.pool.Exec(ctx, `UPDATE tenants SET name = $2 WHERE id = $1`, slug, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SearchUsers finds previously-signed-in users by name or email (for the members
// type-ahead). Empty query returns the most-recent sign-ins.
func (s *Store) SearchUsers(ctx context.Context, q string, limit int) ([]model.UserSummary, error) {
	q = strings.TrimSpace(q)
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	like := "%" + strings.ToLower(q) + "%"
	rows, err := s.pool.Query(ctx,
		`SELECT oid, coalesce(name,''), coalesce(email,'') FROM users
		 WHERE $1 = '' OR lower(name) LIKE $2 OR lower(email) LIKE $2
		 ORDER BY last_login DESC NULLS LAST LIMIT $3`, q, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.UserSummary{}
	for rows.Next() {
		var u model.UserSummary
		if err := rows.Scan(&u.OID, &u.Name, &u.Email); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func computeStats(tenants []model.Tenant) model.FleetStats {
	st := model.FleetStats{Tenants: len(tenants)}
	for _, t := range tenants {
		if t.Enrollment == "bound" {
			st.Bound++
		}
		st.Agents += t.AgentCount
		st.CallsMonth += t.MonthlyCalls
	}
	return st
}

/* ── Catalog ────────────────────────────────────────────────────────────── */

func (s *Store) CatalogList(ctx context.Context) ([]model.CatalogAgent, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT ca.id, ca.name, ca.description, coalesce(ca.type,'prompt'), ca.model, ca.owner_tenant, coalesce(t.name,''), ca.created_at
		 FROM catalog_agents ca LEFT JOIN tenants t ON t.id = ca.owner_tenant
		 ORDER BY ca.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	order := []string{}
	byID := map[string]*model.CatalogAgent{}
	for rows.Next() {
		var a model.CatalogAgent
		if err := rows.Scan(&a.ID, &a.Name, &a.Description, &a.Type, &a.Model, &a.Owner, &a.OwnerName, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.Platform = a.Owner == ""
		byID[a.ID] = &a
		order = append(order, a.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(order) == 0 {
		return []model.CatalogAgent{}, nil
	}

	// The current definition is the latest version's; catalog_versions is kept as
	// the internal definition store (versioning is not surfaced in the model).
	vrows, err := s.pool.Query(ctx,
		`SELECT agent_id, definition FROM catalog_versions ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer vrows.Close()
	for vrows.Next() {
		var agentID string
		var defRaw []byte
		if err := vrows.Scan(&agentID, &defRaw); err != nil {
			return nil, err
		}
		if a := byID[agentID]; a != nil {
			a.Definition = defFromRaw(defRaw) // ORDER BY created_at asc → latest wins
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

// CatalogForTenant returns the agents a tenant can use — the platform agents it
// is entitled to, plus the agents it owns — each flagged with ownership and
// whether it's already enabled.
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
		owned := a.Owner == slug
		entitled := a.Owner == "" && entitledSet[a.ID]
		if !owned && !entitled {
			continue // another tenant's private agent, or a platform agent not entitled
		}
		a.Platform = a.Owner == ""
		a.Owned = owned
		a.Entitled = entitled
		a.Enabled = enabledSet[a.ID]
		a.OwnerName = "" // don't leak owner display names into the tenant view
		out = append(out, a)
	}
	return out, nil
}

func (s *Store) CatalogAgentOwner(ctx context.Context, agentID string) (string, error) {
	var owner string
	err := s.pool.QueryRow(ctx, `SELECT owner_tenant FROM catalog_agents WHERE id = $1`, agentID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return owner, err
}

// DeleteCatalogAgent removes a catalog agent (and its versions, via cascade),
// un-entitles it everywhere, and removes any enabled instances. Deletion is not
// blocked by in-use — the dep tree is trusted; the admin is choosing to remove it.
func (s *Store) DeleteCatalogAgent(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM catalog_agents WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, _ = s.pool.Exec(ctx, `UPDATE tenants SET entitled_agents = array_remove(entitled_agents, $1)`, id)
	_, _ = s.pool.Exec(ctx, `DELETE FROM agents WHERE agent_id = $1`, id)
	return nil
}

// UpdateCatalogAgent edits an agent's name/description/model and overwrites its
// single definition (type is immutable). Versioning is not surfaced, so the edit
// replaces the current definition in place.
func (s *Store) UpdateCatalogAgent(ctx context.Context, id, name, description, agentModel string, def shared.AgentDefinition) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE catalog_agents SET name = $2, description = $3, model = $4 WHERE id = $1`,
		id, name, description, agentModel)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = s.pool.Exec(ctx, `UPDATE catalog_versions SET definition = $2 WHERE agent_id = $1`, id, defToText(def))
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
// platform stores it's entitled to, each flagged with ownership + whether it's
// enabled (reconciled) in the tenant and its per-tenant lifecycle health.
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
		`SELECT `+memoryStoreCols+`, (ts.store_id IS NOT NULL) AS enabled, coalesce(ts.health,'')
		 FROM memory_stores m
		 LEFT JOIN tenant_stores ts ON ts.store_id = m.id AND ts.tenant_slug = $1
		 WHERE m.owner_tenant = $1 OR (m.owner_tenant = '' AND m.id = ANY($2))
		 ORDER BY m.created_at DESC`, slug, entitled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.MemoryStore{}
	for rows.Next() {
		var m model.MemoryStore
		var enabled bool
		var health string
		if err := rows.Scan(append(memStoreScanDest(&m), &enabled, &health)...); err != nil {
			return nil, err
		}
		m.Platform = m.Owner == ""
		m.Owned = m.Owner == slug
		m.Entitled = entitledSet[m.ID]
		m.Enabled = enabled
		m.Health = health
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
	// Detach the store from tenant entitlements, enablements + agent connections.
	_, _ = s.pool.Exec(ctx, `UPDATE tenants SET entitled_stores = array_remove(entitled_stores, $1)`, id)
	_, _ = s.pool.Exec(ctx, `DELETE FROM tenant_stores WHERE store_id = $1`, id)
	_, _ = s.pool.Exec(ctx, `UPDATE agents SET memory_store = '' WHERE memory_store = $1`, id)
	return nil
}

func (s *Store) SetStoreEntitlements(ctx context.Context, slug string, storeIDs []string) error {
	return s.setEntitlements(ctx, slug, model.DepMemoryStore, storeIDs)
}

var ErrStoreNotAccessible = errors.New("memory store not accessible")
var ErrStoreInUse = errors.New("memory store is in use by an enabled agent")

// storeAccessible reports whether a tenant may use a store — it owns the store,
// or the store is a platform store the tenant is entitled to.
func (s *Store) storeAccessible(ctx context.Context, slug, storeID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM memory_stores m WHERE m.id = $2 AND
		   (m.owner_tenant = $1 OR (m.owner_tenant = '' AND m.id IN
		     (SELECT unnest(entitled_stores) FROM tenants WHERE id = $1))))`,
		slug, storeID).Scan(&ok)
	return ok, err
}

// ConnectAgentStore connects (storeID != "") or disconnects (storeID == "") an
// enabled agent to a memory store the tenant owns or is entitled to. Connecting
// auto-enables the store so the reconciler provisions it.
func (s *Store) ConnectAgentStore(ctx context.Context, slug, catalogAgentID, storeID string) error {
	if storeID != "" {
		ok, err := s.storeAccessible(ctx, slug, storeID)
		if err != nil {
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
	if storeID != "" {
		if err := s.autoEnableStores(ctx, slug, []string{storeID}); err != nil {
			return err
		}
	}
	return s.pruneAutoStores(ctx, slug)
}

// EnableStore activates a memory store (one the tenant owns or is entitled to) in
// a tenant, mirroring EnableAgent: it records desired state as a tenant_stores
// row the reconciler provisions into Foundry and reports back on. A manual enable
// clears the auto flag so it survives agent churn.
func (s *Store) EnableStore(ctx context.Context, slug, storeID string) error {
	ok, err := s.storeAccessible(ctx, slug, storeID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrStoreNotAccessible
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO tenant_stores (tenant_slug, store_id, health, auto, sort_order)
		 VALUES ($1,$2,'reconciling',false,
		         coalesce((SELECT max(sort_order)+1 FROM tenant_stores WHERE tenant_slug=$1),1))
		 ON CONFLICT (tenant_slug, store_id) DO UPDATE SET auto = false`,
		slug, storeID)
	return err
}

// DisableStore deactivates a store in a tenant. It refuses if an enabled agent
// still binds to the store (that would strand the agent's memory) — disconnect or
// disable those agents first.
func (s *Store) DisableStore(ctx context.Context, slug, storeID string) error {
	refs, err := s.storesReferencedByEnabledAgents(ctx, slug)
	if err != nil {
		return err
	}
	if refs[storeID] {
		return ErrStoreInUse
	}
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM tenant_stores WHERE tenant_slug = $1 AND store_id = $2`, slug, storeID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// autoEnableStores enables (as auto rows) each accessible store id, so a store an
// agent binds to is provisioned even if it was never explicitly enabled.
// Inaccessible ids are skipped.
func (s *Store) autoEnableStores(ctx context.Context, slug string, storeIDs []string) error {
	for _, id := range storeIDs {
		if id == "" {
			continue
		}
		ok, err := s.storeAccessible(ctx, slug, id)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO tenant_stores (tenant_slug, store_id, health, auto, sort_order)
			 VALUES ($1,$2,'reconciling',true,
			         coalesce((SELECT max(sort_order)+1 FROM tenant_stores WHERE tenant_slug=$1),1))
			 ON CONFLICT (tenant_slug, store_id) DO NOTHING`,
			slug, id); err != nil {
			return err
		}
	}
	return nil
}

// pruneAutoStores removes auto-enabled store rows no longer bound by any enabled
// agent — the symmetric cleanup when an agent is disabled or disconnected.
// Manually-enabled stores (auto = false) are left alone.
func (s *Store) pruneAutoStores(ctx context.Context, slug string) error {
	refs, err := s.storesReferencedByEnabledAgents(ctx, slug)
	if err != nil {
		return err
	}
	keep := make([]string, 0, len(refs))
	for id := range refs {
		keep = append(keep, id)
	}
	_, err = s.pool.Exec(ctx,
		`DELETE FROM tenant_stores WHERE tenant_slug = $1 AND auto = true AND store_id <> ALL($2)`,
		slug, keep)
	return err
}

// storesReferencedByEnabledAgents returns the set of store ids that enabled
// agents in the tenant effectively bind to (per-agent override, else the catalog
// definition's memoryStore).
func (s *Store) storesReferencedByEnabledAgents(ctx context.Context, slug string) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT coalesce(nullif(a.memory_store,''),
		          (SELECT v.definition->>'memoryStore' FROM catalog_versions v
		           WHERE v.agent_id = a.agent_id ORDER BY v.created_at DESC LIMIT 1))
		 FROM agents a WHERE a.tenant_slug = $1`, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id *string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		if id != nil && *id != "" {
			out[*id] = true
		}
	}
	return out, rows.Err()
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
		`SELECT `+tenantCols+`, entitled_agents, entitled_stores, entitled_deployments, entitled_infrastructure FROM tenants WHERE is_platform = false ORDER BY name`)
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
			&r.SubscriptionID, &r.ReconcilerIdentity, &r.FoundryProject, &r.ReconcilerVersion, &installedAt, &r.Enabled,
			&r.Cluster.Name, &r.Cluster.Phase, &r.Cluster.K8sVersion, &r.Cluster.ArgoInstalled, &r.Cluster.NodeCount, &r.Cluster.Detail,
			&r.Cluster.IngressInstalled, &r.Cluster.GatewayIP, &r.Cluster.IngressIssuer, &r.Cluster.InfraDelegated, &r.Cluster.InfraDetail, &r.Cluster.FootprintState, &r.Cluster.FootprintDetail,
			&r.HostingMode, &r.ResourceGroup, &r.ReconcilerPrincipalID,
			&r.EntitledAgents, &r.EntitledStores, &r.EntitledDeployments, &r.EntitledInfrastructure); err != nil {
			return nil, err
		}
		if installedAt != "" {
			r.InstalledAt = &installedAt
		}
		r.Lifecycle = deriveLifecycle(r.Enrollment, r.LastHeartbeat, r.Cluster.InfraDelegated, r.Cluster.FootprintState)
		if r.EntitledAgents == nil {
			r.EntitledAgents = []string{}
		}
		if r.EntitledStores == nil {
			r.EntitledStores = []string{}
		}
		if r.EntitledDeployments == nil {
			r.EntitledDeployments = []string{}
		}
		if r.EntitledInfrastructure == nil {
			r.EntitledInfrastructure = []string{}
		}
		r.EntitledCount = len(r.EntitledAgents)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetEntitlements sets a tenant's entitled agents, guarding removals (can't
// un-entitle an enabled or still-depended-on agent) and cascading to deps.
func (s *Store) SetEntitlements(ctx context.Context, slug string, agentIDs []string) error {
	return s.setEntitlements(ctx, slug, model.DepAgent, agentIDs)
}

/* ── Enable / disable / install (tenant desired state) ──────────────────── */

var ErrNotEntitled = errors.New("not entitled")

func (s *Store) EnableAgent(ctx context.Context, slug, catalogAgentID string, publishTo []string) error {
	var allowed bool
	if err := s.pool.QueryRow(ctx,
		`SELECT ($2 = ANY(entitled_agents))
		        OR EXISTS(SELECT 1 FROM catalog_agents WHERE id = $2 AND owner_tenant = $1)
		 FROM tenants WHERE id = $1`, slug, catalogAgentID).Scan(&allowed); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if !allowed {
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
		`INSERT INTO agents (id, tenant_slug, agent_id, name, version, channel, model, health, publish_to, calls_30d, auto, sort_order)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,'reconciling',$8,0,false,
		         coalesce((SELECT max(sort_order)+1 FROM agents WHERE tenant_slug=$2),1))
		 ON CONFLICT (id) DO UPDATE SET auto = false`,
		slug+":"+catalogAgentID, slug, catalogAgentID, name, version, channel, agentModel, publishTo); err != nil {
		return err
	}
	// Auto-enable the agent's dependencies (its memory store) — entitling +
	// enabling them so the reconciler provisions them alongside the agent.
	if err := s.autoEnableDeps(ctx, slug, model.DepAgent, catalogAgentID); err != nil {
		return err
	}
	return s.recountAgents(ctx, slug)
}

func (s *Store) DisableAgent(ctx context.Context, slug, catalogAgentID string) error {
	// Refuse while an enabled application still depends on this agent.
	deps, err := s.enabledDependents(ctx, slug, model.DepAgent, catalogAgentID)
	if err != nil {
		return err
	}
	if len(deps) > 0 {
		return ErrInUse
	}
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM agents WHERE tenant_slug = $1 AND agent_id = $2`, slug, catalogAgentID); err != nil {
		return err
	}
	return s.pruneAutoDeps(ctx, slug)
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
// reconciler. The tenant is resolved by the caller (by reconciler identity or
// directory id), so this works for both delegated and platform-hosted tenants.
func (s *Store) SyncDesired(ctx context.Context, t model.Tenant) (shared.DesiredState, error) {
	out := shared.DesiredState{TenantID: t.TenantID, Agents: []shared.DesiredAgent{}}
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
		}
		out.Agents = append(out.Agents, a)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	// Attach the typed definition of every store ENABLED in this tenant (explicit
	// or auto-enabled via an agent), so the reconciler provisions each as a
	// Foundry memory_store and binds the agents that reference it.
	srows, err := s.pool.Query(ctx,
		`SELECT m.id, m.name, m.chat_model, m.embedding_model, m.user_profile_enabled,
		        m.user_profile_details, m.chat_summary_enabled, m.procedural_memory_enabled, m.ttl_seconds
		 FROM memory_stores m JOIN tenant_stores ts ON ts.store_id = m.id AND ts.tenant_slug = $1
		 ORDER BY ts.sort_order`, t.ID)
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

	// Applications ENABLED in this tenant. Each app's Azure infrastructure is a
	// separate entity it DEPENDS on (provisioned by the control plane); its outputs
	// are merged into the Helm values here. An app is HELD (waiting, excluded from
	// desired state) until every dependency is satisfied: an infrastructure dep
	// must be enabled + 'ready', an application dep enabled + (transitively) ready,
	// an agent dep 'live'. App→app order is then enforced via Argo sync-waves.
	arows, err := s.pool.Query(ctx,
		`SELECT a.id, a.name, a.namespace, a.repo_url, a.chart, a.target_revision, a.values, a.expose_service, a.expose_port, a.wiring, a.dependencies
		 FROM applications a JOIN tenant_deployments td ON td.app_id = a.id AND td.tenant_slug = $1
		 ORDER BY td.sort_order`, t.ID)
	if err != nil {
		return out, err
	}
	defer arows.Close()
	type appInfo struct {
		da     shared.DesiredApplication
		wiring []shared.WireLink
		deps   []model.Dependency
	}
	apps := []appInfo{}
	enabledApp := map[string]bool{}
	for arows.Next() {
		var da shared.DesiredApplication
		var wraw, draw []byte
		if err := arows.Scan(&da.ID, &da.Name, &da.Namespace, &da.RepoURL, &da.Chart, &da.TargetRevision, &da.Values, &da.ExposeService, &da.ExposePort, &wraw, &draw); err != nil {
			return out, err
		}
		apps = append(apps, appInfo{da: da, wiring: wiringFromRaw(wraw), deps: depsFromRaw(draw)})
		enabledApp[da.ID] = true
	}
	if err := arows.Err(); err != nil {
		return out, err
	}

	// Enabled infrastructure state + resolved outputs in this tenant.
	infraState := map[string]string{}
	infraOutputs := map[string]map[string]any{}
	if irows, err := s.pool.Query(ctx,
		`SELECT infra_id, coalesce(infra_state,''), coalesce(infra_outputs,'{}') FROM tenant_infrastructure WHERE tenant_slug = $1`, t.ID); err == nil {
		for irows.Next() {
			var id, st string
			var oraw []byte
			if irows.Scan(&id, &st, &oraw) == nil {
				infraState[id] = st
				infraOutputs[id] = paramsFromRaw(oraw)
			}
		}
		irows.Close()
	}

	// Which agents are live in this tenant (an app→agent dep is met once live).
	liveAgents := map[string]bool{}
	if lrows, err := s.pool.Query(ctx, `SELECT agent_id FROM agents WHERE tenant_slug = $1 AND health = 'live'`, t.ID); err == nil {
		for lrows.Next() {
			var id string
			if lrows.Scan(&id) == nil {
				liveAgents[id] = true
			}
		}
		lrows.Close()
	}

	byID := make(map[string]appInfo, len(apps))
	for _, a := range apps {
		byID[a.da.ID] = a
	}
	memo := map[string]bool{}
	visiting := map[string]bool{}
	var ready func(id string) bool
	ready = func(id string) bool {
		if v, ok := memo[id]; ok {
			return v
		}
		if visiting[id] {
			return false // dependency cycle — unsatisfiable
		}
		visiting[id] = true
		ok := true
		for _, dep := range byID[id].deps {
			switch dep.Kind {
			case model.DepInfrastructure:
				if infraState[dep.ID] != "ready" {
					ok = false
				}
			case model.DepAgent:
				if !liveAgents[dep.ID] {
					ok = false
				}
			case model.DepApplication:
				if !enabledApp[dep.ID] || !ready(dep.ID) {
					ok = false
				}
			}
			if !ok {
				break
			}
		}
		visiting[id] = false
		memo[id] = ok
		return ok
	}
	// Wireable outputs of every dependency kind, keyed "<kind>:<id>": an
	// infrastructure entity's resolved Bicep outputs, plus derived values for a
	// dependency application (name / namespace / serviceHost) and agent (agentId /
	// name). A dependent app wires any of these into its Helm values.
	sources := map[string]map[string]any{}
	for id, outs := range infraOutputs {
		sources["infrastructure:"+id] = outs
	}
	for _, a := range apps {
		sources["application:"+a.da.ID] = map[string]any{
			"name":        a.da.Name,
			"namespace":   a.da.Namespace,
			"serviceHost": a.da.Name + "." + a.da.Namespace + ".svc.cluster.local",
		}
	}
	for _, ag := range out.Agents {
		sources["agent:"+ag.AgentID] = map[string]any{"agentId": ag.AgentID, "name": ag.Name}
	}

	for _, a := range apps {
		deployable := ready(a.da.ID)
		_, _ = s.pool.Exec(ctx, `UPDATE tenant_deployments SET waiting = $3 WHERE tenant_slug = $1 AND app_id = $2`,
			t.ID, a.da.ID, !deployable)
		if !deployable {
			continue
		}
		da := a.da
		da.Values = applyWiring(da.Values, a.wiring, sources)
		for _, dep := range a.deps { // only app→app edges gate cluster ordering
			if dep.Kind == model.DepApplication {
				da.DependsOn = append(da.DependsOn, dep.ID)
			}
		}
		out.Applications = append(out.Applications, da)
	}
	// Order the deployable apps so their app→app dependencies converge first.
	assignWaves(out.Applications)
	return out, nil
}

// InfraTarget is one enabled infrastructure entity (a resolved ARM template) for
// the control-plane infra worker to provision cross-tenant.
type InfraTarget struct {
	TenantSlug     string
	TenantID       string
	SubscriptionID string
	InfraID        string
	ArmTemplate    string
	State          string // current infra_state
	HostingMode    string // 'delegated' | 'platform'
	ResourceGroup  string // the tenant's footprint RG (platform-hosted); '' ⇒ config default
}

// InfraTargets returns every enabled infrastructure entity (across tenants) that
// has a resolved ARM template and a known subscription to provision it into.
func (s *Store) InfraTargets(ctx context.Context) ([]InfraTarget, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT t.id, coalesce(t.tenant_id,''), coalesce(t.subscription_id,''), i.id, i.arm_template, coalesce(ti.infra_state,''),
		        coalesce(t.hosting_mode,'delegated'), coalesce(t.resource_group,'')
		 FROM tenant_infrastructure ti
		 JOIN infrastructure i ON i.id = ti.infra_id
		 JOIN tenants t ON t.id = ti.tenant_slug
		 WHERE i.arm_template <> '' AND coalesce(t.subscription_id,'') <> '' AND ti.pending_delete = false`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InfraTarget
	for rows.Next() {
		var it InfraTarget
		if err := rows.Scan(&it.TenantSlug, &it.TenantID, &it.SubscriptionID, &it.InfraID, &it.ArmTemplate, &it.State,
			&it.HostingMode, &it.ResourceGroup); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// SetInfraState records the provisioning state + resolved outputs of an enabled
// infrastructure entity (control-plane worker → DB). SyncDesired merges the
// outputs into dependent apps' Helm values and holds an app until its infra is
// "ready".
func (s *Store) SetInfraState(ctx context.Context, tenantSlug, infraID, state string, outputs map[string]any) error {
	raw := []byte("{}")
	if len(outputs) > 0 {
		if b, err := json.Marshal(outputs); err == nil {
			raw = b
		}
	}
	health := shared.StatusReconciling
	switch state {
	case "ready":
		health = shared.StatusLive
	case "failed":
		health = shared.StatusBlocked
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE tenant_infrastructure SET infra_state = $3, infra_outputs = $4, health = $5 WHERE tenant_slug = $1 AND infra_id = $2`,
		tenantSlug, infraID, state, raw, health)
	return err
}

// RecordDelegatedTenant registers a subscription discovered via Lighthouse as a
// tenant (created disabled — a platform admin must enable it before its footprint
// is provisioned), recording its subscription. Returns the tenant slug.
func (s *Store) RecordDelegatedTenant(ctx context.Context, tid, name, subscriptionID string) (string, error) {
	t, err := s.EnsureTenantForTID(ctx, tid, name)
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx,
		`UPDATE tenants SET subscription_id = coalesce(nullif($2,''), subscription_id) WHERE id = $1`,
		t.ID, subscriptionID)
	return t.ID, err
}

// SetInfraDelegation records whether the control plane can reach the tenant's
// delegated subscription (control-plane worker → DB).
func (s *Store) SetInfraDelegation(ctx context.Context, slug string, delegated bool, detail string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE tenants SET infra_delegated = $2, infra_detail = $3 WHERE id = $1`, slug, delegated, detail)
	return err
}

// FootprintTarget is an enabled, delegated tenant whose footprint the control
// plane should provision.
type FootprintTarget struct {
	Slug                  string
	TenantID              string
	SubscriptionID        string
	Name                  string
	State                 string // current footprint_state
	Reprovision           bool   // platform admin asked to re-submit over a ready footprint
	HostingMode           string // 'delegated' | 'platform'
	ResourceGroup         string // per-tenant RG (platform-hosted); '' ⇒ use the config default
	Region                string // per-tenant region; '' ⇒ use the config default
	ReconcilerPrincipalID string // pre-created reconciler MI oid (platform-hosted)
}

// FootprintTargets returns enabled tenants whose footprint the control plane
// should (re)provision: those without a ready footprint yet, plus any a platform
// admin flagged for re-provisioning. Covers both delegated tenants (customer
// subscription via Lighthouse) and platform-hosted ones (platform subscription).
func (s *Store) FootprintTargets(ctx context.Context) ([]FootprintTarget, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, coalesce(tenant_id,''), coalesce(subscription_id,''), name, coalesce(footprint_state,''), coalesce(footprint_reprovision,false),
		        coalesce(hosting_mode,'delegated'), coalesce(resource_group,''), region, coalesce(reconciler_principal_id,'')
		 FROM tenants
		 WHERE enabled = true AND coalesce(subscription_id,'') <> ''
		   AND (coalesce(footprint_state,'') <> 'ready' OR footprint_reprovision = true)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FootprintTarget
	for rows.Next() {
		var t FootprintTarget
		if err := rows.Scan(&t.Slug, &t.TenantID, &t.SubscriptionID, &t.Name, &t.State, &t.Reprovision,
			&t.HostingMode, &t.ResourceGroup, &t.Region, &t.ReconcilerPrincipalID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SetFootprintState records the provisioning state of a tenant's footprint.
func (s *Store) SetFootprintState(ctx context.Context, slug, state, detail string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE tenants SET footprint_state = $2, footprint_detail = $3 WHERE id = $1`, slug, state, detail)
	return err
}

// RequestFootprintReprovision flags a delegated tenant for a one-shot footprint
// re-submit, so footprint template changes reach an already-provisioned tenant.
// The next provisioner sweep re-PUTs the (idempotent) template and clears the
// flag. Errors ErrNotFound when the tenant is missing or not delegated (no sub).
func (s *Store) RequestFootprintReprovision(ctx context.Context, slug string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE tenants SET footprint_reprovision = true, footprint_state = 'provisioning', footprint_detail = 'Re-provision requested.'
		 WHERE id = $1 AND coalesce(subscription_id,'') <> ''`, slug)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClearFootprintReprovision consumes the one-shot reprovision flag (called by the
// provisioner once it has re-submitted, so it fires exactly once per request).
func (s *Store) ClearFootprintReprovision(ctx context.Context, slug string) error {
	_, err := s.pool.Exec(ctx, `UPDATE tenants SET footprint_reprovision = false WHERE id = $1`, slug)
	return err
}

// applyWiring merges a dependency's outputs into an application's Helm values at
// the wired paths (types preserved; malformed values left untouched). sources is
// keyed "<sourceKind>:<sourceId>" → that dependency's output map.
func applyWiring(values string, wiring []shared.WireLink, sources map[string]map[string]any) string {
	if len(wiring) == 0 || len(sources) == 0 {
		return values
	}
	m := map[string]any{}
	if strings.TrimSpace(values) != "" {
		if err := yaml.Unmarshal([]byte(values), &m); err != nil {
			return values
		}
	}
	changed := false
	for _, w := range wiring {
		outs := sources[w.SourceKind+":"+w.SourceID]
		if outs == nil {
			continue
		}
		v, ok := outs[w.Output]
		if !ok || strings.TrimSpace(w.HelmPath) == "" {
			continue
		}
		setNested(m, strings.Split(w.HelmPath, "."), v)
		changed = true
	}
	if !changed {
		return values
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		return values
	}
	return string(out)
}

// setNested sets m[a][b][c] = value, creating intermediate maps as needed.
func setNested(m map[string]any, path []string, value any) {
	for i := 0; i < len(path)-1; i++ {
		next, ok := m[path[i]].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[path[i]] = next
		}
		m = next
	}
	if len(path) > 0 {
		m[path[len(path)-1]] = value
	}
}

// ApplyHeartbeat records a reconciler heartbeat: it upserts the tenant with the
// authoritative in-tenant install details (name, region, subscription, reconciler
// identity, Foundry project) and updates each managed agent's actual health.
func (s *Store) ApplyHeartbeat(ctx context.Context, t model.Tenant, hb shared.Heartbeat) error {
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
	// Record each memory store's reconcile lifecycle (reconciling → live →
	// blocked). Only rows still enabled are updated; a store disabled between
	// sync and heartbeat is a harmless no-op.
	for _, ms := range hb.MemoryStores {
		if _, err := s.pool.Exec(ctx,
			`UPDATE tenant_stores SET health = $3 WHERE tenant_slug = $1 AND store_id = $2`,
			t.ID, ms.StoreID, ms.Health); err != nil {
			return err
		}
	}
	// Record the tenant's cluster/GitOps status.
	if c := hb.Cluster; c != nil {
		if _, err := s.pool.Exec(ctx,
			`UPDATE tenants SET cluster_name = $2, cluster_phase = $3, cluster_k8s_version = $4,
			   cluster_argo_installed = $5, cluster_node_count = $6, cluster_detail = $7,
			   cluster_ingress_installed = $8, cluster_gateway_ip = $9, cluster_ingress_issuer = $10
			 WHERE id = $1`,
			t.ID, c.Name, c.Phase, c.KubernetesVer, c.ArgoInstalled, c.NodeCount, c.Detail,
			c.IngressInstalled, c.GatewayIP, c.IngressIssuer); err != nil {
			return err
		}
	}
	// Record each Argo Application's per-tenant sync/health + derived lifecycle +
	// Argo sync/health only — infra_state is owned by the control-plane infra
	// worker now, not the reconciler (a deployment removed between sync and
	// heartbeat is a harmless no-op).
	for _, a := range hb.Applications {
		if _, err := s.pool.Exec(ctx,
			`UPDATE tenant_deployments SET sync_status = $3, health_status = $4, health = $5
			 WHERE tenant_slug = $1 AND app_id = $2`,
			t.ID, a.ID, a.SyncStatus, a.HealthStatus, deriveDeploymentHealth(a.SyncStatus, a.HealthStatus)); err != nil {
			return err
		}
	}
	return s.recountAgents(ctx, t.ID)
}

/* ── Deployments (catalog entities → per-tenant Argo CD Applications) ─────── */

// ErrDeploymentNotAccessible is returned when a tenant tries to enable a
// deployment it neither owns nor is entitled to.
var ErrDeploymentNotAccessible = errors.New("deployment not accessible to tenant")

const applicationCols = `a.id, a.name, a.description, a.owner_tenant, a.namespace,
	a.repo_url, a.chart, a.target_revision, a.values, a.expose_service, a.expose_port, a.wiring, a.dependencies, a.created_by, a.created_at`

// appScanDest scans the fixed columns; wiring + dependencies (jsonb) are captured
// raw and unmarshalled by the caller (wiringFromRaw / depsFromRaw).
func appScanDest(a *model.Application, wiringRaw, depsRaw *[]byte) []any {
	return []any{&a.ID, &a.Name, &a.Description, &a.Owner, &a.Namespace,
		&a.RepoURL, &a.Chart, &a.TargetRevision, &a.Values, &a.ExposeService, &a.ExposePort, wiringRaw, depsRaw, &a.CreatedBy, &a.CreatedAt}
}

func paramsFromRaw(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	m := map[string]any{}
	if json.Unmarshal(raw, &m) != nil || len(m) == 0 {
		return nil
	}
	return m
}

func paramsJSON(p map[string]any) []byte {
	if len(p) == 0 {
		return []byte("{}")
	}
	b, err := json.Marshal(p)
	if err != nil {
		return []byte("{}")
	}
	return b
}

func wiringFromRaw(raw []byte) []shared.WireLink {
	out := []shared.WireLink{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func wiringJSON(w []shared.WireLink) []byte {
	if len(w) == 0 {
		return []byte("[]")
	}
	b, err := json.Marshal(w)
	if err != nil {
		return []byte("[]")
	}
	return b
}

func depsArray(d []string) []string {
	if d == nil {
		return []string{}
	}
	return d
}

// assignWaves sets each application's Wave to 1 + the max Wave of its enabled
// dependencies (app → app edges only), so Argo sync-waves converge dependencies
// first. Dependencies that aren't enabled applications here (e.g. agents, which
// provision into Foundry in parallel) don't gate cluster ordering. Cycles are
// broken defensively so a bad graph can't hang the sync.
func assignWaves(apps []shared.DesiredApplication) {
	idx := make(map[string]int, len(apps))
	for i, a := range apps {
		idx[a.ID] = i
	}
	wave := make([]int, len(apps))
	state := make([]int, len(apps)) // 0=unvisited, 1=in-progress, 2=done
	var visit func(i int) int
	visit = func(i int) int {
		switch state[i] {
		case 2:
			return wave[i]
		case 1:
			return 0 // cycle back-edge — don't count it
		}
		state[i] = 1
		w := 0
		for _, dep := range apps[i].DependsOn {
			if j, ok := idx[dep]; ok {
				if dw := visit(j) + 1; dw > w {
					w = dw
				}
			}
		}
		wave[i], state[i] = w, 2
		return w
	}
	for i := range apps {
		apps[i].Wave = visit(i)
	}
}

// deriveDeploymentHealth maps Argo's sync/health vocabulary onto the shared
// reconciling → live → blocked lifecycle that agents and memory stores use.
func deriveDeploymentHealth(sync, health string) string {
	switch {
	case strings.EqualFold(health, "Degraded") || strings.EqualFold(health, "Missing") || strings.EqualFold(sync, "Unknown"):
		return shared.StatusBlocked
	case strings.EqualFold(sync, "Synced") && strings.EqualFold(health, "Healthy"):
		return shared.StatusLive
	default:
		return shared.StatusReconciling
	}
}

// ApplicationList is the platform view: every deployment definition + owner name.
func (s *Store) ApplicationList(ctx context.Context) ([]model.Application, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+applicationCols+`, coalesce(t.name,'')
		 FROM applications a LEFT JOIN tenants t ON t.id = a.owner_tenant
		 ORDER BY a.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Application{}
	for rows.Next() {
		var a model.Application
		var wraw, draw []byte
		if err := rows.Scan(append(appScanDest(&a, &wraw, &draw), &a.OwnerName)...); err != nil {
			return nil, err
		}
		a.Wiring = wiringFromRaw(wraw)
		a.Dependencies = depsFromRaw(draw)
		a.Platform = a.Owner == ""
		out = append(out, a)
	}
	return out, rows.Err()
}

// ApplicationsForTenant returns the deployments visible to a tenant — the ones it
// owns plus the platform ones it's entitled to — each with its per-tenant enable
// state + runtime status (mirrors MemoryStoresForTenant).
func (s *Store) ApplicationsForTenant(ctx context.Context, slug string) ([]model.Application, error) {
	var entitled []string
	if err := s.pool.QueryRow(ctx, `SELECT entitled_deployments FROM tenants WHERE id = $1`, slug).Scan(&entitled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []model.Application{}, nil
		}
		return nil, err
	}
	entitledSet := map[string]bool{}
	for _, id := range entitled {
		entitledSet[id] = true
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+applicationCols+`,
		        (td.app_id IS NOT NULL) AS enabled, coalesce(td.sync_status,''), coalesce(td.health_status,''),
		        coalesce(td.waiting,false)
		 FROM applications a
		 LEFT JOIN tenant_deployments td ON td.app_id = a.id AND td.tenant_slug = $1
		 WHERE a.owner_tenant = $1 OR (a.owner_tenant = '' AND a.id = ANY($2))
		 ORDER BY a.created_at DESC`, slug, entitled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Application{}
	for rows.Next() {
		var a model.Application
		var wraw, draw []byte
		var enabled bool
		var sync, health string
		var waiting bool
		if err := rows.Scan(append(appScanDest(&a, &wraw, &draw), &enabled, &sync, &health, &waiting)...); err != nil {
			return nil, err
		}
		a.Wiring = wiringFromRaw(wraw)
		a.Dependencies = depsFromRaw(draw)
		a.Platform = a.Owner == ""
		a.Owned = a.Owner == slug
		a.Entitled = entitledSet[a.ID]
		a.Enabled = enabled
		if enabled {
			a.SyncStatus, a.HealthStatus = sync, health
			a.Health = deriveDeploymentHealth(sync, health)
			a.Waiting = waiting
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ApplicationByID(ctx context.Context, id string) (model.Application, error) {
	var a model.Application
	var wraw, draw []byte
	err := s.pool.QueryRow(ctx, `SELECT `+applicationCols+` FROM applications a WHERE a.id = $1`, id).Scan(appScanDest(&a, &wraw, &draw)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	a.Wiring = wiringFromRaw(wraw)
	a.Dependencies = depsFromRaw(draw)
	a.Platform = a.Owner == ""
	return a, err
}

// UpdateApplication updates the full Helm deployment definition (chart, values,
// wiring, dependencies). Argo re-syncs on the next reconcile.
func (s *Store) UpdateApplication(ctx context.Context, a model.Application) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE applications SET name = $2, description = $3, namespace = $4, repo_url = $5,
		   chart = $6, target_revision = $7, values = $8, expose_service = $9, expose_port = $10,
		   wiring = $11, dependencies = $12
		 WHERE id = $1`,
		a.ID, a.Name, a.Description, a.Namespace, a.RepoURL, a.Chart, a.TargetRevision, a.Values,
		a.ExposeService, a.ExposePort, wiringJSON(a.Wiring), depsJSON(a.Dependencies))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteApplication removes a deployment definition and detaches it from tenant
// entitlements + enablements.
func (s *Store) DeleteApplication(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM applications WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, _ = s.pool.Exec(ctx, `UPDATE tenants SET entitled_deployments = array_remove(entitled_deployments, $1)`, id)
	_, _ = s.pool.Exec(ctx, `DELETE FROM tenant_deployments WHERE app_id = $1`, id)
	return nil
}

func (s *Store) SetDeploymentEntitlements(ctx context.Context, slug string, appIDs []string) error {
	return s.setEntitlements(ctx, slug, model.DepApplication, appIDs)
}

func (s *Store) deploymentAccessible(ctx context.Context, slug, appID string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM applications a WHERE a.id = $2 AND
		   (a.owner_tenant = $1 OR (a.owner_tenant = '' AND a.id IN
		     (SELECT unnest(entitled_deployments) FROM tenants WHERE id = $1))))`,
		slug, appID).Scan(&ok)
	return ok, err
}

// EnableDeployment marks an application enabled for a tenant (one it owns or is
// entitled to), then auto-enables every dependency it needs (infrastructure,
// applications, agents) so the whole graph is installed together.
func (s *Store) EnableDeployment(ctx context.Context, slug, appID string) error {
	ok, err := s.deploymentAccessible(ctx, slug, appID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrDeploymentNotAccessible
	}
	if _, err = s.pool.Exec(ctx,
		`INSERT INTO tenant_deployments (tenant_slug, app_id, health, auto, sort_order)
		 VALUES ($1,$2,'reconciling',false,
		         coalesce((SELECT max(sort_order)+1 FROM tenant_deployments WHERE tenant_slug=$1),1))
		 ON CONFLICT (tenant_slug, app_id) DO UPDATE SET auto = false`,
		slug, appID); err != nil {
		return err
	}
	return s.autoEnableDeps(ctx, slug, model.DepApplication, appID)
}

// DisableDeployment deactivates an application in a tenant. It refuses while an
// enabled application still depends on it, then prunes any now-orphaned auto deps.
func (s *Store) DisableDeployment(ctx context.Context, slug, appID string) error {
	deps, err := s.enabledDependents(ctx, slug, model.DepApplication, appID)
	if err != nil {
		return err
	}
	if len(deps) > 0 {
		return ErrInUse
	}
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM tenant_deployments WHERE tenant_slug = $1 AND app_id = $2`, slug, appID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return s.pruneAutoDeps(ctx, slug)
}

/* ── Infrastructure (Bicep/Azure catalog entities → control-plane provisioned) ── */

// ErrInfrastructureNotAccessible is returned when a tenant tries to enable
// infrastructure it neither owns nor is entitled to.
var ErrInfrastructureNotAccessible = errors.New("infrastructure not accessible to tenant")

const infraCols = `i.id, i.name, i.description, i.owner_tenant, i.bicep, i.bicep_params, i.bicep_outputs, i.dependencies, i.created_by, i.created_at, i.pending_delete`

func infraScanDest(i *model.Infrastructure, paramsRaw, depsRaw *[]byte) []any {
	return []any{&i.ID, &i.Name, &i.Description, &i.Owner, &i.BicepModule, paramsRaw, &i.BicepOutputs, depsRaw, &i.CreatedBy, &i.CreatedAt, &i.PendingDelete}
}

// InfrastructureList is the platform view: every infrastructure definition + owner.
func (s *Store) InfrastructureList(ctx context.Context) ([]model.Infrastructure, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+infraCols+`, coalesce(t.name,'')
		 FROM infrastructure i LEFT JOIN tenants t ON t.id = i.owner_tenant
		 ORDER BY i.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Infrastructure{}
	for rows.Next() {
		var i model.Infrastructure
		var praw, draw []byte
		if err := rows.Scan(append(infraScanDest(&i, &praw, &draw), &i.OwnerName)...); err != nil {
			return nil, err
		}
		i.BicepParams = paramsFromRaw(praw)
		i.Dependencies = depsFromRaw(draw)
		i.Platform = i.Owner == ""
		out = append(out, i)
	}
	return out, rows.Err()
}

// InfrastructureForTenant returns the infrastructure a tenant can use — its own
// plus the platform ones it's entitled to — each with per-tenant enable state +
// runtime status.
func (s *Store) InfrastructureForTenant(ctx context.Context, slug string) ([]model.Infrastructure, error) {
	var entitled []string
	if err := s.pool.QueryRow(ctx, `SELECT entitled_infrastructure FROM tenants WHERE id = $1`, slug).Scan(&entitled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []model.Infrastructure{}, nil
		}
		return nil, err
	}
	entitledSet := map[string]bool{}
	for _, id := range entitled {
		entitledSet[id] = true
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+infraCols+`,
		        (ti.infra_id IS NOT NULL) AS enabled, coalesce(ti.infra_state,''), coalesce(ti.health,''), coalesce(ti.auto,false)
		 FROM infrastructure i
		 LEFT JOIN tenant_infrastructure ti ON ti.infra_id = i.id AND ti.tenant_slug = $1
		 WHERE (i.owner_tenant = $1 OR (i.owner_tenant = '' AND i.id = ANY($2)))
		   AND (ti.infra_id IS NOT NULL OR i.pending_delete = false)
		 ORDER BY i.created_at DESC`, slug, entitled)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Infrastructure{}
	for rows.Next() {
		var i model.Infrastructure
		var praw, draw []byte
		var enabled, auto bool
		var infraState, health string
		if err := rows.Scan(append(infraScanDest(&i, &praw, &draw), &enabled, &infraState, &health, &auto)...); err != nil {
			return nil, err
		}
		i.BicepParams = paramsFromRaw(praw)
		i.Dependencies = depsFromRaw(draw)
		i.Platform = i.Owner == ""
		i.Owned = i.Owner == slug
		i.Entitled = entitledSet[i.ID]
		i.Enabled = enabled
		if enabled {
			i.InfraState = infraState
			i.Health = health
			i.Waiting = infraState != "ready" && infraState != "failed" && infraState != "deprovisioning"
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *Store) InfrastructureByID(ctx context.Context, id string) (model.Infrastructure, error) {
	var i model.Infrastructure
	var praw, draw []byte
	err := s.pool.QueryRow(ctx, `SELECT `+infraCols+` FROM infrastructure i WHERE i.id = $1`, id).Scan(infraScanDest(&i, &praw, &draw)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return i, ErrNotFound
	}
	i.BicepParams = paramsFromRaw(praw)
	i.Dependencies = depsFromRaw(draw)
	i.Platform = i.Owner == ""
	return i, err
}

func (s *Store) UpdateInfrastructure(ctx context.Context, i model.Infrastructure) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE infrastructure SET name = $2, description = $3, bicep = $4, arm_template = $5,
		   bicep_params = $6, bicep_outputs = $7, dependencies = $8
		 WHERE id = $1`,
		i.ID, i.Name, i.Description, i.BicepModule, i.ArmTemplate, paramsJSON(i.BicepParams), depsArray(i.BicepOutputs), depsJSON(i.Dependencies))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteInfrastructure(ctx context.Context, id string) error {
	// Mark the definition as being deleted (shown "Deleting"); it is removed once
	// its last provisioned instance is torn down.
	tag, err := s.pool.Exec(ctx, `UPDATE infrastructure SET pending_delete = true WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// Stop offering it — detach from every tenant's entitlements.
	_, _ = s.pool.Exec(ctx, `UPDATE tenants SET entitled_infrastructure = array_remove(entitled_infrastructure, $1)`, id)
	// Provisioned instances → mark for teardown (kept as "Deprovisioning").
	if _, err := s.pool.Exec(ctx,
		`UPDATE tenant_infrastructure ti SET pending_delete = true, infra_state = 'deprovisioning', health = 'reconciling', auto = false
		 FROM tenants t WHERE ti.tenant_slug = t.id AND ti.infra_id = $1
		   AND ti.infra_state <> '' AND coalesce(t.subscription_id, '') <> ''`, id); err != nil {
		return err
	}
	// Never-provisioned instances → drop immediately (nothing in Azure).
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM tenant_infrastructure ti USING tenants t
		 WHERE ti.tenant_slug = t.id AND ti.infra_id = $1
		   AND (ti.infra_state = '' OR coalesce(t.subscription_id, '') = '')`, id); err != nil {
		return err
	}
	// Nothing left to tear down anywhere → remove the definition now.
	_, err = s.pool.Exec(ctx,
		`DELETE FROM infrastructure WHERE id = $1 AND pending_delete = true
		   AND NOT EXISTS (SELECT 1 FROM tenant_infrastructure WHERE infra_id = $1)`, id)
	return err
}

// InfrastructureOwner returns an infrastructure entity's owner ("" = platform).
func (s *Store) InfrastructureOwner(ctx context.Context, id string) (string, error) {
	var owner string
	err := s.pool.QueryRow(ctx, `SELECT owner_tenant FROM infrastructure WHERE id = $1`, id).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return owner, err
}

func (s *Store) SetInfrastructureEntitlements(ctx context.Context, slug string, ids []string) error {
	return s.setEntitlements(ctx, slug, model.DepInfrastructure, ids)
}

func (s *Store) infrastructureAccessible(ctx context.Context, slug, id string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM infrastructure i WHERE i.id = $2 AND i.pending_delete = false AND
		   (i.owner_tenant = $1 OR (i.owner_tenant = '' AND i.id IN
		     (SELECT unnest(entitled_infrastructure) FROM tenants WHERE id = $1))))`,
		slug, id).Scan(&ok)
	return ok, err
}

// EnableInfrastructure marks infrastructure enabled for a tenant (one it owns or
// is entitled to), then auto-enables the infrastructure it depends on. The
// control-plane infra worker provisions it cross-tenant.
func (s *Store) EnableInfrastructure(ctx context.Context, slug, id string) error {
	ok, err := s.infrastructureAccessible(ctx, slug, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInfrastructureNotAccessible
	}
	if _, err = s.pool.Exec(ctx,
		`INSERT INTO tenant_infrastructure (tenant_slug, infra_id, health, auto, sort_order)
		 VALUES ($1,$2,'reconciling',false,
		         coalesce((SELECT max(sort_order)+1 FROM tenant_infrastructure WHERE tenant_slug=$1),1))
		 ON CONFLICT (tenant_slug, infra_id) DO UPDATE SET
		   auto = false,
		   pending_delete = false,
		   infra_state = CASE WHEN tenant_infrastructure.pending_delete THEN '' ELSE tenant_infrastructure.infra_state END,
		   health = CASE WHEN tenant_infrastructure.pending_delete THEN 'reconciling' ELSE tenant_infrastructure.health END`,
		slug, id); err != nil {
		return err
	}
	return s.autoEnableDeps(ctx, slug, model.DepInfrastructure, id)
}

// DisableInfrastructure deactivates infrastructure in a tenant. It refuses while
// an enabled application or infrastructure still depends on it.
func (s *Store) DisableInfrastructure(ctx context.Context, slug, id string) error {
	deps, err := s.enabledDependents(ctx, slug, model.DepInfrastructure, id)
	if err != nil {
		return err
	}
	if len(deps) > 0 {
		return ErrInUse
	}
	// A provisioned instance (has a deployment + subscription) is marked for
	// teardown and stays visible as "Deprovisioning" until the provisioner removes
	// its Azure resources; anything never provisioned is dropped immediately.
	var infraState, sub string
	err = s.pool.QueryRow(ctx,
		`SELECT ti.infra_state, coalesce(t.subscription_id, '')
		 FROM tenant_infrastructure ti JOIN tenants t ON t.id = ti.tenant_slug
		 WHERE ti.tenant_slug = $1 AND ti.infra_id = $2`, slug, id).Scan(&infraState, &sub)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if infraState != "" && sub != "" {
		if _, err := s.pool.Exec(ctx,
			`UPDATE tenant_infrastructure SET pending_delete = true, infra_state = 'deprovisioning', health = 'reconciling', auto = false
			 WHERE tenant_slug = $1 AND infra_id = $2`, slug, id); err != nil {
			return err
		}
	} else if _, err := s.pool.Exec(ctx,
		`DELETE FROM tenant_infrastructure WHERE tenant_slug = $1 AND infra_id = $2`, slug, id); err != nil {
		return err
	}
	return s.pruneAutoDeps(ctx, slug)
}
