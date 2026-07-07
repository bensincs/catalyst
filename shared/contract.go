// Package shared holds types used by both the control plane and the in-tenant
// reconciler — chiefly the sync (desired state) and heartbeat (actual state)
// wire contract. The reconciler authenticates with its own Entra token, so no
// shared auth header is part of the contract.
package shared

import "encoding/json"

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
	// memory-store catalog). The reconciler resolves it to the store's config and
	// injects it into the Foundry agent's definition.memory.
	MemoryStore string `json:"memoryStore,omitempty"`
	// hosted
	Image    string            `json:"image,omitempty"`
	Endpoint string            `json:"endpoint,omitempty"`
	CPU      string            `json:"cpu,omitempty"`
	Memory   string            `json:"memory,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
}

// DesiredMemoryStore is a memory store a tenant's reconciler should provision and
// make available to agents (control plane → reconciler). Config is the Foundry
// memory definition, forwarded verbatim.
type DesiredMemoryStore struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config,omitempty"`
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
	// MemoryStores are the stores referenced by the desired agents, with their
	// configs, so the reconciler can bind each agent to its store's memory.
	MemoryStores []DesiredMemoryStore `json:"memoryStores,omitempty"`
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
