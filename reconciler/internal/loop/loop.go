package loop

import (
	"context"
	"log/slog"
	"math/rand"
	"time"

	"github.com/inception42/cortex/reconciler/internal/config"
	"github.com/inception42/cortex/reconciler/internal/controlplane"
	"github.com/inception42/cortex/reconciler/internal/foundry"
	"github.com/inception42/cortex/reconciler/internal/tokens"
	"github.com/inception42/cortex/shared"
)

// Reconciler runs the single idempotent loop: pull desired state, converge the
// in-tenant Foundry runtime toward it, and heartbeat actual state + install
// identity back to the control plane.
type Reconciler struct {
	cfg     config.Config
	cp      *controlplane.Client
	foundry *foundry.Foundry
}

func New(cfg config.Config, src tokens.Source) *Reconciler {
	return &Reconciler{
		cfg:     cfg,
		cp:      controlplane.New(cfg.ControlPlaneURL, src),
		foundry: foundry.New(),
	}
}

func (r *Reconciler) Run(ctx context.Context) {
	r.once(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.jittered()):
			r.once(ctx)
		}
	}
}

func (r *Reconciler) jittered() time.Duration {
	base := r.cfg.PollInterval
	return base + time.Duration(rand.Int63n(int64(base)/5+1))
}

func (r *Reconciler) once(ctx context.Context) {
	desired, err := r.cp.Sync(ctx)
	if err != nil {
		slog.Warn("sync failed; will retry", "err", err)
		return
	}
	statuses := r.foundry.Reconcile(desired.Agents)
	hb := r.heartbeat(statuses)
	if err := r.cp.Heartbeat(ctx, hb); err != nil {
		slog.Warn("heartbeat failed", "err", err)
		return
	}
	slog.Info("reconciled", "desired", len(desired.Agents), "healthy", countHealthy(statuses))
}

func (r *Reconciler) heartbeat(statuses []shared.AgentStatus) shared.Heartbeat {
	return shared.Heartbeat{
		TenantID:           r.cfg.TenantID,
		TenantName:         r.cfg.TenantName,
		Region:             r.cfg.Region,
		Plan:               r.cfg.Plan,
		SubscriptionID:     r.cfg.SubscriptionID,
		ReconcilerIdentity: r.cfg.ReconcilerIdentity,
		FoundryProject:     r.cfg.FoundryProject,
		ReconcilerVersion:  r.cfg.ReconcilerVersion,
		Agents:             statuses,
	}
}

func countHealthy(s []shared.AgentStatus) int {
	n := 0
	for _, a := range s {
		if a.Health == "healthy" {
			n++
		}
	}
	return n
}
