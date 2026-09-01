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

// WireLink maps one output of a dependency into a Helm values path on the
// dependent application, so the chart is configured from what it depends on. The
// source is any of the app's dependencies: an infrastructure entity (its resolved
// Bicep outputs), a dependency application (derived: name / namespace /
// serviceHost), or a dependency agent (derived: agentId / name).
type WireLink struct {
	SourceKind string `json:"sourceKind"` // infrastructure | application | agent
	SourceID   string `json:"sourceId"`   // id of the depended-on entity
	Output     string `json:"output"`     // the source's output name
	HelmPath   string `json:"helmPath"`   // dotted Helm values path, e.g. database.host
}

// DesiredApplication is a Helm deployment a tenant wants running in its cluster
// (control plane → reconciler). The reconciler just stamps an Argo CD Application
// (Helm source) — ordered by Wave so dependencies converge first. Any Azure
// infrastructure the app depends on is provisioned by the control plane (via
// Lighthouse) and its outputs are already merged into Values before the app is
// served here.
type DesiredApplication struct {
	ID             string `json:"id"`
	Name           string `json:"name"`           // Argo Application name (also the release)
	Namespace      string `json:"namespace"`      // destination namespace in the cluster
	RepoURL        string `json:"repoURL"`        // Helm repo (https) or OCI registry (oci://)
	Chart          string `json:"chart"`          // chart name
	TargetRevision string `json:"targetRevision"` // chart version
	Values         string `json:"values,omitempty"`
	// ExposeService, when set, is the in-cluster Service the app publishes that the
	// gateway Ingress should route to (charts often name it <release>-<chart>, so
	// it's declared explicitly rather than guessed). Empty ⇒ the app is
	// cluster-internal and no Ingress is created. ExposePort is the Service port
	// (default 80).
	ExposeService string `json:"exposeService,omitempty"`
	ExposePort    int    `json:"exposePort,omitempty"`

	// Hostname is the label this app publishes under, within the tenant's
	// delegated domain: <Hostname>.<IngressConfig.AppsDomain>. Empty ⇒ the app
	// id is used. Ignored when ExposeService is empty.
	Hostname string `json:"hostname,omitempty"`
	// AuthRequired puts the app behind an OIDC login at the edge. The gateway
	// routes to an oauth2-proxy in front of the app rather than to the app.
	AuthRequired bool `json:"authRequired,omitempty"`
	// OIDCScope is the scope on the tenant's app registration this app needs, so
	// the token issued for one app isn't automatically good for another.
	OIDCScope string `json:"oidcScope,omitempty"`
	// DependsOn are ids of other applications that must converge first; Wave is
	// the derived Argo sync-wave (0 = no deps) that enforces the order. (Only
	// app→app edges gate cluster ordering; infra/agent deps are gated earlier,
	// control-plane-side, by holding the app until they're ready/live.)
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

	// Ingress carries everything the cluster needs to publish apps on the
	// tenant's own domain: the domain itself, the wildcard certificate the
	// control plane obtained for it, and the customer's OIDC application.
	// nil ⇒ nothing is published (no domain configured yet).
	Ingress *IngressConfig `json:"ingress,omitempty"`

	// Registry is the tenant's pull access to the platform registry, where
	// private charts and images are cached. It travels on the sync rather than
	// in the footprint because the credential rotates: baking it into the
	// deployment meant a rotation could only be delivered by re-stamping the
	// footprint, and until then the cluster held one the registry no longer
	// accepted. nil ⇒ no platform registry, and public artifacts still pull.
	Registry *RegistryAuth `json:"registry,omitempty"`
}

// RegistryAuth is pull access to the platform registry for one tenant. The token
// is registry-scoped rather than an Entra identity, which is what lets a cluster
// in the customer's own directory use it at all.
type RegistryAuth struct {
	LoginServer string `json:"loginServer"`
	Username    string `json:"username"`
	Password    string `json:"password"`
}

// Configured reports whether the credential is complete enough to use.
func (r *RegistryAuth) Configured() bool {
	return r != nil && r.LoginServer != "" && r.Username != "" && r.Password != ""
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

	// Publishing state, reported by the reconciler because it owns the zone.
	// DNSNameservers is what the customer must set at their registrar;
	// DNSState is '' | pending | verified | failed.
	DNSState       string   `json:"dnsState,omitempty"`
	DNSDetail      string   `json:"dnsDetail,omitempty"`
	DNSNameservers []string `json:"dnsNameservers,omitempty"`
	// TLSExpiresAt is the wildcard certificate's expiry, RFC3339. Empty ⇒ none
	// held yet, so the gateway serves HTTP only and auth-required apps stay shut.
	TLSExpiresAt string `json:"tlsExpiresAt,omitempty"`
	NodeCount    int    `json:"nodeCount,omitempty"`
	Detail       string `json:"detail,omitempty"` // human-readable note when not ready
}

// ApplicationStatus is the actual state of one Argo CD Application the reconciler
// stamped into the cluster (reconciler → control plane). SyncStatus/HealthStatus
// mirror Argo's own vocabulary (Synced/OutOfSync; Healthy/Progressing/Degraded).
type ApplicationStatus struct {
	ID           string `json:"id"`
	SyncStatus   string `json:"syncStatus"`   // Synced | OutOfSync | Unknown | pending
	HealthStatus string `json:"healthStatus"` // Healthy | Progressing | Degraded | Missing | pending
	// Detail is why the app is unhealthy or out of sync — Argo's own message
	// (a failed chart pull, a rejected manifest, a degraded resource). Empty
	// when healthy. Without it the console can only show "Degraded" and the
	// reason is reachable only with cluster access.
	Detail string `json:"detail,omitempty"`
}

// IngressConfig is the tenant's published-apps INTENT, set by a platform admin
// and handed to the reconciler. It carries no certificate: the reconciler owns
// the DNS zone — which lives in the tenant's own subscription — and therefore
// obtains the wildcard itself over ACME DNS-01.
//
// That placement is the whole point. The reconciler's managed identity and the
// zone are in the same Entra directory, so the footprint can grant DNS Zone
// Contributor on it; a zone in the platform's subscription could not be granted
// to a customer-directory identity at all. No certificate or DNS credential ever
// leaves the tenant's subscription.
//
// OIDCClientSecret is a secret. It travels over the authenticated TLS sync
// channel and must never be logged.
type IngressConfig struct {
	// AppsDomain is the zone the tenant delegates, e.g. "apps.contoso.com". Apps
	// are published at <app hostname>.<AppsDomain>.
	AppsDomain string `json:"appsDomain"`

	// The customer's OIDC application. Empty ⇒ no app can require auth.
	OIDCIssuer       string `json:"oidcIssuer,omitempty"`
	OIDCClientID     string `json:"oidcClientId,omitempty"`
	OIDCClientSecret string `json:"oidcClientSecret,omitempty"`
}

// Configured reports whether a domain has been set; without one nothing is
// published, which is the designed state rather than a failure.
func (c *IngressConfig) Configured() bool {
	return c != nil && c.AppsDomain != ""
}

// OIDCConfigured reports whether an OIDC application is available to put apps
// behind. A certificate is also required at the point of use, since the OAuth
// callback must be HTTPS — the reconciler checks that against the cert it holds.
func (c *IngressConfig) OIDCConfigured() bool {
	return c.Configured() && c.OIDCIssuer != "" && c.OIDCClientID != "" && c.OIDCClientSecret != ""
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
