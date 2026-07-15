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

	// Platform Azure service principal — used to provision each deployment's Bicep
	// infra cross-tenant into the customer's Lighthouse-delegated resource group.
	// When unset, infra provisioning is disabled (deployments with infra stay held).
	AzureTenantID      string
	AzureClientID      string
	AzureClientSecret  string
	InfraResourceGroup string // delegated RG the control plane deploys infra into
	InfraPollSeconds   int
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

		AzureTenantID:      env("AZURE_TENANT_ID", ""),
		AzureClientID:      env("AZURE_CLIENT_ID", ""),
		AzureClientSecret:  env("AZURE_CLIENT_SECRET", ""),
		InfraResourceGroup: env("INFRA_RESOURCE_GROUP", "cortex-infra"),
		InfraPollSeconds:   envInt("INFRA_POLL_SECONDS", 30),
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
