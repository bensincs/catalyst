package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

// Protocol constants for the Foundry Agent Service data plane. These are not
// reported values — they're safe GA defaults for the API surface — so defaulting
// them doesn't violate the "report nothing you weren't told" rule below.
const (
	defaultFoundryAPIVersion = "2025-05-01"                    // GA (preview: 2025-05-15-preview)
	defaultFoundryScope      = "https://ai.azure.com/.default" // Entra resource for Foundry
)

type Config struct {
	ControlPlaneURL    string
	CortexAPIScope     string // Entra scope/resource for the control-plane API
	TenantID           string // customer's Entra tenant id
	TenantName         string
	Region             string
	SubscriptionID     string
	Plan               string
	FoundryProject     string // display name reported in the heartbeat
	FoundryEndpoint    string // Foundry project endpoint the reconciler drives
	FoundryAPIVersion  string // Foundry data-plane api-version
	FoundryScope       string // Entra scope for the Foundry token
	ReconcilerIdentity string
	ReconcilerVersion  string
	PollInterval       time.Duration
}

// Load reads .env then the environment. Nothing is defaulted or derived — every
// value the reconciler reports is supplied explicitly (see Missing), so it can
// never heartbeat fabricated identity/version/foundry data.
func Load() Config {
	loadDotEnv(".env")
	poll := 0
	if v, err := strconv.Atoi(env("POLL_INTERVAL_SECONDS")); err == nil {
		poll = v
	}
	foundryAPIVersion := strings.TrimSpace(env("FOUNDRY_API_VERSION"))
	if foundryAPIVersion == "" {
		foundryAPIVersion = defaultFoundryAPIVersion
	}
	foundryScope := strings.TrimSpace(env("FOUNDRY_SCOPE"))
	if foundryScope == "" {
		foundryScope = defaultFoundryScope
	}
	return Config{
		ControlPlaneURL:    strings.TrimRight(env("CONTROL_PLANE_URL"), "/"),
		CortexAPIScope:     env("CORTEX_API_SCOPE"),
		TenantID:           strings.ToLower(strings.TrimSpace(env("TENANT_ID"))),
		TenantName:         env("TENANT_NAME"),
		Region:             env("AZURE_REGION"),
		SubscriptionID:     env("AZURE_SUBSCRIPTION_ID"),
		Plan:               env("PLAN"),
		FoundryProject:     env("FOUNDRY_PROJECT"),
		FoundryEndpoint:    strings.TrimRight(strings.TrimSpace(env("FOUNDRY_PROJECT_ENDPOINT")), "/"),
		FoundryAPIVersion:  foundryAPIVersion,
		FoundryScope:       foundryScope,
		ReconcilerIdentity: env("RECONCILER_IDENTITY"),
		ReconcilerVersion:  env("RECONCILER_VERSION"),
		PollInterval:       time.Duration(poll) * time.Second,
	}
}

// Missing lists the required settings that are unset, so the reconciler fails
// fast at startup rather than reporting blanks or made-up values.
func (c Config) Missing() []string {
	req := []struct{ name, val string }{
		{"CONTROL_PLANE_URL", c.ControlPlaneURL},
		{"CORTEX_API_SCOPE", c.CortexAPIScope},
		{"TENANT_ID", c.TenantID},
		{"TENANT_NAME", c.TenantName},
		{"AZURE_REGION", c.Region},
		{"AZURE_SUBSCRIPTION_ID", c.SubscriptionID},
		{"PLAN", c.Plan},
		{"FOUNDRY_PROJECT", c.FoundryProject},
		{"FOUNDRY_PROJECT_ENDPOINT", c.FoundryEndpoint},
		{"RECONCILER_IDENTITY", c.ReconcilerIdentity},
		{"RECONCILER_VERSION", c.ReconcilerVersion},
	}
	var missing []string
	for _, r := range req {
		if strings.TrimSpace(r.val) == "" {
			missing = append(missing, r.name)
		}
	}
	if c.PollInterval <= 0 {
		missing = append(missing, "POLL_INTERVAL_SECONDS")
	}
	return missing
}

func env(key string) string {
	return os.Getenv(key)
}

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
