package foundry

import (
	"sync"

	"github.com/inception42/cortex/shared"
)

// Foundry is a stub for the in-tenant Microsoft Foundry agent runtime.
//
// A real implementation would, per PLAN.md §7: ensure model deployments,
// create/update the prompt agent via the Foundry project REST API, assign the
// agent's Entra Agent ID data-plane RBAC, expose the API endpoint, and read back
// health. Here we simulate convergence: a newly-desired agent reports
// "reconciling" for one loop, then "healthy" once "provisioned".
type Foundry struct {
	mu   sync.Mutex
	seen map[string]string // agentID → the version last converged to
}

func New() *Foundry {
	return &Foundry{seen: map[string]string{}}
}

// Reconcile drives actual state toward desired and returns the actual state.
// A newly-desired agent or a version change reports "reconciling" for one loop
// (converging), then "healthy" once the desired version is in place.
func (f *Foundry) Reconcile(desired []shared.DesiredAgent) []shared.AgentStatus {
	f.mu.Lock()
	defer f.mu.Unlock()

	live := make(map[string]bool, len(desired))
	out := make([]shared.AgentStatus, 0, len(desired))
	for _, d := range desired {
		live[d.AgentID] = true
		health := "healthy"
		if f.seen[d.AgentID] != d.Version {
			health = "reconciling" // new agent or version bump — converging
			f.seen[d.AgentID] = d.Version
		}
		out = append(out, shared.AgentStatus{
			AgentID: d.AgentID,
			Version: d.Version, // actual converges to the desired version
			Health:  health,
			// TODO(telemetry): report real 30-day call volume from Azure Monitor /
			// Foundry usage metrics. Until then this is 0 and Usage/Metering read
			// as empty rather than showing fabricated numbers.
			Calls30d: 0,
		})
	}
	// Forget agents that are no longer desired (de-provisioned).
	for id := range f.seen {
		if !live[id] {
			delete(f.seen, id)
		}
	}
	return out
}
