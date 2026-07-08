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

// DesiredState is what a tenant's reconciler should converge to.
type DesiredState struct {
	TenantID string         `json:"tenantId"`
	Agents   []DesiredAgent `json:"agents"`
	// MemoryStores are the stores enabled in this tenant (explicitly, or because
	// an enabled agent references one), with their typed definitions — so the
	// reconciler provisions each as a Foundry memory_store and binds agents to it.
	MemoryStores []DesiredMemoryStore `json:"memoryStores,omitempty"`
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
}
