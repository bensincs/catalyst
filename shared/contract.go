// Package shared holds types used by both the control plane and the in-tenant
// reconciler — chiefly the sync (desired state) and heartbeat (actual state)
// wire contract. The reconciler authenticates with its own Entra token, so no
// shared auth header is part of the contract.
package shared

// AgentType is how an agent is realized in Foundry (see AGENT-MODEL.md).
type AgentType string

const (
	// AgentPrompt is a declarative agent: model + instructions + tools + knowledge.
	AgentPrompt AgentType = "prompt"
	// AgentHosted is a bring-your-own-code container agent.
	AgentHosted AgentType = "hosted"
)

// AgentDefinition is the versioned substance of an agent, authored by the
// publisher. Which fields apply is decided by the agent's Type: prompt agents
// use Instructions/Tools/Knowledge/Temperature; hosted agents use
// Image/Endpoint/CPU/Memory/Env.
type AgentDefinition struct {
	// prompt
	Instructions string   `json:"instructions,omitempty"`
	Tools        []string `json:"tools,omitempty"`
	Knowledge    []string `json:"knowledge,omitempty"`
	Temperature  *float64 `json:"temperature,omitempty"`
	TopP         *float64 `json:"topP,omitempty"`
	// MemoryStore is the id of a memory store this agent connects to (see the
	// memory-store catalog). The reconciler resolves it to the store's Foundry
	// name and binds the agent by adding a memory_search_preview tool.
	MemoryStore string `json:"memoryStore,omitempty"`
	// hosted
	Image    string            `json:"image,omitempty"`
	Endpoint string            `json:"endpoint,omitempty"`
	CPU      string            `json:"cpu,omitempty"`
	Memory   string            `json:"memory,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

// MemoryStoreDefinition is the typed, real Foundry memory-store definition
// (kind "default"): the models that process memory plus which memory kinds are
// extracted. It mirrors the Azure AI Projects MemoryStoreDefaultDefinition /
// MemoryStoreDefaultOptions schema. The reconciler maps these fields onto the
// Foundry POST /memory_stores body (snake_case), so the store is modeled — never
// forwarded as an opaque JSON blob.
type MemoryStoreDefinition struct {
	// ChatModel is the chat-completion model deployment used to process memory.
	ChatModel string `json:"chatModel"`
	// EmbeddingModel is the embedding model deployment used to index memory.
	EmbeddingModel string `json:"embeddingModel"`
	// UserProfileEnabled extracts and stores durable facts about the user.
	UserProfileEnabled bool `json:"userProfileEnabled"`
	// UserProfileDetails optionally narrows which categories of user-profile
	// information to extract (free text, e.g. "preferences, timezone").
	UserProfileDetails string `json:"userProfileDetails,omitempty"`
	// ChatSummaryEnabled extracts and stores rolling conversation summaries.
	ChatSummaryEnabled bool `json:"chatSummaryEnabled"`
	// ProceduralMemoryEnabled extracts and stores learned procedures/preferences.
	ProceduralMemoryEnabled bool `json:"proceduralMemoryEnabled"`
	// TTLSeconds is how long memories live before expiring; 0 = never expire.
	TTLSeconds int `json:"ttlSeconds"`
}

// DesiredMemoryStore is a memory store a tenant's reconciler should provision as
// a first-class Foundry memory_store resource (control plane → reconciler), and
// bind referencing agents to. Definition is the typed store definition.
type DesiredMemoryStore struct {
	ID         string                `json:"id"`
	Name       string                `json:"name"`
	Definition MemoryStoreDefinition `json:"definition"`
}

// DesiredAgent is one agent a tenant wants running (control plane → reconciler).
type DesiredAgent struct {
	AgentID string    `json:"agentId"`
	Name    string    `json:"name"`
	Type    AgentType `json:"type"`
	Version string    `json:"version"`
	Model   string    `json:"model"`
	Channel string    `json:"channel"`
	// Definition is the versioned substance the reconciler provisions.
	Definition AgentDefinition `json:"definition"`
	PublishTo  []string        `json:"publishTo"`
}

// WireLink maps one Bicep deployment output to a Helm values path, so the chart
// is configured with the address/secret of the Azure infra that backs it.
type WireLink struct {
	BicepOutput string `json:"bicepOutput"` // name of a Bicep `output`
	HelmPath    string `json:"helmPath"`    // dotted Helm values path, e.g. database.host
}

// DesiredApplication is a deployment a tenant wants running in its cluster
// (control plane → reconciler). It is realized in two steps: the reconciler
// provisions the app's Bicep infra (Azure) if any, wires those outputs into the
// Helm values, then stamps an Argo CD Application (Helm source) — ordered by Wave
// so dependencies converge first.
type DesiredApplication struct {
	ID             string `json:"id"`
	Name           string `json:"name"`           // Argo Application name (also the release)
	Namespace      string `json:"namespace"`      // destination namespace in the cluster
	RepoURL        string `json:"repoURL"`        // Helm repo (https) or OCI registry (oci://)
	Chart          string `json:"chart"`          // chart name
	TargetRevision string `json:"targetRevision"` // chart version
	Values         string `json:"values,omitempty"`
	// Azure infra + wiring. InfraTemplate is the compiled ARM template (from the
	// deployment's Bicep); the reconciler provisions it before the chart and
	// injects its outputs into the Helm values per Wiring.
	InfraTemplate string     `json:"infraTemplate,omitempty"`
	Wiring        []WireLink `json:"wiring,omitempty"`
	// DependsOn are ids of other entities (apps/agents) that must converge first;
	// Wave is the derived Argo sync-wave (0 = no deps) that enforces the order.
	DependsOn []string `json:"dependsOn,omitempty"`
	Wave      int      `json:"wave,omitempty"`
}

// IngressJWTRule is one accepted token issuer for the cluster's ingress gateway:
// a fully-formed Entra endpoint (so the reconciler stays cloud-agnostic) whose
// tokens must be addressed to one of Audiences. The control plane emits one rule
// per token version (v2 + v1) for the requesting tenant only.
type IngressJWTRule struct {
	Issuer    string   `json:"issuer"`              // e.g. https://login.microsoftonline.com/{tid}/v2.0
	JWKSURI   string   `json:"jwksUri"`             // Entra signing-key endpoint for that issuer
	Audiences []string `json:"audiences,omitempty"` // accepted aud values (the Cortex app registration)
}

// IngressAuth makes the tenant's ingress gateway require an Entra token from THIS
// tenant's directory, addressed to the (multi-tenant) Cortex app registration.
// Because the issuers are pinned to the tenant's own tid, a user from any other
// tenant consented to the same app is rejected — "the app, but just this tenant".
type IngressAuth struct {
	Rules []IngressJWTRule `json:"rules"`
}

// DesiredState is what a tenant's reconciler should converge to.
type DesiredState struct {
	TenantID string         `json:"tenantId"`
	Agents   []DesiredAgent `json:"agents"`
	// MemoryStores are the stores enabled in this tenant (explicitly, or because
	// an enabled agent references one), with their typed definitions — so the
	// reconciler provisions each as a Foundry memory_store and binds agents to it.
	MemoryStores []DesiredMemoryStore `json:"memoryStores,omitempty"`
	// Applications are the Helm deployments the reconciler should stamp into the
	// tenant's cluster as Argo CD Applications.
	Applications []DesiredApplication `json:"applications,omitempty"`
	// IngressAuth pins the cluster's ingress gateway to accept only this tenant's
	// Entra tokens (nil ⇒ the control plane has no app registration configured).
	IngressAuth *IngressAuth `json:"ingressAuth,omitempty"`
}

// Lifecycle status values shared by agents and memory stores (reconciler →
// control plane). A resource is `reconciling` while being provisioned into the
// tenant's Foundry project, `live` once it exists and has converged, and
// `blocked` if the reconciler couldn't realize it.
const (
	StatusReconciling = "reconciling"
	StatusLive        = "live"
	StatusBlocked     = "blocked"
)

// AgentStatus is the actual state of one agent (reconciler → control plane).
type AgentStatus struct {
	AgentID  string `json:"agentId"`
	Version  string `json:"version"`
	Health   string `json:"health"` // live | reconciling | blocked
	Calls30d int64  `json:"calls30d"`
}

// MemoryStoreStatus is the actual state of one memory store the reconciler
// provisions in the tenant's Foundry project (reconciler → control plane), so
// the control plane can show the same reconciling→live lifecycle stores have as
// agents.
type MemoryStoreStatus struct {
	StoreID string `json:"storeId"`
	Health  string `json:"health"` // live | reconciling | blocked
}

// Cluster lifecycle phases (Cluster.Phase). The AKS cluster is provisioned by the
// managed-app Bicep; the reconciler bootstraps Argo CD into it and reports here.
const (
	ClusterProvisioning = "provisioning" // reachable but Argo CD not yet installed
	ClusterReady        = "ready"        // Argo CD installed + reconciling
	ClusterUnreachable  = "unreachable"  // couldn't reach / authenticate to the cluster
)

// ClusterStatus is the actual state of a tenant's Kubernetes cluster + its GitOps
// bootstrap (reconciler → control plane).
type ClusterStatus struct {
	Name             string `json:"name"`
	Phase            string `json:"phase"` // provisioning | ready | unreachable
	KubernetesVer    string `json:"kubernetesVersion,omitempty"`
	ArgoInstalled    bool   `json:"argoInstalled"`
	IngressInstalled bool   `json:"ingressInstalled"`        // Envoy ingress present
	GatewayIP        string `json:"gatewayIP,omitempty"`     // public ingress address (LB IP/hostname)
	IngressIssuer    string `json:"ingressIssuer,omitempty"` // Entra issuer the ingress enforces ("" ⇒ closed)
	NodeCount        int    `json:"nodeCount,omitempty"`
	Detail           string `json:"detail,omitempty"` // human-readable note when not ready
}

// ApplicationStatus is the actual state of one Argo CD Application the reconciler
// stamped into the cluster (reconciler → control plane). SyncStatus/HealthStatus
// mirror Argo's own vocabulary (Synced/OutOfSync; Healthy/Progressing/Degraded).
type ApplicationStatus struct {
	ID           string `json:"id"`
	SyncStatus   string `json:"syncStatus"`           // Synced | OutOfSync | Unknown | pending
	HealthStatus string `json:"healthStatus"`         // Healthy | Progressing | Degraded | Missing | pending
	InfraState   string `json:"infraState,omitempty"` // "" | provisioning | ready | failed (Bicep infra)
}

// Heartbeat is the reconciler's periodic report: the in-tenant install identity
// (subscription, region, reconciler identity, Foundry project — the authoritative
// source for these) plus the actual state of every managed agent and memory store.
type Heartbeat struct {
	TenantID           string              `json:"tenantId"`
	TenantName         string              `json:"tenantName"`
	Region             string              `json:"region"`
	Plan               string              `json:"plan,omitempty"`
	SubscriptionID     string              `json:"subscriptionId"`
	ReconcilerIdentity string              `json:"reconcilerIdentity"`
	FoundryProject     string              `json:"foundryProject"`
	ReconcilerVersion  string              `json:"reconcilerVersion"`
	Agents             []AgentStatus       `json:"agents"`
	MemoryStores       []MemoryStoreStatus `json:"memoryStores,omitempty"`
	// Cluster + Applications report the tenant's Kubernetes/GitOps layer.
	Cluster      *ClusterStatus      `json:"cluster,omitempty"`
	Applications []ApplicationStatus `json:"applications,omitempty"`
}
