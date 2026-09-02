package store

import (
	"errors"
	"testing"
	"time"

	"github.com/inception42/cortex/control-plane/internal/model"
	"github.com/inception42/cortex/shared"
)

// The single most important property of secret sets: a value must never reach
// the Helm values, because those are copied verbatim into the Argo Application's
// spec.source.helm.values and are readable by anyone with cluster access. Wiring
// is the only path from a dependency into an app's values, so it is the only
// place this could go wrong.

func TestSecretSetSourcesExposeNoValues(t *testing.T) {
	sets := []shared.DesiredSecretSet{{
		ID:         "db-creds",
		Name:       "Database credentials",
		SecretName: "cortex-secret-db-creds",
		VaultURI:   "https://kv.vault.azure.net/",
		Keys:       []string{"password", "username"},
		Complete:   true,
	}}

	src := secretSetSources(sets)
	out, ok := src["secret_set:db-creds"]
	if !ok {
		t.Fatal("no wiring source produced for the set")
	}

	// Everything exposed must be a non-secret fact: the Secret's name, or a key's
	// name. A key's VALUE is not present in DesiredSecretSet at all, so the only
	// way this could regress is by someone adding one.
	allowed := map[string]bool{
		"cortex-secret-db-creds": true,
		"password":               true,
		"username":               true,
	}
	for k, v := range out {
		s, isString := v.(string)
		if !isString {
			t.Fatalf("wiring output %q is not a string: %#v", k, v)
		}
		if !allowed[s] {
			t.Fatalf("wiring output %q exposes %q, which is not a name — a value must never be wireable", k, s)
		}
	}

	if out["secretName"] != "cortex-secret-db-creds" {
		t.Fatalf("secretName not wireable, so a chart cannot reference the Secret: %v", out["secretName"])
	}
}

func TestApplyWiringCannotCarryASecretValue(t *testing.T) {
	// Wire a secret set into an app and confirm what lands in the values is the
	// Secret's NAME. This is the end-to-end statement of the design: the chart is
	// configured to go and find the secret, rather than being handed it.
	sets := []shared.DesiredSecretSet{{
		ID: "db-creds", SecretName: "cortex-secret-db-creds",
		Keys: []string{"password"}, Complete: true,
	}}
	values := applyWiring("", []shared.WireLink{
		{SourceKind: "secret_set", SourceID: "db-creds", Output: "secretName", HelmPath: "database.existingSecret"},
	}, secretSetSources(sets))

	if want := "cortex-secret-db-creds"; !contains(values, want) {
		t.Fatalf("expected the Secret name %q in the values, got:\n%s", want, values)
	}
}

func TestSecretSetOutstanding(t *testing.T) {
	s := model.SecretSet{
		Keys:    []string{"username", "password", "token"},
		KeysSet: []string{"username"},
	}
	got := s.Outstanding()
	if len(got) != 2 || got[0] != "password" || got[1] != "token" {
		t.Fatalf("outstanding = %v, want [password token]", got)
	}

	// A set with every key filled has nothing outstanding, which is what gates
	// the application that depends on it.
	full := model.SecretSet{Keys: []string{"a"}, KeysSet: []string{"a"}}
	if len(full.Outstanding()) != 0 {
		t.Fatalf("a fully-populated set reported %v outstanding", full.Outstanding())
	}
}

func TestSecretSetName(t *testing.T) {
	// Fixed and documented, because a chart author writes it by hand.
	if got, want := (model.SecretSet{ID: "db-creds"}).SecretName(), "cortex-secret-db-creds"; got != want {
		t.Fatalf("SecretName = %q, want %q", got, want)
	}
}

func TestSecretSetIsALeafAndIsDependable(t *testing.T) {
	// Applications and infrastructure may depend on a secret set; a secret set
	// depends on nothing (there is nothing for it to need).
	if !edgeAllowed(model.DepApplication, model.DepSecretSet) {
		t.Error("an application must be able to depend on a secret set")
	}
	if !edgeAllowed(model.DepInfrastructure, model.DepSecretSet) {
		t.Error("infrastructure must be able to depend on a secret set")
	}
	for _, k := range allKinds {
		if edgeAllowed(model.DepSecretSet, k) {
			t.Errorf("a secret set must be a leaf, but may depend on %s", k)
		}
	}
	if entitlementColumn[model.DepSecretSet] == "" {
		t.Error("secret sets have no entitlement column, so they cannot be granted to a tenant")
	}
	if catalogTable[model.DepSecretSet] == "" {
		t.Error("secret sets have no catalog table registered")
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// TestSecretSetLifecycle drives the database path: declare a set, entitle it,
// enable it with a subset of keys, and confirm the store reports what is still
// outstanding without ever holding a value.
func TestSecretSetLifecycle(t *testing.T) {
	st, ctx := testStore(t)

	slug := "t-secrets-" + randSuffix()
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, tenant_id) VALUES ($1,$1,$1)`, slug); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = st.pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, slug) })

	setID := "db-creds-" + randSuffix()
	if _, err := st.Apply(ctx, "tester", ApplyBatch{SecretSets: []model.SecretSet{{
		ID: setID, Name: "DB creds", Keys: []string{"username", "password"},
	}}}); err != nil {
		t.Fatalf("create set: %v", err)
	}
	t.Cleanup(func() { _ = st.DeleteSecretSet(ctx, setID) })

	// Not entitled yet — enabling must be refused rather than silently working.
	if err := st.EnableSecretSet(ctx, slug, setID, []string{"username"}, "https://kv/"); !errors.Is(err, ErrSecretSetNotAccessible) {
		t.Fatalf("enable without entitlement = %v, want ErrSecretSetNotAccessible", err)
	}

	if err := st.SetSecretSetEntitlements(ctx, slug, []string{setID}); err != nil {
		t.Fatalf("entitle: %v", err)
	}

	// Enable with only one of the two keys supplied.
	if err := st.EnableSecretSet(ctx, slug, setID, []string{"username"}, "https://kv/"); err != nil {
		t.Fatalf("enable: %v", err)
	}
	sets, err := st.SecretSetsForTenant(ctx, slug)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var got *model.SecretSet
	for i := range sets {
		if sets[i].ID == setID {
			got = &sets[i]
		}
	}
	if got == nil {
		t.Fatal("set not returned in the tenant view")
	}
	if !got.Enabled {
		t.Error("set not reported as enabled")
	}
	if out := got.Outstanding(); len(out) != 1 || out[0] != "password" {
		t.Errorf("outstanding = %v, want [password]", out)
	}
	if got.Health != "blocked" {
		t.Errorf("health = %q, want blocked while a key has no value", got.Health)
	}

	// Supplying the second key later must not appear to unset the first.
	if err := st.EnableSecretSet(ctx, slug, setID, []string{"password"}, "https://kv/"); err != nil {
		t.Fatalf("second enable: %v", err)
	}
	keys, err := st.SecretSetKeysSet(ctx, slug, setID)
	if err != nil {
		t.Fatalf("keys set: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("keys_set = %v, want both keys retained across two calls", keys)
	}

	// An incomplete set holds its dependents; a complete one is deliverable.
	desired, err := st.desiredSecretSets(ctx, slug)
	if err != nil {
		t.Fatalf("desired: %v", err)
	}
	if len(desired) != 1 || !desired[0].Complete {
		t.Fatalf("desired = %+v, want one complete set", desired)
	}
	if desired[0].SecretName != "cortex-secret-"+setID {
		t.Errorf("SecretName = %q", desired[0].SecretName)
	}

	// Un-entitling something the tenant has enabled must be refused.
	if err := st.SetSecretSetEntitlements(ctx, slug, nil); !errors.Is(err, ErrEntitlementInUse) {
		t.Fatalf("un-entitle while enabled = %v, want ErrEntitlementInUse", err)
	}

	if err := st.DisableSecretSet(ctx, slug, setID); err != nil {
		t.Fatalf("disable: %v", err)
	}
}

func randSuffix() string {
	return time.Now().Format("150405.000000")[:6] + "x"
}

// A held application must still be SENT, flagged, not omitted.
//
// The reconciler deletes any Argo Application it does not see on the sync, and
// Argo's finalizer cascade-deletes the workloads with it. So omitting a held app
// meant that adding a dependency to a running deployment — or a dependency
// merely regressing — tore the running app down instead of pausing it. This test
// exists because that failure is invisible until it happens in production.
func TestHeldApplicationIsSentNotOmitted(t *testing.T) {
	st, ctx := testStore(t)

	sfx := randSuffix()
	slug, tid := "t-held-"+sfx, "tid-held-"+sfx
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO tenants (id, name, tenant_id, enabled) VALUES ($1,$1,$2,true)`, slug, tid); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = st.pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, slug) })

	setID, appID := "creds-"+sfx, "app-"+sfx
	if _, err := st.Apply(ctx, "tester", ApplyBatch{
		SecretSets: []model.SecretSet{{ID: setID, Name: "Creds", Keys: []string{"password"}}},
		Applications: []model.Application{{
			ID: appID, Name: "App", Namespace: "held-ns",
			RepoURL: "https://charts.example.com", Chart: "app", TargetRevision: "1.0.0",
			Dependencies: []model.Dependency{{Kind: model.DepSecretSet, ID: setID}},
		}},
	}); err != nil {
		t.Fatalf("author: %v", err)
	}
	t.Cleanup(func() {
		_ = st.DeleteApplication(ctx, appID)
		_ = st.DeleteSecretSet(ctx, setID)
	})

	if err := st.SetSecretSetEntitlements(ctx, slug, []string{setID}); err != nil {
		t.Fatalf("entitle set: %v", err)
	}
	if err := st.SetDeploymentEntitlements(ctx, slug, []string{appID}); err != nil {
		t.Fatalf("entitle app: %v", err)
	}
	// Enable the app. Its secret set is pulled in automatically, with no values.
	if err := st.EnableDeployment(ctx, slug, appID); err != nil {
		t.Fatalf("enable app: %v", err)
	}

	ds, err := st.SyncDesired(ctx, mustTenantByTID(t, ctx, st, tid))
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	var got *shared.DesiredApplication
	for i := range ds.Applications {
		if ds.Applications[i].ID == appID {
			got = &ds.Applications[i]
		}
	}
	if got == nil {
		t.Fatal("held app was omitted from the sync — the reconciler would delete it and its workloads")
	}
	if !got.Held {
		t.Error("app is deployable despite its secret set having no values")
	}
	if got.HoldReason == "" {
		t.Error("no hold reason, so an operator cannot tell why it stopped")
	}

	// Once the value exists the app becomes deployable, without being recreated.
	if err := st.EnableSecretSet(ctx, slug, setID, []string{"password"}, "https://kv/"); err != nil {
		t.Fatalf("enable set: %v", err)
	}
	ds2, err := st.SyncDesired(ctx, mustTenantByTID(t, ctx, st, tid))
	if err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	for _, a := range ds2.Applications {
		if a.ID == appID && a.Held {
			t.Fatalf("app still held after every key was supplied: %s", a.HoldReason)
		}
	}
}
