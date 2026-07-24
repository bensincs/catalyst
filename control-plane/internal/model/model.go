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

// HostingMode is where a tenant's Azure footprint lives.
const (
	HostingDelegated = "delegated" // customer's own subscription, via Azure Lighthouse
	HostingPlatform  = "platform"  // the platform's own subscription, a dedicated RG per tenant
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
	ID               string      `json:"id"` // stable slug
	Name             string      `json:"name"`
	TenantID         string      `json:"tenantId"` // Entra directory (tid)
	Region           string      `json:"region"`
	Plan             string      `json:"plan"`
	Enrollment       string      `json:"enrollment"`
	Lifecycle        string      `json:"lifecycle"` // enrolling | live | degraded | suspended (derived)
	Enabled          bool        `json:"enabled"`   // access gate: may sign in / run a reconciler
	Cluster          ClusterInfo `json:"cluster"`   // Kubernetes/GitOps status (reconciler-reported)
	AgentCount       int         `json:"agentCount"`
	ReconcilingCount int         `json:"reconcilingCount"`
	Version          string      `json:"version"`
	LastHeartbeat    *time.Time  `json:"lastHeartbeat"`
	MonthlyCalls     int64       `json:"monthlyCalls"`
	Drift            int         `json:"drift"`

	// Install / identity — populated for a single tenant's context view.
	SubscriptionID     string  `json:"subscriptionId,omitempty"`
	ReconcilerIdentity string  `json:"reconcilerIdentity,omitempty"`
	FoundryProject     string  `json:"foundryProject,omitempty"`
	ReconcilerVersion  string  `json:"reconcilerVersion,omitempty"`
	InstalledAt        *string `json:"installedAt,omitempty"`

	// Hosting: 'delegated' (customer subscription via Lighthouse) or 'platform'
	// (the platform's own subscription, a dedicated resource group per tenant).
	HostingMode           string `json:"hostingMode"`
	ResourceGroup         string `json:"resourceGroup,omitempty"`
	ReconcilerPrincipalID string `json:"-"` // pre-created reconciler MI oid (platform-hosted); internal
}

// Agent is an enabled agent running in a tenant.
type Agent struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	Type       string                 `json:"type"` // prompt | hosted
	Model      string                 `json:"model"`
	Definition shared.AgentDefinition `json:"definition"`
	Health     string                 `json:"health"`
	PublishTo  []string               `json:"publishTo"`
	Calls30d   int64                  `json:"calls30d"`
	Note       string                 `json:"note,omitempty"`
	// MemoryStore is the effective store this tenant's agent connects to (the
	// per-tenant override if set, else the catalog definition's memoryStore).
	MemoryStore string `json:"memoryStore,omitempty"`
}

type FleetStats struct {
	Tenants    int   `json:"tenants"`
	Bound      int   `json:"bound"`
	Agents     int   `json:"agents"`
	CallsMonth int64 `json:"callsMonth"`
}

type FleetResponse struct {
	Stats   FleetStats `json:"stats"`
	Tenants []Tenant   `json:"tenants"`
}

type TenantContextResponse struct {
	Tenant Tenant  `json:"tenant"`
	Agents []Agent `json:"agents"`
	// The tenant's ENABLED resources — enough to draw its dependency topology
	// (both on the tenant's own overview and the platform drill-in) without extra
	// round-trips.
	Infrastructure []Infrastructure `json:"infrastructure"`
	Applications   []Application    `json:"applications"`
	Stores         []MemoryStore    `json:"stores"`
}

type MeResponse struct {
	Identity
	// Tenant is the caller's primary/default tenant (nil for platform admins).
	Tenant *Tenant `json:"tenant"`
	// Tenants is every tenant the caller can access — their delegated directory
	// tenant plus any they're assigned to (memberships). Drives the console's
	// tenant switcher. Empty for platform admins (who see the whole fleet).
	Tenants []Tenant `json:"tenants,omitempty"`
}

// Membership is an explicit user → tenant assignment (platform-hosted tenants).
// A member is assigned by a principal — an email (oid bound on first sign-in) or
// an Entra object id directly.
type Membership struct {
	TenantSlug string    `json:"tenantSlug"`
	Principal  string    `json:"principal"` // the assigned identifier: an email or an oid
	Email      string    `json:"email,omitempty"`
	OID        string    `json:"oid,omitempty"`
	Role       string    `json:"role"`
	CreatedAt  time.Time `json:"createdAt"`
}

// CatalogAgent is an agent definition, authored by the platform (Owner == "") or
// by a tenant (Owner == <tenant slug>, private to it). Platform agents are granted
// to tenants via entitlements; tenant agents are private.
type CatalogAgent struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Type        string                 `json:"type"` // prompt | hosted (immutable)
	Model       string                 `json:"model"`
	Owner       string                 `json:"owner"` // "" = platform-authored; else tenant slug
	Definition  shared.AgentDefinition `json:"definition"`
	CreatedAt   time.Time              `json:"createdAt"`

	// Populated in the tenant view:
	Platform bool `json:"platform"` // platform-authored (vs tenant-owned)
	Owned    bool `json:"owned"`    // owned by the viewing tenant
	Entitled bool `json:"entitled"`
	Enabled  bool `json:"enabled"`
	// Populated in the platform view:
	OwnerName string `json:"ownerName,omitempty"` // owning tenant's display name
}

// TenantRegistryRow is a fleet tenant plus its entitlements (platform view).
type TenantRegistryRow struct {
	Tenant
	EntitledAgents         []string `json:"entitledAgents"`
	EntitledCount          int      `json:"entitledCount"`
	EntitledStores         []string `json:"entitledStores"`
	EntitledDeployments    []string `json:"entitledDeployments"`
	EntitledInfrastructure []string `json:"entitledInfrastructure"`
}

// MemoryStore is a reusable Foundry memory configuration that agents connect to.
// Platform-authored stores (Owner == "") are granted to tenants via
// entitlements; tenant-created stores (Owner == <tenant slug>) are private to
// their tenant.
type MemoryStore struct {
	ID          string                       `json:"id"`
	Name        string                       `json:"name"`
	Description string                       `json:"description"`
	Owner       string                       `json:"owner"` // "" = platform-authored; else tenant slug
	Definition  shared.MemoryStoreDefinition `json:"definition"`
	CreatedBy   string                       `json:"createdBy,omitempty"`
	CreatedAt   time.Time                    `json:"createdAt"`

	// Populated in the tenant view:
	Platform bool   `json:"platform"`         // platform-authored (vs tenant-owned)
	Owned    bool   `json:"owned"`            // owned by the viewing tenant
	Entitled bool   `json:"entitled"`         // entitled to the viewing tenant
	Enabled  bool   `json:"enabled"`          // explicitly enabled (reconciled) in the viewing tenant
	Health   string `json:"health,omitempty"` // per-tenant lifecycle: reconciling | live | blocked
	// Populated in the platform view:
	OwnerName string `json:"ownerName,omitempty"` // owning tenant's display name
}

// ClusterInfo is a tenant's Kubernetes/GitOps status, reported by the reconciler
// via the heartbeat (never client-supplied).
type ClusterInfo struct {
	Name             string `json:"name"`
	Phase            string `json:"phase"` // provisioning | ready | unreachable | "" (none)
	K8sVersion       string `json:"kubernetesVersion,omitempty"`
	ArgoInstalled    bool   `json:"argoInstalled"`
	IngressInstalled bool   `json:"ingressInstalled"`
	GatewayIP        string `json:"gatewayIP,omitempty"`
	IngressIssuer    string `json:"ingressIssuer,omitempty"`
	InfraDelegated   bool   `json:"infraDelegated"`           // control plane can reach the tenant's Lighthouse-delegated RG
	InfraDetail      string `json:"infraDetail,omitempty"`    // human note about delegation reachability
	FootprintState   string `json:"footprintState,omitempty"` // "" | provisioning | ready | failed (reconciler + Foundry)
	FootprintDetail  string `json:"footprintDetail,omitempty"`
	NodeCount        int    `json:"nodeCount"`
	Detail           string `json:"detail,omitempty"`
}

// DepKind is the kind of catalog entity a dependency edge points at.
type DepKind string

const (
	DepInfrastructure DepKind = "infrastructure"
	DepApplication    DepKind = "application"
	DepAgent          DepKind = "agent"
	DepMemoryStore    DepKind = "memory_store"
)

// Dependency is one typed edge: the owning entity depends on (Kind, ID). Allowed
// edges (enforced): infrastructure→infrastructure, application→{infrastructure,
// application, agent}, agent→memory_store.
type Dependency struct {
	Kind DepKind `json:"kind"`
	ID   string  `json:"id"`
}

// Infrastructure is an Azure/Bicep module authored as a catalog entity (platform
// Owner "" or a tenant), entitled to tenants, and enabled per tenant — then
// provisioned cross-tenant by the control plane (ARM via Lighthouse).
// Applications depend on infrastructure and wire its outputs into their values.
type Infrastructure struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	Owner        string         `json:"owner"` // "" = platform-authored; else tenant slug
	BicepModule  string         `json:"bicepModule,omitempty"`
	BicepParams  map[string]any `json:"bicepParams,omitempty"`
	ArmTemplate  string         `json:"-"` // resolved ARM template; worker-only
	BicepOutputs []string       `json:"bicepOutputs"`
	Dependencies []Dependency   `json:"dependencies"` // infrastructure → infrastructure only
	CreatedBy    string         `json:"createdBy,omitempty"`
	CreatedAt    time.Time      `json:"createdAt"`

	// Populated in the tenant view (per-tenant enablement + runtime status):
	Platform   bool   `json:"platform"`
	Owned      bool   `json:"owned"`
	Entitled   bool   `json:"entitled"`
	Enabled    bool   `json:"enabled"`
	InfraState string `json:"infraState,omitempty"` // "" | provisioning | ready | failed | deprovisioning
	Health     string `json:"health,omitempty"`     // reconciling | live | blocked
	Waiting    bool   `json:"waiting,omitempty"`    // enabled but held for unmet infra deps
	// PendingDelete: the definition is being deleted and torn down; kept visible
	// as "Deleting" until its last provisioned instance is gone.
	PendingDelete bool `json:"pendingDelete,omitempty"`
	// Populated in the platform view:
	OwnerName string `json:"ownerName,omitempty"`
}

// Application is a Helm deployment defined as a catalog entity (like an agent or
// memory store): authored by the platform (Owner "") or a tenant (Owner = slug),
// entitled to tenants, and explicitly enabled per tenant — then realized as an
// Argo CD Application in that tenant's cluster. Its Azure infrastructure is a
// separate entity it DEPENDS on (see Dependencies + Wiring), not embedded.
type Application struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	Owner          string            `json:"owner"` // "" = platform-authored; else tenant slug
	Namespace      string            `json:"namespace"`
	RepoURL        string            `json:"repoURL"`
	Chart          string            `json:"chart"`
	TargetRevision string            `json:"targetRevision"`
	Values         string            `json:"values,omitempty"`
	ExposeService  string            `json:"exposeService"` // Service the gateway routes to ("" = internal)
	ExposePort     int               `json:"exposePort"`    // Service port (default 80)
	Wiring         []shared.WireLink `json:"wiring"`        // infra dependency output → Helm values path
	Dependencies   []Dependency      `json:"dependencies"`  // infrastructure | application | agent
	CreatedBy      string            `json:"createdBy,omitempty"`
	CreatedAt      time.Time         `json:"createdAt"`

	// Populated in the tenant view (per-tenant enablement + runtime status):
	Platform     bool   `json:"platform"`               // platform-authored (vs tenant-owned)
	Owned        bool   `json:"owned"`                  // owned by the viewing tenant
	Entitled     bool   `json:"entitled"`               // entitled to the viewing tenant
	Enabled      bool   `json:"enabled"`                // explicitly enabled (deployed) in the viewing tenant
	Health       string `json:"health,omitempty"`       // per-tenant lifecycle: reconciling | live | blocked
	SyncStatus   string `json:"syncStatus,omitempty"`   // Argo sync when enabled
	HealthStatus string `json:"healthStatus,omitempty"` // Argo health when enabled
	Waiting      bool   `json:"waiting,omitempty"`      // enabled but held for unmet dependencies
	// Populated in the platform view:
	OwnerName string `json:"ownerName,omitempty"` // owning tenant's display name
}
