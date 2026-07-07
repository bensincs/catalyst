package store

import (
	"context"
	"encoding/json"
	"os"
	"testing"

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

	// 1. Author a catalog agent (creates v1.0.0), then publish v1.1.0.
	if err := st.CreateCatalogAgent(ctx, agentID, "ZZ Test Agent", "desc", "prompt", "gpt-4o", "oid-test", shared.AgentDefinition{Instructions: "v1"}); err != nil {
		t.Fatalf("create catalog agent: %v", err)
	}
	if err := st.PublishVersion(ctx, agentID, "1.1.0", "stable", "notes", 25, shared.AgentDefinition{Instructions: "v1.1"}); err != nil {
		t.Fatalf("publish version: %v", err)
	}
	cat, err := st.CatalogList(ctx)
	if err != nil {
		t.Fatalf("catalog list: %v", err)
	}
	var found *struct{ latest string }
	for _, a := range cat {
		if a.ID == agentID {
			found = &struct{ latest string }{a.LatestVersion}
			if len(a.Versions) != 2 {
				t.Fatalf("expected 2 versions, got %d", len(a.Versions))
			}
		}
	}
	if found == nil || found.latest != "1.1.0" {
		t.Fatalf("expected latest 1.1.0, got %+v", found)
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
	if agents[0].Name != "ZZ Test Agent" || agents[0].Version != "1.1.0" {
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
	if err := st.CreateMemoryStore(ctx, storeID, "ZZ Store", "platform memory", "", json.RawMessage(`{"scope":"user"}`), "oid-test"); err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := st.CreateCatalogAgent(ctx, agentID, "ZZ MS Agent", "d", "prompt", "gpt-4o", "oid-test",
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

	// 3. Enable the agent; SyncDesired carries the effective store + its config.
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
	if len(ds.MemoryStores) != 1 || ds.MemoryStores[0].ID != storeID || len(ds.MemoryStores[0].Config) == 0 {
		t.Fatalf("desired memory stores wrong: %+v", ds.MemoryStores)
	}

	// 4. Tenant creates their own store and connects the agent to it (override).
	tenantStore := slug + "-notes"
	if err := st.CreateMemoryStore(ctx, tenantStore, "Notes", "tenant memory", slug, json.RawMessage(`{"scope":"tenant"}`), "oid-tenant"); err != nil {
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
