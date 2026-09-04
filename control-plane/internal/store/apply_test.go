package store

import (
	"errors"
	"testing"

	"github.com/inception42/cortex/shared"

	"github.com/inception42/cortex/control-plane/internal/model"
)

func TestApplyBatch(t *testing.T) {
	st, ctx := testStore(t)
	defer st.Close()

	ids := []string{"zz-apply-infra", "zz-apply-infra-b", "zz-apply-app"}
	cleanup := func() {
		for _, id := range ids {
			st.pool.Exec(ctx, `DELETE FROM applications WHERE id = $1`, id)
			st.pool.Exec(ctx, `DELETE FROM infrastructure WHERE id = $1`, id)
		}
	}
	cleanup()
	defer cleanup()

	// 1. A batch: an infra + an app that depends on it — the intra-batch dependency
	//    must resolve (the infra is a sibling in the same transaction).
	batch := ApplyBatch{
		Infrastructure: []model.Infrastructure{{
			ID: "zz-apply-infra", Name: "ZZ Apply Infra", BicepModule: "{}", ArmTemplate: "{}",
		}},
		Applications: []model.Application{{
			ID: "zz-apply-app", Name: "ZZ Apply App", Namespace: "default",
			RepoURL: "https://charts.example/repo", Chart: "nginx", TargetRevision: "1.0.0",
			Dependencies: []model.Dependency{{Kind: model.DepInfrastructure, ID: "zz-apply-infra"}},
		}},
	}
	res, err := st.Apply(ctx, "oid-test", batch)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(res.Infrastructure) != 1 || len(res.Applications) != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if _, err := st.InfrastructureByID(ctx, "zz-apply-infra"); err != nil {
		t.Fatalf("infra not created: %v", err)
	}

	// 2. An app depending on a nonexistent target is rejected (nothing is created).
	bad := ApplyBatch{
		Applications: []model.Application{{
			ID: "zz-apply-app-bad", Name: "ZZ Bad", Namespace: "default",
			RepoURL: "https://charts.example/repo", Chart: "nginx", TargetRevision: "1.0.0",
			Dependencies: []model.Dependency{{Kind: model.DepInfrastructure, ID: "does-not-exist"}},
		}},
	}
	if _, err := st.Apply(ctx, "oid-test", bad); !errors.Is(err, ErrBadDependency) {
		t.Fatalf("expected ErrBadDependency, got %v", err)
	}

	// 3. A cycle within the batch is rejected.
	cyc := ApplyBatch{
		Infrastructure: []model.Infrastructure{
			{ID: "zz-apply-infra", Name: "A", BicepModule: "{}", ArmTemplate: "{}", Dependencies: []model.Dependency{{Kind: model.DepInfrastructure, ID: "zz-apply-infra-b"}}},
			{ID: "zz-apply-infra-b", Name: "B", BicepModule: "{}", ArmTemplate: "{}", Dependencies: []model.Dependency{{Kind: model.DepInfrastructure, ID: "zz-apply-infra"}}},
		},
	}
	if _, err := st.Apply(ctx, "oid-test", cyc); !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("expected ErrDependencyCycle, got %v", err)
	}
}

// A service declares the calls it wants made when an application is deployed,
// and those survive a round trip through the store.
//
// The alternative was the reconciler calling one service directly, which meant
// a generic component holding that service's namespace, secret name, DNS name
// and API shape. This is where that knowledge belongs.
func TestApplicationHooksRoundTrip(t *testing.T) {
	st, ctx := testStore(t)
	defer st.Close()
	id := "zz-hooks-app"
	st.pool.Exec(ctx, `DELETE FROM applications WHERE id = $1`, id)
	defer st.pool.Exec(ctx, `DELETE FROM applications WHERE id = $1`, id)

	hook := shared.ApplicationHook{
		Name:    "authz app registration",
		Method:  "PUT",
		Service: shared.ServiceRef{Namespace: "cortex-authz", Name: "authz-service", Port: 8080},
		Path:    "/v1/apps/{{app}}/roles/user",
		Body:    "{}",
		TokenSecret: &shared.SecretKeyRef{
			Namespace: "cortex-authz", Name: "cortex-secret-authz-secrets", Key: "authz-admin-token",
		},
	}
	if _, err := st.Apply(ctx, "oid-test", ApplyBatch{Applications: []model.Application{{
		ID: id, Name: "App access", Namespace: "cortex-authz",
		RepoURL: "oci://example/charts", Chart: "authz-service", TargetRevision: "0.1.0",
		ApplicationHooks: []shared.ApplicationHook{hook},
	}}}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	got, err := st.ApplicationByID(ctx, id)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got.ApplicationHooks) != 1 {
		t.Fatalf("want 1 hook, got %d", len(got.ApplicationHooks))
	}
	h := got.ApplicationHooks[0]
	if h.Path != hook.Path || h.Method != hook.Method || h.Service != hook.Service {
		t.Errorf("hook = %+v, want %+v", h, hook)
	}
	if h.TokenSecret == nil || h.TokenSecret.Key != "authz-admin-token" {
		t.Errorf("the credential reference must survive; got %+v", h.TokenSecret)
	}
}
