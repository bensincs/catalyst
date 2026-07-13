package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/inception42/cortex/control-plane/internal/auth"
	"github.com/inception42/cortex/control-plane/internal/config"
	"github.com/inception42/cortex/control-plane/internal/httpapi"
	"github.com/inception42/cortex/control-plane/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg := config.Load()
	if cfg.EntraClientID == "" || strings.HasPrefix(strings.ToLower(cfg.EntraClientID), "replace_") {
		slog.Warn("ENTRA_CLIENT_ID not set — token audience validation will reject all Entra tokens until it is")
	}
	if cfg.PlatformTenantID == "" || strings.HasPrefix(cfg.PlatformTenantID, "replace_") {
		slog.Warn("PLATFORM_TENANT_ID not set — no user will resolve to Platform Admin until it is")
	}
	if cfg.EntraClientID == "" {
		slog.Warn("ENTRA_CLIENT_ID not set — reconciler token audience can't be validated; /recon endpoints will reject all callers")
	}

	ctx := context.Background()
	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connect failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		slog.Error("migrate failed", "err", err)
		os.Exit(1)
	}
	if cfg.SeedDemo {
		if err := st.Seed(ctx); err != nil {
			slog.Error("seed failed", "err", err)
			os.Exit(1)
		}
		slog.Info("database ready (migrated + demo seed applied)")
	} else {
		slog.Info("database ready (migrated; no demo seed — set SEED_DEMO=true to load it)")
	}

	// One JWKS cache, shared by the user (delegated) and reconciler (app) token
	// validators — both trust the same Entra signing keys.
	keys := auth.NewJWKS(cfg.EntraJWKSURL)
	authn := auth.New(
		keys,
		cfg.EntraClientID,
		cfg.EntraExtraAud,
		cfg.EntraRequiredScp,
		cfg.PlatformTenantID,
		cfg.EntraIssuerHost,
	)
	recon := auth.NewRecon(
		keys,
		cfg.EntraClientID,
		cfg.EntraExtraAud,
		cfg.EntraIssuerHost,
	)
	srv := httpapi.NewServer(st, authn, recon, cfg.CORSOrigin, cfg.EntraClientID, cfg.EntraIssuerHost)

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("control-plane API listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
}
