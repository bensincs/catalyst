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
	// hosted
	Image    string            `json:"image,omitempty"`
	Endpoint string            `json:"endpoint,omitempty"`
	CPU      string            `json:"cpu,omitempty"`
	Memory   string            `json:"memory,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
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
}

// AgentStatus is the actual state of one agent (reconciler → control plane).
type AgentStatus struct {
	AgentID  string `json:"agentId"`
	Version  string `json:"version"`
	Health   string `json:"health"` // healthy | reconciling | blocked
	Calls30d int64  `json:"calls30d"`
}

// Heartbeat is the reconciler's periodic report: the in-tenant install identity
// (subscription, region, reconciler identity, Foundry project — the authoritative
// source for these) plus the actual state of every managed agent.
type Heartbeat struct {
	TenantID           string        `json:"tenantId"`
	TenantName         string        `json:"tenantName"`
	Region             string        `json:"region"`
	Plan               string        `json:"plan,omitempty"`
	SubscriptionID     string        `json:"subscriptionId"`
	ReconcilerIdentity string        `json:"reconcilerIdentity"`
	FoundryProject     string        `json:"foundryProject"`
	ReconcilerVersion  string        `json:"reconcilerVersion"`
	Agents             []AgentStatus `json:"agents"`
}
