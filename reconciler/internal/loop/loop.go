package loop

import (
	"context"
	"log/slog"
	"math/rand"
	"time"

	"github.com/inception42/cortex/reconciler/internal/cluster"
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
	cluster *cluster.Client // nil when the cluster feature is disabled
}

func New(cfg config.Config, apiSrc tokens.Source, foundrySrc tokens.Source, clusterClient *cluster.Client) *Reconciler {
	return &Reconciler{
		cfg:     cfg,
		cp:      controlplane.New(cfg.ControlPlaneURL, apiSrc),
		foundry: foundry.New(cfg, foundrySrc),
		cluster: clusterClient,
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
	statuses, storeStatuses := r.foundry.Reconcile(ctx, desired.Agents, desired.MemoryStores)
	hb := r.heartbeat(statuses, storeStatuses)

	// Kubernetes/GitOps layer: bootstrap Argo CD + stamp the tenant's Helm
	// deployments into its cluster, and report cluster + app status.
	clusterPhase := "disabled"
	if r.cluster != nil {
		cs, appStatuses := r.cluster.Reconcile(ctx, desired.Applications, desired.IngressAuth)
		hb.Cluster = &cs
		hb.Applications = appStatuses
		clusterPhase = cs.Phase
	}

	if err := r.cp.Heartbeat(ctx, hb); err != nil {
		slog.Warn("heartbeat failed", "err", err)
		return
	}
	slog.Info("reconciled",
		"agents", len(desired.Agents), "agentsLive", countLive(statuses),
		"stores", len(desired.MemoryStores), "storesLive", countStoresLive(storeStatuses),
		"apps", len(desired.Applications), "cluster", clusterPhase)
}

func (r *Reconciler) heartbeat(statuses []shared.AgentStatus, storeStatuses []shared.MemoryStoreStatus) shared.Heartbeat {
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
		MemoryStores:       storeStatuses,
	}
}

func countLive(s []shared.AgentStatus) int {
	n := 0
	for _, a := range s {
		if a.Health == shared.StatusLive {
			n++
		}
	}
	return n
}

func countStoresLive(s []shared.MemoryStoreStatus) int {
	n := 0
	for _, ms := range s {
		if ms.Health == shared.StatusLive {
			n++
		}
	}
	return n
}
