// Package model holds the control-plane domain types serialized to the console.
package model

import (
	"time"

	"github.com/inception42/cortex/shared"
)

type Role string

const (
	RolePlatform Role = "platform"
	RoleTenant   Role = "tenant"
)

// Identity is the authenticated caller, derived from the internal token + tenant.
type Identity struct {
	OID   string `json:"oid"`
	TID   string `json:"tid"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  Role   `json:"role"`
}

// Tenant is a customer tenant in the fleet (and, for the caller, their own).
type Tenant struct {
	ID               string     `json:"id"` // stable slug
	Name             string     `json:"name"`
	TenantID         string     `json:"tenantId"` // Entra directory (tid)
	Region           string     `json:"region"`
	Plan             string     `json:"plan"`
	Enrollment       string     `json:"enrollment"`
	Lifecycle        string     `json:"lifecycle"` // enrolling | live | degraded | suspended (derived)
	AgentCount       int        `json:"agentCount"`
	ReconcilingCount int        `json:"reconcilingCount"`
	Version          string     `json:"version"`
	LastHeartbeat    *time.Time `json:"lastHeartbeat"`
	MonthlyCalls     int64      `json:"monthlyCalls"`
	Drift            int        `json:"drift"`

	// Install / identity — populated for a single tenant's context view.
	SubscriptionID     string  `json:"subscriptionId,omitempty"`
	ReconcilerIdentity string  `json:"reconcilerIdentity,omitempty"`
	FoundryProject     string  `json:"foundryProject,omitempty"`
	ReconcilerVersion  string  `json:"reconcilerVersion,omitempty"`
	InstalledAt        *string `json:"installedAt,omitempty"`
}

// Agent is an enabled agent running in a tenant.
type Agent struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Type           string                 `json:"type"` // prompt | hosted
	Version        string                 `json:"version"`        // actual — what the reconciler has converged to
	DesiredVersion string                 `json:"desiredVersion"` // desired — latest catalog version for its channel
	Drift          bool                   `json:"drift"`          // desired ≠ actual (a reconcile is pending/underway)
	Channel        string                 `json:"channel"`
	Model          string                 `json:"model"`
	Definition     shared.AgentDefinition `json:"definition"`
	Health         string                 `json:"health"`
	PublishTo      []string               `json:"publishTo"`
	Calls30d       int64                  `json:"calls30d"`
	Note           string                 `json:"note,omitempty"`
}

type FleetStats struct {
	Tenants       int    `json:"tenants"`
	Bound         int    `json:"bound"`
	Agents        int    `json:"agents"`
	CallsMonth    int64  `json:"callsMonth"`
	LatestVersion string `json:"latestVersion"`
	OnLatest      int    `json:"onLatest"`
}

type FleetResponse struct {
	Stats   FleetStats `json:"stats"`
	Tenants []Tenant   `json:"tenants"`
}

type TenantContextResponse struct {
	Tenant Tenant  `json:"tenant"`
	Agents []Agent `json:"agents"`
}

type MeResponse struct {
	Identity
	// Tenant is the caller's own tenant when Role == tenant (nil for platform).
	Tenant *Tenant `json:"tenant"`
}

// CatalogVersion is a published version of a catalog agent.
type CatalogVersion struct {
	Version        string                 `json:"version"`
	Channel        string                 `json:"channel"`
	Notes          string                 `json:"notes,omitempty"`
	RolloutPercent int                    `json:"rolloutPercent"`
	Definition     shared.AgentDefinition `json:"definition"`
	CreatedAt      time.Time              `json:"createdAt"`
}

// CatalogAgent is a publisher-authored agent definition (+ its versions).
type CatalogAgent struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	Description   string           `json:"description"`
	Type          string           `json:"type"` // prompt | hosted (immutable)
	Model         string           `json:"model"`
	LatestVersion string           `json:"latestVersion"`
	Versions      []CatalogVersion `json:"versions"`
	CreatedAt     time.Time        `json:"createdAt"`

	// Populated in the tenant view:
	Entitled bool `json:"entitled"`
	Enabled  bool `json:"enabled"`
}

// TenantRegistryRow is a fleet tenant plus its entitlements (platform view).
type TenantRegistryRow struct {
	Tenant
	EntitledAgents []string `json:"entitledAgents"`
	EntitledCount  int      `json:"entitledCount"`
}
