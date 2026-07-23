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
  | "drift" // live state differs from desired — warning (amber)
  | "blocked" // couldn't be realized; action required — danger (red)
  | "disabled" // inert / not enabled — neutral (gray)
  | "unknown"; // enabled but no live reconciler has confirmed — neutral (gray)

export type EnrollmentStatus = "bound" | "pending" | "suspended" | "offboarding";

/**
 * Operational lifecycle, derived by the control plane from enrollment + how
 * fresh the reconciler's last heartbeat is. This is the "is it actually working
 * right now" status, distinct from enrollment (the binding state).
 */
// Lifecycle follows the install flow: pending (awaiting the Lighthouse
// delegation) → provisioning (Cortex building the environment) → enrolling
// (environment ready, awaiting the reconciler's first heartbeat) → live; degraded
// when a bound reconciler goes stale or provisioning failed; suspended when cut off.
export type Lifecycle = "pending" | "provisioning" | "enrolling" | "live" | "degraded" | "suspended";

export type Plan = "enterprise" | "sovereign" | "team";

export type PublishTarget = "api" | "teams" | "m365";

/** How an agent is realized in Foundry (see AGENT-MODEL.md). */
export type AgentType = "prompt" | "hosted";

/** The substance of an agent, authored by the publisher. Which fields apply is
 * decided by the agent's type. */
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
  lastHeartbeatMs: number; // epoch ms
  monthlyCalls: number;
  lifecycle: Lifecycle;
  enabled: boolean; // access gate: may sign in / run a reconciler
}

export interface EnabledAgent {
  id: string;
  name: string;
  type: AgentType;
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
  ingressInstalled: boolean;
  gatewayIP?: string;
  ingressIssuer?: string; // Entra issuer the ingress enforces ("" ⇒ closed)
  infraDelegated: boolean; // control plane can reach the tenant's Lighthouse-delegated RG
  infraDetail?: string; // human note about delegation reachability
  footprintState?: string; // "" | provisioning | ready | failed (reconciler + Foundry provisioned by control plane)
  footprintDetail?: string;
  nodeCount: number;
  detail?: string;
}

/** The kinds of catalog entity a dependency edge can point at. */
export type DepKind = "infrastructure" | "application" | "agent" | "memory_store";

/** A typed dependency edge in the catalog graph. Allowed edges (enforced in the
 *  UI pickers): infrastructure → infrastructure; application → infrastructure |
 *  application | agent; agent → memory_store (handled in the agent editor). */
export interface Dependency {
  kind: DepKind;
  id: string;
}

/** Maps an output of one of an application's dependencies into a Helm values path
 *  (the wiring). The source is any dependency: an infrastructure entity (its Bicep
 *  outputs), a dependency application (name / namespace / serviceHost), or a
 *  dependency agent (agentId / name). */
export interface WireLink {
  sourceKind: DepKind; // infrastructure | application | agent
  sourceId: string; // id of the dependency the output comes from
  output: string;
  helmPath: string;
}

/** One input parameter of a Bicep module, resolved from its published schema —
 *  drives the generated authoring form (types, required, allowed values). */
export interface BicepParamSpec {
  name: string;
  type: string; // string | int | bool | object | array | securestring | secureobject
  required: boolean;
  default?: unknown;
  allowed?: unknown[]; // allowedValues → a dropdown
  description?: string;
  secure?: boolean;
}

/** One output of a Bicep module (name + type), for the wiring board. */
export interface BicepOutputSpec {
  name: string;
  type: string;
}

/** A Helm chart's authoring surface — its default values (values.yaml) and an
 *  optional JSON Schema (values.schema.json) — resolved from the chart so the
 *  console can render a typed, searchable values builder instead of a raw
 *  textarea. The stored deployment `values` are override-only YAML: Helm merges
 *  them over these defaults at install. */
export interface ChartInterface {
  name: string;
  version: string;
  description?: string;
  defaults: Record<string, unknown>; // values.yaml → the default value tree
  schema?: Record<string, unknown>; // values.schema.json (JSON Schema), when present
}

/** A typed dependency candidate ({kind,id,name}) offered in a form's dependency
 *  picker. Pre-filtered by the page to entitled/owned entities + allowed edges. */
export interface DepOption {
  id: string;
  name: string;
  kind: DepKind;
}

/** Infrastructure defined as a catalog entity (the Azure/Bicep half, split out
 *  from deployments): a published Bicep module authored by the platform or a
 *  tenant, entitled to tenants, and enabled per tenant — then provisioned by the
 *  control plane into the tenant's resource group. Its outputs are wired into an
 *  application's Helm values. It may depend on other infrastructure. */
export interface Infrastructure {
  id: string;
  name: string;
  description: string;
  owner: string; // "" = platform-authored; else tenant slug
  bicepModule?: string; // OCI ref to a published Bicep module (Azure infra)
  bicepParams?: Record<string, unknown>; // author-supplied module params
  bicepOutputs: string[]; // resolved module output names (for wiring)
  dependencies: Dependency[]; // other infrastructure that must provision first
  createdAt: string;
  // platform view
  ownerName?: string;
  // tenant view flags
  platform: boolean;
  owned: boolean;
  entitled: boolean;
  enabled?: boolean; // explicitly enabled (provisioned) in the viewing tenant
  infraState?: string; // Bicep infra: "" | provisioning | ready | failed | deprovisioning
  health?: Health; // per-tenant lifecycle when enabled: reconciling | live | blocked
  waiting?: boolean; // enabled but held until dependencies converge
  pendingDelete?: boolean; // definition is being deleted + torn down ("Deleting")
}

/** A deployment defined as a catalog entity (like an agent or memory store):
 *  authored by the platform or a tenant, entitled to tenants, and enabled per
 *  tenant — then realized as an Argo CD Application in that tenant's cluster.
 *  It wires the outputs of its infrastructure dependencies into the Helm values,
 *  and may depend on other infrastructure, applications, or agents. */
export interface Application {
  id: string;
  name: string;
  description: string;
  owner: string; // "" = platform-authored; else tenant slug
  namespace: string;
  repoURL: string;
  chart: string;
  targetRevision: string;
  values?: string;
  exposeService: string; // in-cluster Service the gateway routes to ("" = internal)
  exposePort: number; // Service port (default 80)
  wiring: WireLink[]; // infrastructure output → Helm values path
  dependencies: Dependency[]; // typed dependencies that must converge first
  createdAt: string;
  // platform view
  ownerName?: string;
  // tenant view flags
  platform: boolean;
  owned: boolean;
  entitled: boolean;
  enabled?: boolean; // explicitly enabled (deployed) in the viewing tenant
  health?: Health; // per-tenant lifecycle when enabled: reconciling | live | blocked
  syncStatus?: string; // Argo sync when enabled
  healthStatus?: string; // Argo health when enabled
  waiting?: boolean; // enabled but held until dependencies converge
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
}

export interface CatalogAgent {
  id: string;
  name: string;
  description: string;
  type: AgentType;
  model: string;
  owner: string; // "" = platform-authored; else tenant slug
  definition: AgentDefinition;
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
  lastHeartbeatMs: number;
  monthlyCalls: number;
  entitledAgents: string[];
  entitledCount: number;
  entitledStores: string[];
  entitledDeployments: string[];
  entitledInfrastructure: string[];
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
  provisioning: { label: "Provisioning", tone: "info" },
  pending: { label: "Pending", tone: "neutral" },
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
