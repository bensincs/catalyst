// Domain types for the Cortex control-plane console.
// Mirrors the data models in PLAN.md §8 / IMPLEMENTATION.md §5, trimmed to what
// the shell + landing pages render.

export type Role = "platform" | "tenant";

export type Environment = "dev" | "qa" | "uat" | "prod";

/** Control-plane lifecycle vocabulary, shared by agents and memory stores. Each
 * maps to a status color + a second cue. */
export type Health =
  | "live" // provisioned & converged in the tenant's cluster — success (lime)
  | "reconciling" // being provisioned into the cluster — info (violet)
  | "drift" // newer version desired than what's live — warning (amber)
  | "blocked" // couldn't be realized; action required — danger (red)
  | "disabled" // inert / not enabled — neutral (gray)
  | "unknown"; // enabled but no live reconciler has confirmed — neutral (gray)

export type EnrollmentStatus = "bound" | "pending" | "suspended" | "offboarding";

/**
 * Operational lifecycle, derived by the control plane from enrollment + how
 * fresh the reconciler's last heartbeat is. This is the "is it actually working
 * right now" status, distinct from enrollment (the binding state).
 */
export type Lifecycle = "enrolling" | "live" | "degraded" | "suspended";

export type Plan = "enterprise" | "sovereign" | "team";

export type PublishTarget = "api" | "teams" | "m365";

/** How an agent is realized in Foundry (see AGENT-MODEL.md). */
export type AgentType = "prompt" | "hosted";

/** The versioned substance of an agent, authored by the publisher. Which fields
 * apply is decided by the agent's type. */
export interface AgentDefinition {
  // prompt
  instructions?: string;
  tools?: string[];
  knowledge?: string[];
  temperature?: number;
  topP?: number;
  memoryStore?: string; // id of a connected memory store
  // hosted
  image?: string;
  endpoint?: string;
  cpu?: string;
  memory?: string;
  env?: Record<string, string>;
}

export interface TenantSummary {
  id: string;
  name: string;
  tenantId: string; // Entra tenant GUID (mono)
  region: string;
  plan: Plan;
  enrollment: EnrollmentStatus;
  agentCount: number;
  reconcilingCount: number;
  version: string; // reconciler / spine version
  lastHeartbeatMs: number; // epoch ms
  monthlyCalls: number;
  drift?: number; // pending desired-vs-actual changes
  lifecycle: Lifecycle;
  enabled: boolean; // access gate: may sign in / run a reconciler
}

export interface EnabledAgent {
  id: string;
  name: string;
  type: AgentType;
  version: string; // actual — converged by the reconciler
  desiredVersion: string; // desired — latest catalog version for its channel
  drift: boolean; // desired ≠ actual (reconcile pending/underway)
  channel: "stable" | "beta";
  model: string;
  definition: AgentDefinition;
  health: Health;
  publishTo: PublishTarget[];
  calls30d: number;
  note?: string;
  memoryStore?: string; // effective connected memory store (override or catalog default)
}

export interface ClusterInfo {
  name: string;
  phase: string; // provisioning | ready | unreachable | "" (none)
  kubernetesVersion?: string;
  argoInstalled: boolean;
  nodeCount: number;
  detail?: string;
}

/** A Helm deployment a tenant runs in its cluster (realized as an Argo CD
 * Application). sync/health are reported by the reconciler. */
export interface Application {
  id: string;
  name: string;
  namespace: string;
  repoURL: string;
  chart: string;
  targetRevision: string;
  values?: string;
  syncStatus: string; // Synced | OutOfSync | Unknown | pending
  healthStatus: string; // Healthy | Progressing | Degraded | pending
  createdAt: string;
}

export interface TenantContextInfo {
  id: string;
  name: string;
  tenantId: string;
  subscriptionId: string;
  region: string;
  plan: Plan;
  enrollment: EnrollmentStatus;
  reconcilerIdentity: string;
  foundryProject: string;
  installedAt: string;
  reconcilerVersion: string;
  lastHeartbeatMs: number;
  lifecycle: Lifecycle;
  enabled: boolean;
  cluster: ClusterInfo;
}

export interface FleetStats {
  tenants: number;
  bound: number;
  agents: number;
  callsMonth: number;
  latestVersion: string;
  onLatest: number;
}

export interface CatalogVersion {
  version: string;
  channel: "stable" | "beta";
  notes?: string;
  rolloutPercent: number;
  definition: AgentDefinition;
  createdAt: string;
}

export interface CatalogAgent {
  id: string;
  name: string;
  description: string;
  type: AgentType;
  model: string;
  owner: string; // "" = platform-authored; else tenant slug
  latestVersion: string;
  versions: CatalogVersion[];
  createdAt: string;
  // platform view
  ownerName?: string;
  // tenant view flags
  platform: boolean;
  owned: boolean;
  entitled: boolean;
  enabled: boolean;
}

export interface TenantRegistryRow {
  id: string;
  name: string;
  tenantId: string;
  region: string;
  plan: Plan;
  enrollment: EnrollmentStatus;
  agentCount: number;
  version: string;
  lastHeartbeatMs: number;
  monthlyCalls: number;
  entitledAgents: string[];
  entitledCount: number;
  entitledStores: string[];
  lifecycle: Lifecycle;
  enabled: boolean;
}

/** The typed Foundry memory-store definition (kind "default"): the models that
 * process memory plus which memory kinds are extracted. Immutable once created —
 * the Foundry resource has no update surface. */
export interface MemoryStoreDefinition {
  chatModel: string; // Foundry chat deployment
  embeddingModel: string; // Foundry embedding deployment
  userProfileEnabled: boolean;
  userProfileDetails?: string;
  chatSummaryEnabled: boolean;
  proceduralMemoryEnabled: boolean;
  ttlSeconds: number; // 0 = never expire
}

/** A reusable Foundry memory store that agents connect to. Platform stores
 * (owner === "") are granted via entitlements; tenant stores (owner === the
 * tenant slug) are private to their tenant. */
export interface MemoryStore {
  id: string;
  name: string;
  description: string;
  owner: string; // "" = platform-authored; else tenant slug
  definition: MemoryStoreDefinition;
  createdAt: string;
  // platform view
  ownerName?: string;
  // tenant view flags
  platform: boolean;
  owned: boolean;
  entitled: boolean;
  enabled?: boolean; // explicitly enabled (reconciled) in the viewing tenant
  health?: Health; // per-tenant lifecycle when enabled: reconciling | live | blocked
}

export interface HealthMeta {
  label: string;
  tone: "success" | "info" | "warning" | "danger" | "neutral";
}

export const HEALTH_META: Record<Health, HealthMeta> = {
  live: { label: "Live", tone: "success" },
  reconciling: { label: "Reconciling", tone: "info" },
  drift: { label: "Drift", tone: "warning" },
  blocked: { label: "Blocked", tone: "danger" },
  disabled: { label: "Disabled", tone: "neutral" },
  unknown: { label: "Unreported", tone: "neutral" },
};

export const ENROLLMENT_META: Record<EnrollmentStatus, HealthMeta> = {
  bound: { label: "Bound", tone: "success" },
  pending: { label: "Enrolling", tone: "info" },
  suspended: { label: "Suspended", tone: "warning" },
  offboarding: { label: "Offboarding", tone: "danger" },
};

export const LIFECYCLE_META: Record<Lifecycle, HealthMeta> = {
  live: { label: "Live", tone: "success" },
  enrolling: { label: "Enrolling", tone: "info" },
  degraded: { label: "Degraded", tone: "warning" },
  suspended: { label: "Suspended", tone: "neutral" },
};

export const ENV_META: Record<
  Environment,
  { label: string; short: string; tone: "neutral" | "info" | "warning" | "danger" }
> = {
  dev: { label: "Development", short: "DEV", tone: "neutral" },
  qa: { label: "QA", short: "QA", tone: "info" },
  uat: { label: "UAT", short: "UAT", tone: "warning" },
  prod: { label: "Production", short: "PROD", tone: "danger" },
};
