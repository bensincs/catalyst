package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

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

	src, err := tokens.NewSource(cfg.CortexAPIScope)
	if err != nil {
		slog.Error("azure credential setup failed", "err", err)
		os.Exit(1)
	}

	slog.Info("cortex reconciler starting",
		"controlPlane", cfg.ControlPlaneURL,
		"tenant", cfg.TenantID,
		"tenantName", cfg.TenantName,
		"region", cfg.Region,
		"foundryProject", cfg.FoundryProject,
		"reconcilerIdentity", cfg.ReconcilerIdentity,
		"reconcilerVersion", cfg.ReconcilerVersion,
		"authScope", cfg.CortexAPIScope,
		"poll", cfg.PollInterval.String(),
	)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	loop.New(cfg, src).Run(ctx)
	slog.Info("cortex reconciler stopped")
}
