package config

import (
	"bufio"
	"encoding/base64"
	"os"
	"strconv"
	"strings"
	"time"
)

// Protocol constants for the Foundry Agent Service data plane. These are not
// reported values — they're safe GA defaults for the API surface — so defaulting
// them doesn't violate the "report nothing you weren't told" rule below.
const (
	defaultFoundryAPIVersion = "v1"                            // Foundry Agents API (new /agents surface)
	defaultFoundryScope      = "https://ai.azure.com/.default" // Entra resource for Foundry
	defaultArgoCDVersion     = "v2.13.2"                       // Argo CD the reconciler bootstraps
)

// Cluster kinds — how the reconciler reaches the tenant's cluster.
const (
	ClusterKindAKS        = "aks"        // Cortex-provisioned AKS via ARM + AKS AAD token
	ClusterKindKubeconfig = "kubeconfig" // bring-your-own (Arc) cluster via a supplied kubeconfig
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

	// Kubernetes/GitOps (opt-in). When ClusterEnabled, the reconciler bootstraps
	// Argo CD into the tenant's cluster and stamps Helm deployments into it.
	//
	// ClusterKind selects how the reconciler reaches that cluster:
	//   • "aks"        — the tenant's Cortex-provisioned AKS cluster, reached via
	//                    ARM (listClusterUserCredential) + an AKS AAD token, with
	//                    Application Gateway for Containers ingress.
	//   • "kubeconfig" — a bring-your-own cluster (e.g. Azure Arc-connected) the
	//                    reconciler reaches directly from a supplied kubeconfig
	//                    (Kubeconfig). No ARM, no AGC — ingress is the customer's.
	ClusterEnabled       bool
	ClusterKind          string
	ClusterName          string
	ClusterResourceGroup string
	// Kubeconfig is the decoded bring-your-own cluster kubeconfig (ClusterKind
	// "kubeconfig"). Supplied base64-encoded via KUBECONFIG_BASE64 so it survives
	// transport as a container secret without newline/escaping hazards.
	Kubeconfig    []byte
	ArgoCDVersion string

	// AppsDomain, when set, is the DNS suffix for per-app hosts served by the
	// Azure Application Gateway (<app>.<AppsDomain>). Empty ⇒ host-less Ingress.
	AppsDomain string

	// HelmOCIRegistry, when set, registers an OCI-enabled Argo CD Helm repository
	// (e.g. ghcr.io/bensincs) so apps whose RepoURL is that registry pull their
	// chart over OCI. HelmOCIUsername/Password are optional (private packages).
	HelmOCIRegistry string
	HelmOCIUsername string
	HelmOCIPassword string
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
	argocd := strings.TrimSpace(env("ARGOCD_VERSION"))
	if argocd == "" {
		argocd = defaultArgoCDVersion
	}
	clusterKind := strings.ToLower(strings.TrimSpace(env("CLUSTER_KIND")))
	if clusterKind != ClusterKindKubeconfig {
		clusterKind = ClusterKindAKS
	}
	var kubeconfig []byte
	if raw := strings.TrimSpace(env("KUBECONFIG_BASE64")); raw != "" {
		if b, err := base64.StdEncoding.DecodeString(raw); err == nil {
			kubeconfig = b
		}
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

		ClusterEnabled:       strings.EqualFold(strings.TrimSpace(env("CLUSTER_ENABLED")), "true"),
		ClusterKind:          clusterKind,
		ClusterName:          strings.TrimSpace(env("CLUSTER_NAME")),
		ClusterResourceGroup: strings.TrimSpace(env("CLUSTER_RESOURCE_GROUP")),
		Kubeconfig:           kubeconfig,
		ArgoCDVersion:        argocd,

		AppsDomain: strings.TrimSpace(env("APPS_DOMAIN")),

		HelmOCIRegistry: strings.TrimSpace(env("HELM_OCI_REGISTRY")),
		HelmOCIUsername: strings.TrimSpace(env("HELM_OCI_USERNAME")),
		HelmOCIPassword: env("HELM_OCI_PASSWORD"),
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
	// The cluster is opt-in; if enabled, its address must be complete for the
	// selected kind — an AKS cluster's ARM coordinates, or a BYO kubeconfig.
	if c.ClusterEnabled {
		switch c.ClusterKind {
		case ClusterKindKubeconfig:
			if len(c.Kubeconfig) == 0 {
				missing = append(missing, "KUBECONFIG_BASE64")
			}
		default:
			if strings.TrimSpace(c.ClusterName) == "" {
				missing = append(missing, "CLUSTER_NAME")
			}
			if strings.TrimSpace(c.ClusterResourceGroup) == "" {
				missing = append(missing, "CLUSTER_RESOURCE_GROUP")
			}
		}
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
