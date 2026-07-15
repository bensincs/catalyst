package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/inception42/cortex/reconciler/internal/cluster"
	"github.com/inception42/cortex/reconciler/internal/config"
	"github.com/inception42/cortex/reconciler/internal/loop"
	"github.com/inception42/cortex/reconciler/internal/tokens"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg := config.Load()
	if missing := cfg.Missing(); len(missing) > 0 {
		slog.Error("missing required configuration — set every value explicitly", "missing", strings.Join(missing, ", "))
		os.Exit(1)
	}

	cred, err := tokens.NewCredential()
	if err != nil {
		slog.Error("azure credential setup failed", "err", err)
		os.Exit(1)
	}
	// One credential, two scopes: the Cortex control-plane API and the in-tenant
	// Foundry Agent Service.
	apiSrc := tokens.SourceFor(cred, cfg.CortexAPIScope)
	foundrySrc := tokens.SourceFor(cred, cfg.FoundryScope)

	// Cluster/GitOps is opt-in: when enabled, the reconciler bootstraps Argo CD
	// into the tenant's AKS cluster and stamps its Helm deployments in.
	var clusterClient *cluster.Client
	if cfg.ClusterEnabled {
		clusterClient = cluster.New(cred, cluster.Options{
			SubscriptionID:           cfg.SubscriptionID,
			ResourceGroup:            cfg.ClusterResourceGroup,
			ClusterName:              cfg.ClusterName,
			ArgoVersion:              cfg.ArgoCDVersion,
			IngressTLSCredentialName: cfg.IngressTLSCredentialName,
		})
	}

	slog.Info("cortex reconciler starting",
		"controlPlane", cfg.ControlPlaneURL,
		"tenant", cfg.TenantID,
		"tenantName", cfg.TenantName,
		"region", cfg.Region,
		"foundryProject", cfg.FoundryProject,
		"foundryEndpoint", cfg.FoundryEndpoint,
		"foundryApiVersion", cfg.FoundryAPIVersion,
		"reconcilerIdentity", cfg.ReconcilerIdentity,
		"reconcilerVersion", cfg.ReconcilerVersion,
		"authScope", cfg.CortexAPIScope,
		"clusterEnabled", cfg.ClusterEnabled,
		"cluster", cfg.ClusterName,
		"poll", cfg.PollInterval.String(),
	)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	loop.New(cfg, apiSrc, foundrySrc, clusterClient).Run(ctx)
	slog.Info("cortex reconciler stopped")
}
