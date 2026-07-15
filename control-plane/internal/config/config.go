package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port             string
	DatabaseURL      string
	SeedDemo         bool
	EntraClientID    string
	EntraExtraAud    string
	EntraRequiredScp string
	EntraJWKSURL     string
	EntraIssuerHost  string
	PlatformTenantID string
	CORSOrigin       string

	// Cross-tenant provisioning (Azure Lighthouse). The control plane authenticates
	// with its own managed identity (DefaultAzureCredential) — no secret held here.
	// Off unless CROSS_TENANT_PROVISIONING=true.
	CrossTenantProvisioning bool
	InfraResourceGroup      string // delegated RG the control plane deploys app infra into
	FootprintRG             string // RG the control plane deploys the tenant footprint into
	InfraRegion             string // region for created resource groups
	InfraPollSeconds        int

	// Footprint parameters injected into each tenant's reconciler.
	ControlPlanePublicURL string // the reconciler → control plane base URL
	CortexAPIScope        string // Entra scope for the control-plane API
	ReconcilerImage       string // reconciler container image
}

// Load reads .env (if present) into the process env, then builds Config.
func Load() Config {
	loadDotEnv(".env")
	seed := env("SEED_DEMO", "")
	return Config{
		Port:             env("PORT", "8080"),
		DatabaseURL:      env("DATABASE_URL", "postgres://localhost:5432/cortex?sslmode=disable"),
		SeedDemo:         strings.EqualFold(seed, "true") || seed == "1",
		EntraClientID:    env("ENTRA_CLIENT_ID", ""),
		EntraExtraAud:    env("ENTRA_API_AUDIENCE", ""),
		EntraRequiredScp: env("ENTRA_REQUIRED_SCOPE", "access_as_user"),
		EntraJWKSURL:     env("ENTRA_JWKS_URL", "https://login.microsoftonline.com/common/discovery/v2.0/keys"),
		EntraIssuerHost:  env("ENTRA_ISSUER_HOST", "https://login.microsoftonline.com/"),
		PlatformTenantID: strings.ToLower(env("PLATFORM_TENANT_ID", "")),
		CORSOrigin:       env("CORS_ORIGIN", "http://localhost:4200"),

		CrossTenantProvisioning: strings.EqualFold(strings.TrimSpace(env("CROSS_TENANT_PROVISIONING", "")), "true"),
		InfraResourceGroup:      env("INFRA_RESOURCE_GROUP", "cortex-infra"),
		FootprintRG:             env("FOOTPRINT_RESOURCE_GROUP", "cortex"),
		InfraRegion:             env("INFRA_REGION", "uksouth"),
		InfraPollSeconds:        envInt("INFRA_POLL_SECONDS", 30),

		ControlPlanePublicURL: env("CONTROL_PLANE_PUBLIC_URL", "https://api.catalyst.msft.ae"),
		CortexAPIScope:        env("CORTEX_API_SCOPE", ""),
		ReconcilerImage:       env("RECONCILER_IMAGE", "ghcr.io/inception42/cortex-reconciler:latest"),
	}
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// loadDotEnv loads KEY=VALUE lines without overriding already-set env vars.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, exists := os.LookupEnv(k); !exists {
			_ = os.Setenv(k, v)
		}
	}
}
