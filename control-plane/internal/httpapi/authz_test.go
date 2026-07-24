package httpapi

import (
	"context"
	"os"
	"testing"

	"github.com/inception42/cortex/control-plane/internal/model"
	"github.com/inception42/cortex/control-plane/internal/store"
)

func authzTestStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://bensinclair@localhost:5432/cortex?sslmode=disable"
	}
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Skipf("no database available: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st, ctx
}

// TestAuthorizeTenantEnforcesEnabledPerTenant: disabling ONE of a member's
// tenants cuts off exactly that tenant (not all-or-nothing), while platform
// admins may still act on a disabled tenant (to re-enable it).
func TestAuthorizeTenantEnforcesEnabledPerTenant(t *testing.T) {
	st, ctx := authzTestStore(t)
	defer st.Close()
	srv := &Server{store: st, platformTID: "platform-tid-authz"}

	t1, err := st.CreatePlatformTenant(ctx, "ZZ Authz One", "uksouth", "team", "zz-authz-sub")
	if err != nil {
		t.Fatalf("create t1: %v", err)
	}
	defer st.DeleteTenant(ctx, t1.ID)
	t2, err := st.CreatePlatformTenant(ctx, "ZZ Authz Two", "uksouth", "team", "zz-authz-sub")
	if err != nil {
		t.Fatalf("create t2: %v", err)
	}
	defer st.DeleteTenant(ctx, t2.ID)

	if err := st.AddMembership(ctx, t1.ID, "u@corp.com", "admin"); err != nil {
		t.Fatalf("add m1: %v", err)
	}
	if err := st.AddMembership(ctx, t2.ID, "u@corp.com", "admin"); err != nil {
		t.Fatalf("add m2: %v", err)
	}

	member := model.Identity{OID: "oid-u", Email: "u@corp.com", TID: "some-directory", Role: model.RoleTenant}
	admin := model.Identity{OID: "oid-admin", Email: "admin@platform", TID: "platform-tid-authz", Role: model.RolePlatform}

	reload := func(slug string) model.Tenant {
		tn, err := st.TenantBySlug(ctx, slug)
		if err != nil {
			t.Fatalf("reload %s: %v", slug, err)
		}
		return tn
	}

	// Both enabled: the member may act on either.
	if !srv.authorizeTenant(ctx, member, reload(t1.ID)) || !srv.authorizeTenant(ctx, member, reload(t2.ID)) {
		t.Fatal("member should be authorized for both enabled tenants")
	}

	// Disable t1 only.
	if err := st.SetTenantEnabled(ctx, t1.ID, false); err != nil {
		t.Fatalf("disable t1: %v", err)
	}
	if srv.authorizeTenant(ctx, member, reload(t1.ID)) {
		t.Fatal("disabling t1 must cut off the member from t1")
	}
	if !srv.authorizeTenant(ctx, member, reload(t2.ID)) {
		t.Fatal("t2 must stay accessible after t1 is disabled")
	}
	// Platform admins may still act on the disabled tenant (to re-enable it).
	if !srv.authorizeTenant(ctx, admin, reload(t1.ID)) {
		t.Fatal("platform admin must be able to act on a disabled tenant")
	}
}
