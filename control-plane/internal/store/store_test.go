package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/inception42/cortex/control-plane/internal/model"
	"github.com/inception42/cortex/shared"
)

func testStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://bensinclair@localhost:5432/cortex?sslmode=disable"
	}
	ctx := context.Background()
	st, err := New(ctx, dsn)
	if err != nil {
		t.Skipf("no database available: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st, ctx
}

func TestCatalogEntitleEnableLoop(t *testing.T) {
	st, ctx := testStore(t)
	defer st.Close()

	const (
		agentID = "zz-test-agent"
		slug    = "zz-test-tenant"
		tid     = "zz-test-tid-0001"
	)
	cleanup := func() {
		st.pool.Exec(ctx, `DELETE FROM catalog_agents WHERE id = $1`, agentID)
		st.pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, slug)
	}
	cleanup()
	defer cleanup()

	// 1. Author a catalog agent (with its definition).
	if err := st.CreateCatalogAgent(ctx, agentID, "ZZ Test Agent", "desc", "prompt", "gpt-4o", "", "oid-test", shared.AgentDefinition{Instructions: "v1"}); err != nil {
		t.Fatalf("create catalog agent: %v", err)
	}
	cat, err := st.CatalogList(ctx)
	if err != nil {
		t.Fatalf("catalog list: %v", err)
	}
	found := false
	for _, a := range cat {
		if a.ID == agentID {
			found = true
			if a.Definition.Instructions != "v1" {
				t.Fatalf("expected definition v1, got %+v", a.Definition)
			}
		}
	}
	if !found {
		t.Fatalf("catalog agent %s not found in list", agentID)
	}

	// 2. A tenant exists.
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, tenant_id, enrollment) VALUES ($1,'ZZ Tenant',$2,'bound')`, slug, tid); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	// 3. Enabling before entitlement is rejected.
	if err := st.EnableAgent(ctx, slug, agentID, []string{"api"}); err != ErrNotEntitled {
		t.Fatalf("expected ErrNotEntitled, got %v", err)
	}

	// 4. Entitle, then enable.
	if err := st.SetEntitlements(ctx, slug, []string{agentID}); err != nil {
		t.Fatalf("set entitlements: %v", err)
	}
	if err := st.EnableAgent(ctx, slug, agentID, []string{"api", "teams"}); err != nil {
		t.Fatalf("enable: %v", err)
	}

	agents, err := st.Agents(ctx, slug)
	if err != nil || len(agents) != 1 {
		t.Fatalf("expected 1 enabled agent, got %d (err %v)", len(agents), err)
	}
	if agents[0].Name != "ZZ Test Agent" || agents[0].Definition.Instructions != "v1" {
		t.Fatalf("unexpected enabled agent: %+v", agents[0])
	}

	// 5. Tenant catalog view flags entitled + enabled; registry counts match.
	tcat, err := st.CatalogForTenant(ctx, slug)
	if err != nil || len(tcat) != 1 || !tcat[0].Entitled || !tcat[0].Enabled {
		t.Fatalf("tenant catalog wrong: %+v (err %v)", tcat, err)
	}
	reg, err := st.TenantsRegistry(ctx)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	for _, r := range reg {
		if r.ID == slug {
			if r.AgentCount != 1 || r.EntitledCount != 1 {
				t.Fatalf("registry counts wrong: agents=%d entitled=%d", r.AgentCount, r.EntitledCount)
			}
		}
	}

	// 6. Disable removes it and re-counts.
	if err := st.DisableAgent(ctx, slug, agentID); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if agents, _ := st.Agents(ctx, slug); len(agents) != 0 {
		t.Fatalf("expected 0 agents after disable, got %d", len(agents))
	}
}

func TestMemoryStoreLifecycle(t *testing.T) {
	st, ctx := testStore(t)
	defer st.Close()

	const (
		storeID = "zz-ms-platform"
		agentID = "zz-ms-agent"
		slug    = "zz-ms-tenant"
		tid     = "zz-ms-tid-0001"
	)
	cleanup := func() {
		st.pool.Exec(ctx, `DELETE FROM catalog_agents WHERE id = $1`, agentID)
		st.pool.Exec(ctx, `DELETE FROM memory_stores WHERE id = $1 OR owner_tenant = $2`, storeID, slug)
		st.pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, slug)
	}
	cleanup()
	defer cleanup()

	// 1. Platform authors a memory store; a catalog agent references it.
	if err := st.CreateMemoryStore(ctx, storeID, "ZZ Store", "platform memory", "",
		shared.MemoryStoreDefinition{ChatModel: "gpt-4o", EmbeddingModel: "text-embedding-3-small", UserProfileEnabled: true, ChatSummaryEnabled: true}, "oid-test"); err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := st.CreateCatalogAgent(ctx, agentID, "ZZ MS Agent", "d", "prompt", "gpt-4o", "", "oid-test",
		shared.AgentDefinition{Instructions: "v1", MemoryStore: storeID}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, tenant_id, enrollment) VALUES ($1,'ZZ MS Tenant',$2,'bound')`, slug, tid); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	// 2. Entitling the agent auto-entitles the store it references.
	if err := st.SetEntitlements(ctx, slug, []string{agentID}); err != nil {
		t.Fatalf("entitle: %v", err)
	}
	stores, err := st.MemoryStoresForTenant(ctx, slug)
	if err != nil {
		t.Fatalf("stores for tenant: %v", err)
	}
	entitled := false
	for _, s := range stores {
		if s.ID == storeID {
			entitled = s.Entitled
		}
	}
	if !entitled {
		t.Fatalf("expected store auto-entitled after entitling its agent; got %+v", stores)
	}

	// 3. Enable the agent; SyncDesired carries the effective store + its definition.
	if err := st.EnableAgent(ctx, slug, agentID, nil); err != nil {
		t.Fatalf("enable: %v", err)
	}
	ds, err := st.SyncDesired(ctx, tid)
	if err != nil || len(ds.Agents) != 1 {
		t.Fatalf("sync: %d agents (err %v)", len(ds.Agents), err)
	}
	if ds.Agents[0].Definition.MemoryStore != storeID {
		t.Fatalf("desired agent store = %q, want %q", ds.Agents[0].Definition.MemoryStore, storeID)
	}
	if len(ds.MemoryStores) != 1 || ds.MemoryStores[0].ID != storeID ||
		ds.MemoryStores[0].Definition.ChatModel != "gpt-4o" || ds.MemoryStores[0].Definition.EmbeddingModel != "text-embedding-3-small" {
		t.Fatalf("desired memory stores wrong: %+v", ds.MemoryStores)
	}

	// 4. Tenant creates their own store and connects the agent to it (override).
	tenantStore := slug + "-notes"
	if err := st.CreateMemoryStore(ctx, tenantStore, "Notes", "tenant memory", slug,
		shared.MemoryStoreDefinition{ChatModel: "gpt-4o", EmbeddingModel: "text-embedding-3-small", UserProfileEnabled: true}, "oid-tenant"); err != nil {
		t.Fatalf("create tenant store: %v", err)
	}
	if err := st.ConnectAgentStore(ctx, slug, agentID, tenantStore); err != nil {
		t.Fatalf("connect: %v", err)
	}
	agents, err := st.Agents(ctx, slug)
	if err != nil || len(agents) != 1 || agents[0].MemoryStore != tenantStore {
		t.Fatalf("effective store after connect wrong: %+v (err %v)", agents, err)
	}
	if ds2, _ := st.SyncDesired(ctx, tid); len(ds2.Agents) != 1 || ds2.Agents[0].Definition.MemoryStore != tenantStore {
		t.Fatalf("sync override store wrong: %+v", ds2.Agents)
	}

	// 5. Connecting to an inaccessible store is rejected.
	if err := st.ConnectAgentStore(ctx, slug, agentID, "nonexistent-store"); err != ErrStoreNotAccessible {
		t.Fatalf("expected ErrStoreNotAccessible, got %v", err)
	}
}

// A store gets a per-tenant lifecycle: explicitly enabled → in desired state +
// tenant view (reconciling), heartbeat moves it to live, disable removes it.
func TestStoreEnablementLifecycle(t *testing.T) {
	st, ctx := testStore(t)
	defer st.Close()

	const (
		storeID = "zz-en-store"
		slug    = "zz-en-tenant"
		tid     = "zz-en-tid-0002"
	)
	cleanup := func() {
		st.pool.Exec(ctx, `DELETE FROM tenant_stores WHERE tenant_slug = $1`, slug)
		st.pool.Exec(ctx, `DELETE FROM memory_stores WHERE id = $1`, storeID)
		st.pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, slug)
	}
	cleanup()
	defer cleanup()

	if err := st.CreateMemoryStore(ctx, storeID, "EN Store", "", "",
		shared.MemoryStoreDefinition{ChatModel: "gpt-4o", EmbeddingModel: "text-embedding-3-small"}, "oid"); err != nil {
		t.Fatalf("create store: %v", err)
	}
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, tenant_id, enrollment, entitled_stores) VALUES ($1,'EN Tenant',$2,'bound',ARRAY[$3])`,
		slug, tid, storeID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	// Not enabled yet → not desired.
	if ds, _ := st.SyncDesired(ctx, tid); len(ds.MemoryStores) != 0 {
		t.Fatalf("store should not be desired before enable: %+v", ds.MemoryStores)
	}

	// Enable → desired + tenant view shows enabled/reconciling.
	if err := st.EnableStore(ctx, slug, storeID); err != nil {
		t.Fatalf("enable store: %v", err)
	}
	if ds, _ := st.SyncDesired(ctx, tid); len(ds.MemoryStores) != 1 || ds.MemoryStores[0].ID != storeID {
		t.Fatalf("enabled store not desired: %+v", ds.MemoryStores)
	}
	stores, _ := st.MemoryStoresForTenant(ctx, slug)
	if len(stores) != 1 || !stores[0].Enabled || stores[0].Health != "reconciling" {
		t.Fatalf("tenant store view wrong: %+v", stores)
	}

	// Heartbeat reports it live → tenant view reflects it.
	if err := st.ApplyHeartbeat(ctx, shared.Heartbeat{TenantID: tid, TenantName: "EN Tenant",
		MemoryStores: []shared.MemoryStoreStatus{{StoreID: storeID, Health: "live"}}}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if stores, _ := st.MemoryStoresForTenant(ctx, slug); stores[0].Health != "live" {
		t.Fatalf("store health after heartbeat = %q, want live", stores[0].Health)
	}

	// Enabling a store the tenant can't access is rejected.
	if err := st.EnableStore(ctx, slug, "no-such-store"); err != ErrStoreNotAccessible {
		t.Fatalf("expected ErrStoreNotAccessible, got %v", err)
	}

	// Disable → gone from desired.
	if err := st.DisableStore(ctx, slug, storeID); err != nil {
		t.Fatalf("disable store: %v", err)
	}
	if ds, _ := st.SyncDesired(ctx, tid); len(ds.MemoryStores) != 0 {
		t.Fatalf("disabled store still desired: %+v", ds.MemoryStores)
	}
}

// A tenant-owned agent is private to its tenant and needs no entitlement;
// platform agents the tenant isn't entitled to don't appear.
func TestTenantOwnedAgentVisibility(t *testing.T) {
	st, ctx := testStore(t)
	defer st.Close()

	const (
		slug      = "zz-own-tenant"
		tid       = "zz-own-tid-0003"
		ownAgent  = "zz-own-tenant-myagent"
		platAgent = "zz-own-plat-agent"
	)
	cleanup := func() {
		st.pool.Exec(ctx, `DELETE FROM catalog_agents WHERE id = ANY($1)`, []string{ownAgent, platAgent})
		st.pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, slug)
	}
	cleanup()
	defer cleanup()

	if _, err := st.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, tenant_id, enrollment) VALUES ($1,'Own Tenant',$2,'bound')`, slug, tid); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	if err := st.CreateCatalogAgent(ctx, ownAgent, "My Agent", "", "prompt", "gpt-4o", slug, "oid", shared.AgentDefinition{Instructions: "x"}); err != nil {
		t.Fatalf("create owned agent: %v", err)
	}
	if err := st.CreateCatalogAgent(ctx, platAgent, "Plat Agent", "", "prompt", "gpt-4o", "", "oid", shared.AgentDefinition{Instructions: "x"}); err != nil {
		t.Fatalf("create platform agent: %v", err)
	}

	list, err := st.CatalogForTenant(ctx, slug)
	if err != nil {
		t.Fatalf("catalog for tenant: %v", err)
	}
	var sawOwn, sawPlat bool
	for _, a := range list {
		switch a.ID {
		case ownAgent:
			sawOwn = true
			if !a.Owned || a.Platform || a.Entitled {
				t.Fatalf("owned agent flags wrong: %+v", a)
			}
		case platAgent:
			sawPlat = true
		}
	}
	if !sawOwn {
		t.Fatalf("tenant did not see its own agent")
	}
	if sawPlat {
		t.Fatalf("tenant saw a platform agent it isn't entitled to")
	}
}

// A JIT-provisioned tenant starts disabled (pending approval); a platform admin
// enables/disables its access.
func TestTenantAccessGate(t *testing.T) {
	st, ctx := testStore(t)
	defer st.Close()

	const tid = "zz-gate-tid-0004"
	cleanup := func() { st.pool.Exec(ctx, `DELETE FROM tenants WHERE tenant_id = $1`, tid) }
	cleanup()
	defer cleanup()

	tn, err := st.EnsureTenantForTID(ctx, tid, "Gate Co")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if tn.Enabled {
		t.Fatalf("a JIT-provisioned tenant must start disabled (pending approval)")
	}

	if err := st.SetTenantEnabled(ctx, tn.ID, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if got, _ := st.TenantByTID(ctx, tid); !got.Enabled {
		t.Fatalf("tenant should be enabled after SetTenantEnabled(true)")
	}

	if err := st.SetTenantEnabled(ctx, tn.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got, _ := st.TenantByTID(ctx, tid); got.Enabled {
		t.Fatalf("tenant should be disabled after SetTenantEnabled(false)")
	}

	if err := st.SetTenantEnabled(ctx, "no-such-tenant", true); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for unknown tenant, got %v", err)
	}
}

// A deployment gets the same catalog → entitle → enable → lifecycle as a memory
// store: platform-authored + entitled but not enabled → not desired; enabled →
// desired + tenant view (reconciling); a Synced/Healthy heartbeat derives live;
// disable removes it; an inaccessible enable is rejected.
func TestDeploymentLifecycle(t *testing.T) {
	st, ctx := testStore(t)
	defer st.Close()

	const (
		appID = "zz-dep-app"
		slug  = "zz-dep-tenant"
		tid   = "zz-dep-tid-0003"
	)
	cleanup := func() {
		st.pool.Exec(ctx, `DELETE FROM tenant_deployments WHERE tenant_slug = $1`, slug)
		st.pool.Exec(ctx, `DELETE FROM applications WHERE id = $1 OR owner_tenant = $2`, appID, slug)
		st.pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, slug)
	}
	cleanup()
	defer cleanup()

	// Platform authors a deployable chart.
	if err := st.CreateApplication(ctx, model.Application{
		ID: appID, Name: "ZZ Nginx", Owner: "", Namespace: "web",
		RepoURL: "https://charts.example/repo", Chart: "nginx", TargetRevision: "1.0.0",
	}, "oid"); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, tenant_id, enrollment, entitled_deployments) VALUES ($1,'ZZ Dep Tenant',$2,'bound',ARRAY[$3])`,
		slug, tid, appID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}

	// Platform list shows it as platform-owned.
	list, err := st.ApplicationList(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	foundPlatform := false
	for _, a := range list {
		if a.ID == appID {
			foundPlatform = a.Platform
		}
	}
	if !foundPlatform {
		t.Fatalf("deployment not in platform list as platform-owned")
	}

	// Entitled but not enabled → not desired; tenant view shows entitled+disabled.
	if ds, _ := st.SyncDesired(ctx, tid); len(ds.Applications) != 0 {
		t.Fatalf("deployment should not be desired before enable: %+v", ds.Applications)
	}
	apps, _ := st.ApplicationsForTenant(ctx, slug)
	if len(apps) != 1 || !apps[0].Entitled || apps[0].Enabled {
		t.Fatalf("tenant deployment view before enable wrong: %+v", apps)
	}

	// Enable → desired + tenant view enabled/reconciling.
	if err := st.EnableDeployment(ctx, slug, appID); err != nil {
		t.Fatalf("enable deployment: %v", err)
	}
	ds, _ := st.SyncDesired(ctx, tid)
	if len(ds.Applications) != 1 || ds.Applications[0].ID != appID ||
		ds.Applications[0].Chart != "nginx" || ds.Applications[0].Namespace != "web" {
		t.Fatalf("enabled deployment not desired correctly: %+v", ds.Applications)
	}
	if apps, _ := st.ApplicationsForTenant(ctx, slug); len(apps) != 1 || !apps[0].Enabled || apps[0].Health != shared.StatusReconciling {
		t.Fatalf("tenant deployment view after enable wrong: %+v", apps)
	}

	// A Synced/Healthy heartbeat derives the live lifecycle.
	if err := st.ApplyHeartbeat(ctx, shared.Heartbeat{TenantID: tid, TenantName: "ZZ Dep Tenant",
		Applications: []shared.ApplicationStatus{{ID: appID, SyncStatus: "Synced", HealthStatus: "Healthy"}}}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if apps, _ := st.ApplicationsForTenant(ctx, slug); apps[0].Health != shared.StatusLive || apps[0].SyncStatus != "Synced" {
		t.Fatalf("deployment health after heartbeat wrong: %+v", apps)
	}

	// Enabling an inaccessible deployment is rejected.
	if err := st.EnableDeployment(ctx, slug, "no-such-app"); err != ErrDeploymentNotAccessible {
		t.Fatalf("expected ErrDeploymentNotAccessible, got %v", err)
	}

	// Disable → gone from desired.
	if err := st.DisableDeployment(ctx, slug, appID); err != nil {
		t.Fatalf("disable deployment: %v", err)
	}
	if ds, _ := st.SyncDesired(ctx, tid); len(ds.Applications) != 0 {
		t.Fatalf("disabled deployment still desired: %+v", ds.Applications)
	}
}

// assignWaves is pure (no DB): a deployment's wave is 1 + the deepest enabled
// dependency chain; non-app deps are ignored and cycles must not hang.
func TestAssignWaves(t *testing.T) {
	apps := []shared.DesiredApplication{
		{ID: "a"},
		{ID: "b", DependsOn: []string{"a"}},
		{ID: "c", DependsOn: []string{"b", "a"}},
		{ID: "d", DependsOn: []string{"agent-x"}}, // non-app dep is ignored
	}
	assignWaves(apps)
	want := map[string]int{"a": 0, "b": 1, "c": 2, "d": 0}
	for _, a := range apps {
		if a.Wave != want[a.ID] {
			t.Fatalf("wave(%s) = %d, want %d", a.ID, a.Wave, want[a.ID])
		}
	}

	// A cycle must terminate (waves stay bounded, no infinite recursion).
	cyc := []shared.DesiredApplication{
		{ID: "x", DependsOn: []string{"y"}},
		{ID: "y", DependsOn: []string{"x"}},
	}
	assignWaves(cyc)
}

// Entitling/enabling an application cascades to its dependencies, and the in-use
// guard + auto-prune keep the graph consistent.
func TestDeploymentDependencyCascade(t *testing.T) {
	st, ctx := testStore(t)
	defer st.Close()

	const (
		appA = "zz-dh-a"
		appB = "zz-dh-b"
		slug = "zz-dh-tenant"
		tid  = "zz-dh-tid-0004"
	)
	cleanup := func() {
		st.pool.Exec(ctx, `DELETE FROM tenant_deployments WHERE tenant_slug = $1`, slug)
		st.pool.Exec(ctx, `DELETE FROM applications WHERE id IN ($1,$2)`, appA, appB)
		st.pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, slug)
	}
	cleanup()
	defer cleanup()

	mk := func(id, name string, deps []model.Dependency) {
		if err := st.CreateApplication(ctx, model.Application{
			ID: id, Name: name, Owner: "", Namespace: "web", RepoURL: "https://r", Chart: "c", Dependencies: deps,
		}, "oid"); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mk(appA, "A", nil)
	mk(appB, "B", []model.Dependency{{Kind: model.DepApplication, ID: appA}})
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, tenant_id, enrollment) VALUES ($1,'DH',$2,'bound')`, slug, tid); err != nil {
		t.Fatalf("tenant: %v", err)
	}

	// Entitle only B; the cascade must also entitle its dependency A.
	if err := st.SetDeploymentEntitlements(ctx, slug, []string{appB}); err != nil {
		t.Fatalf("entitle: %v", err)
	}
	apps, _ := st.ApplicationsForTenant(ctx, slug)
	entitled := map[string]bool{}
	for _, a := range apps {
		if a.Entitled {
			entitled[a.ID] = true
		}
	}
	if !entitled[appA] || !entitled[appB] {
		t.Fatalf("entitlement did not cascade to A: %+v", entitled)
	}

	// Enable B; the cascade must auto-enable A → both deployable, A wave 0, B wave 1.
	if err := st.EnableDeployment(ctx, slug, appB); err != nil {
		t.Fatalf("enable B: %v", err)
	}
	ds, _ := st.SyncDesired(ctx, tid)
	if len(ds.Applications) != 2 {
		t.Fatalf("both should deploy after cascade: %+v", ds.Applications)
	}
	wave := map[string]int{}
	for _, a := range ds.Applications {
		wave[a.ID] = a.Wave
	}
	if wave[appA] != 0 || wave[appB] != 1 {
		t.Fatalf("waves wrong: A=%d B=%d", wave[appA], wave[appB])
	}

	// In-use guard: A can't be disabled while B depends on it.
	if err := st.DisableDeployment(ctx, slug, appA); !errors.Is(err, ErrInUse) {
		t.Fatalf("expected ErrInUse disabling A, got %v", err)
	}
	// Disabling B prunes the auto-enabled A too.
	if err := st.DisableDeployment(ctx, slug, appB); err != nil {
		t.Fatalf("disable B: %v", err)
	}
	if ds, _ := st.SyncDesired(ctx, tid); len(ds.Applications) != 0 {
		t.Fatalf("both should be gone after disabling B: %+v", ds.Applications)
	}
}

// The typed dependency graph is enforced end-to-end: author-time validation
// (allowed edges, existence, cycles), entitlement cascade, enable cascade, and
// the in-use disable guard + auto-prune — across infrastructure, applications,
// agents, and memory stores.
func TestDependencyGraphEnforcement(t *testing.T) {
	st, ctx := testStore(t)
	defer st.Close()

	const (
		infra1 = "zz-e-infra1"
		infra2 = "zz-e-infra2"
		store1 = "zz-e-store"
		agent1 = "zz-e-agent"
		app1   = "zz-e-app"
		slug   = "zz-e-tenant"
		tid    = "zz-e-tid-0005"
	)
	cleanup := func() {
		st.pool.Exec(ctx, `DELETE FROM tenant_infrastructure WHERE tenant_slug=$1`, slug)
		st.pool.Exec(ctx, `DELETE FROM tenant_deployments WHERE tenant_slug=$1`, slug)
		st.pool.Exec(ctx, `DELETE FROM tenant_stores WHERE tenant_slug=$1`, slug)
		st.pool.Exec(ctx, `DELETE FROM agents WHERE tenant_slug=$1`, slug)
		st.pool.Exec(ctx, `DELETE FROM applications WHERE id=$1`, app1)
		st.pool.Exec(ctx, `DELETE FROM infrastructure WHERE id IN ($1,$2)`, infra1, infra2)
		st.pool.Exec(ctx, `DELETE FROM catalog_agents WHERE id=$1`, agent1)
		st.pool.Exec(ctx, `DELETE FROM memory_stores WHERE id=$1`, store1)
		st.pool.Exec(ctx, `DELETE FROM tenants WHERE id=$1`, slug)
	}
	cleanup()
	defer cleanup()

	// Platform entities: a store, an agent that uses it, two infra (infra1→infra2).
	if err := st.CreateMemoryStore(ctx, store1, "Store", "", "", shared.MemoryStoreDefinition{ChatModel: "gpt-4o", EmbeddingModel: "e"}, "oid"); err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := st.CreateCatalogAgent(ctx, agent1, "Agent", "", "prompt", "gpt-4o", "", "oid", shared.AgentDefinition{Instructions: "x", MemoryStore: store1}); err != nil {
		t.Fatalf("agent: %v", err)
	}
	if err := st.CreateInfrastructure(ctx, model.Infrastructure{ID: infra2, Name: "Infra2"}, "oid"); err != nil {
		t.Fatalf("infra2: %v", err)
	}
	if err := st.ValidateDependencies(ctx, model.DepInfrastructure, infra1, "", []model.Dependency{{Kind: model.DepInfrastructure, ID: infra2}}); err != nil {
		t.Fatalf("valid infra→infra rejected: %v", err)
	}
	if err := st.CreateInfrastructure(ctx, model.Infrastructure{ID: infra1, Name: "Infra1", Dependencies: []model.Dependency{{Kind: model.DepInfrastructure, ID: infra2}}}, "oid"); err != nil {
		t.Fatalf("infra1: %v", err)
	}

	// Author-time validation.
	if err := st.ValidateDependencies(ctx, model.DepInfrastructure, infra1, "", []model.Dependency{{Kind: model.DepApplication, ID: app1}}); !errors.Is(err, ErrBadDependency) {
		t.Fatalf("expected ErrBadDependency for infra→app, got %v", err)
	}
	if err := st.ValidateDependencies(ctx, model.DepApplication, app1, "", []model.Dependency{{Kind: model.DepInfrastructure, ID: "nope"}}); !errors.Is(err, ErrBadDependency) {
		t.Fatalf("expected ErrBadDependency for missing target, got %v", err)
	}
	if err := st.ValidateDependencies(ctx, model.DepInfrastructure, infra2, "", []model.Dependency{{Kind: model.DepInfrastructure, ID: infra2}}); !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("expected ErrDependencyCycle for self-dep, got %v", err)
	}
	if err := st.ValidateDependencies(ctx, model.DepInfrastructure, infra2, "", []model.Dependency{{Kind: model.DepInfrastructure, ID: infra1}}); !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("expected ErrDependencyCycle for infra2→infra1, got %v", err)
	}

	// App depends on infra1 + agent1 (both allowed).
	appDeps := []model.Dependency{{Kind: model.DepInfrastructure, ID: infra1}, {Kind: model.DepAgent, ID: agent1}}
	if err := st.ValidateDependencies(ctx, model.DepApplication, app1, "", appDeps); err != nil {
		t.Fatalf("valid app deps rejected: %v", err)
	}
	if err := st.CreateApplication(ctx, model.Application{ID: app1, Name: "App", Namespace: "web", RepoURL: "https://r", Chart: "c", Dependencies: appDeps}, "oid"); err != nil {
		t.Fatalf("app: %v", err)
	}

	if _, err := st.pool.Exec(ctx, `INSERT INTO tenants (id,name,tenant_id,enrollment) VALUES ($1,'E',$2,'bound')`, slug, tid); err != nil {
		t.Fatalf("tenant: %v", err)
	}

	// Entitle only the app; the cascade must entitle infra1, infra2 (transitive),
	// agent1, and the agent's store1.
	if err := st.SetDeploymentEntitlements(ctx, slug, []string{app1}); err != nil {
		t.Fatalf("entitle: %v", err)
	}
	assertEntitled := func(col, id string) {
		var ok bool
		st.pool.QueryRow(ctx, `SELECT $2 = ANY(`+col+`) FROM tenants WHERE id=$1`, slug, id).Scan(&ok)
		if !ok {
			t.Fatalf("expected %s in %s", id, col)
		}
	}
	assertEntitled("entitled_infrastructure", infra1)
	assertEntitled("entitled_infrastructure", infra2)
	assertEntitled("entitled_agents", agent1)
	assertEntitled("entitled_stores", store1)

	// Enable the app → cascade enables infra1, infra2, agent1, store1.
	if err := st.EnableDeployment(ctx, slug, app1); err != nil {
		t.Fatalf("enable app: %v", err)
	}
	count := func(q, id string) int {
		var n int
		st.pool.QueryRow(ctx, q, slug, id).Scan(&n)
		return n
	}
	if count(`SELECT count(*) FROM tenant_infrastructure WHERE tenant_slug=$1 AND infra_id=$2`, infra1) == 0 ||
		count(`SELECT count(*) FROM tenant_infrastructure WHERE tenant_slug=$1 AND infra_id=$2`, infra2) == 0 ||
		count(`SELECT count(*) FROM agents WHERE tenant_slug=$1 AND agent_id=$2`, agent1) == 0 ||
		count(`SELECT count(*) FROM tenant_stores WHERE tenant_slug=$1 AND store_id=$2`, store1) == 0 {
		t.Fatalf("enable did not cascade to all dependencies")
	}

	// In-use guard: infra1 can't be disabled while the app depends on it.
	if err := st.DisableInfrastructure(ctx, slug, infra1); !errors.Is(err, ErrInUse) {
		t.Fatalf("expected ErrInUse disabling infra1, got %v", err)
	}

	// Un-entitle guard: can't remove an infra entitlement while it's enabled.
	if err := st.SetInfrastructureEntitlements(ctx, slug, []string{}); !errors.Is(err, ErrEntitlementInUse) {
		t.Fatalf("expected ErrEntitlementInUse un-entitling enabled infra, got %v", err)
	}

	// Disable the app → every auto-enabled dependency is pruned.
	if err := st.DisableDeployment(ctx, slug, app1); err != nil {
		t.Fatalf("disable app: %v", err)
	}
	var remaining int
	st.pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM tenant_infrastructure WHERE tenant_slug=$1)
		      + (SELECT count(*) FROM tenant_stores WHERE tenant_slug=$1)
		      + (SELECT count(*) FROM agents WHERE tenant_slug=$1)
		      + (SELECT count(*) FROM tenant_deployments WHERE tenant_slug=$1)`, slug).Scan(&remaining)
	if remaining != 0 {
		t.Fatalf("auto deps not pruned after disabling app: %d remain", remaining)
	}

	// Un-entitle guard (dependent): the app is still entitled and depends on infra1.
	if err := st.SetInfrastructureEntitlements(ctx, slug, []string{infra2}); !errors.Is(err, ErrEntitlementInUse) {
		t.Fatalf("expected ErrEntitlementInUse un-entitling a depended-on infra, got %v", err)
	}
	// Removing the app entitlement first, then both infra together, is allowed.
	if err := st.SetDeploymentEntitlements(ctx, slug, []string{}); err != nil {
		t.Fatalf("un-entitle app: %v", err)
	}
	if err := st.SetInfrastructureEntitlements(ctx, slug, []string{}); err != nil {
		t.Fatalf("un-entitle infra after app removed: %v", err)
	}
}
