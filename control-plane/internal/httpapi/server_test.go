package httpapi

import (
	"testing"

	"github.com/inception42/cortex/control-plane/internal/model"
)

func TestGateAgentHealth(t *testing.T) {
	agents := func() []model.Agent {
		return []model.Agent{
			{ID: "a", Health: "live"},
			{ID: "b", Health: "blocked"},
		}
	}

	t.Run("live tenant: reconciler-reported health passes through", func(t *testing.T) {
		out := gateAgentHealth(model.Tenant{Lifecycle: "live"}, agents())
		if out[0].Health != "live" || out[1].Health != "blocked" {
			t.Fatalf("expected reported health preserved, got %+v", out)
		}
	})

	for _, lc := range []string{"degraded", "enrolling", "suspended", ""} {
		t.Run("non-live ("+lc+"): health is unreported", func(t *testing.T) {
			out := gateAgentHealth(model.Tenant{Lifecycle: lc}, agents())
			for _, a := range out {
				if a.Health != "unknown" {
					t.Fatalf("lifecycle %q: expected unknown, got %q", lc, a.Health)
				}
			}
		})
	}
}
