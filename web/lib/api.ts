import "server-only";
import { cache } from "react";
import { cookies } from "next/headers";
import { decode } from "next-auth/jwt";
import type {
  AgentDefinition,
  AgentType,
  Application,
  Dependency,
  Infrastructure,
  WireLink,
  CatalogAgent,
  ClusterInfo,
  EnabledAgent,
  FleetStats,
  MemoryStore,
  MemoryStoreDefinition,
  Plan,
  EnrollmentStatus,
  Health,
  Lifecycle,
  PublishTarget,
  Role,
  TenantContextInfo,
  TenantRegistryRow,
  TenantSummary,
} from "@/lib/types";

const API_URL = process.env.CORTEX_API_URL ?? "http://localhost:8080";

/* ── Raw API shapes (mirror the Go control-plane) ─────────────────────────── */

interface ApiTenant {
  id: string;
  name: string;
  tenantId: string;
  region: string;
  plan: string;
  enrollment: string;
  lifecycle?: string;
  agentCount: number;
  reconcilingCount: number;
  version: string;
  lastHeartbeat: string | null;
  monthlyCalls: number;
  drift: number;
  subscriptionId?: string;
  reconcilerIdentity?: string;
  foundryProject?: string;
  reconcilerVersion?: string;
  installedAt?: string;
  enabled?: boolean;
  hostingMode?: string;
  resourceGroup?: string;
  cluster?: ApiCluster | null;
}
interface ApiCluster {
  name?: string;
  phase?: string;
  kubernetesVersion?: string;
  argoInstalled?: boolean;
  ingressInstalled?: boolean;
  gatewayIP?: string;
  ingressIssuer?: string;
  infraDelegated?: boolean;
  infraDetail?: string;
  footprintState?: string;
  footprintDetail?: string;
  nodeCount?: number;
  detail?: string;
}
interface ApiAgent {
  id: string;
  name: string;
  type: string;
  version: string;
  desiredVersion: string;
  drift: boolean;
  channel: string;
  model: string;
  definition: AgentDefinition;
  health: string;
  publishTo: string[];
  calls30d: number;
  note?: string;
  memoryStore?: string;
}
interface ApiMe {
  oid: string;
  tid: string;
  name: string;
  email: string;
  role: Role;
  tenant: ApiTenant | null;
  tenants?: ApiTenant[] | null;
}
interface ApiFleet {
  stats: FleetStats;
  tenants: ApiTenant[];
}
interface ApiTenantContext {
  tenant: ApiTenant;
  agents: ApiAgent[];
  infrastructure?: ApiInfrastructure[] | null;
  applications?: ApiApplication[] | null;
  stores?: ApiMemoryStore[] | null;
}

/* ── Auth: forward the API access token (server-side only) ────────────────── */

// The access token (minted for this API) lives in the encrypted, httpOnly
// session cookie. We read + decode it on the server and forward it as a Bearer
// token; the Go API validates it against Entra's JWKS. Never sent to the browser.
const SESSION_COOKIES = ["authjs.session-token", "__Secure-authjs.session-token"];

async function getAccessToken(): Promise<{ token: string; tenantSlug: string }> {
  const store = await cookies();
  for (const base of SESSION_COOKIES) {
    let raw = store.get(base)?.value;
    if (!raw) {
      // Cookie chunking: Auth.js splits large session cookies into `<name>.0`,
      // `.1`, … — reassemble them before decoding.
      const parts: string[] = [];
      for (let i = 0; ; i++) {
        const chunk = store.get(`${base}.${i}`)?.value;
        if (!chunk) break;
        parts.push(chunk);
      }
      if (parts.length) raw = parts.join("");
    }
    if (!raw) continue;
    const decoded = await decode({
      token: raw,
      secret: process.env.AUTH_SECRET!,
      salt: base,
    });
    if (!decoded) continue;
    // One access token per session (the sign-in directory). The active Cortex
    // TENANT is a slug sent as X-Cortex-Tenant, not a per-directory token.
    if (decoded.error) throw new ApiError(401, `token ${decoded.error}`);
    const accessToken = decoded.accessToken as string | undefined;
    const tenantSlug = (decoded.activeTenantSlug as string | undefined) ?? "";
    if (accessToken) return { token: accessToken, tenantSlug };
  }
  throw new ApiError(401, "no access token in session");
}

// authHeaders builds the Authorization + optional X-Cortex-Tenant headers.
async function authHeaders(extra?: Record<string, string>): Promise<Record<string, string>> {
  const { token, tenantSlug } = await getAccessToken();
  const h: Record<string, string> = { Authorization: `Bearer ${token}`, ...extra };
  if (tenantSlug) h["X-Cortex-Tenant"] = tenantSlug;
  return h;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}

async function apiGet<T>(path: string): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, {
    headers: await authHeaders(),
    cache: "no-store",
  });
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new ApiError(res.status, `GET ${path} → ${res.status} ${body}`);
  }
  return (await res.json()) as T;
}

/** POST/PATCH/DELETE with the API access token. Used by server actions. */
export async function apiSend<T = unknown>(
  method: "POST" | "PATCH" | "DELETE",
  path: string,
  body?: unknown,
): Promise<T> {
  const res = await fetch(`${API_URL}${path}`, {
    method,
    headers: await authHeaders(body !== undefined ? { "Content-Type": "application/json" } : undefined),
    body: body === undefined ? undefined : JSON.stringify(body),
    cache: "no-store",
  });
  if (!res.ok) {
    const b = await res.text().catch(() => "");
    throw new ApiError(res.status, `${method} ${path} → ${res.status} ${b}`);
  }
  const text = await res.text();
  return (text ? JSON.parse(text) : {}) as T;
}

/* ── Mappers → console view models ────────────────────────────────────────── */

const ms = (iso: string | null): number => (iso ? Date.parse(iso) : 0);

function toSummary(t: ApiTenant): TenantSummary {
  return {
    id: t.id,
    name: t.name,
    tenantId: t.tenantId,
    region: t.region,
    plan: t.plan as Plan,
    enrollment: t.enrollment as EnrollmentStatus,
    agentCount: t.agentCount,
    reconcilingCount: t.reconcilingCount,
    lastHeartbeatMs: ms(t.lastHeartbeat),
    monthlyCalls: t.monthlyCalls,
    lifecycle: (t.lifecycle ?? "enrolling") as Lifecycle,
    enabled: t.enabled ?? true,
    hostingMode: (t.hostingMode as TenantSummary["hostingMode"]) ?? "delegated",
  };
}

function toContext(t: ApiTenant): TenantContextInfo {
  return {
    id: t.id,
    name: t.name,
    tenantId: t.tenantId,
    subscriptionId: t.subscriptionId ?? "",
    region: t.region,
    plan: t.plan as Plan,
    enrollment: t.enrollment as EnrollmentStatus,
    reconcilerIdentity: t.reconcilerIdentity ?? "",
    foundryProject: t.foundryProject ?? "",
    installedAt: t.installedAt ?? "",
    reconcilerVersion: t.reconcilerVersion ?? "",
    lastHeartbeatMs: ms(t.lastHeartbeat),
    lifecycle: (t.lifecycle ?? "enrolling") as Lifecycle,
    enabled: t.enabled ?? true,
    cluster: toCluster(t.cluster),
  };
}

function toCluster(c?: ApiCluster | null): ClusterInfo {
  return {
    name: c?.name ?? "",
    phase: c?.phase ?? "",
    kubernetesVersion: c?.kubernetesVersion,
    argoInstalled: Boolean(c?.argoInstalled),
    ingressInstalled: Boolean(c?.ingressInstalled),
    gatewayIP: c?.gatewayIP,
    ingressIssuer: c?.ingressIssuer,
    infraDelegated: Boolean(c?.infraDelegated),
    infraDetail: c?.infraDetail,
    footprintState: c?.footprintState,
    footprintDetail: c?.footprintDetail,
    nodeCount: c?.nodeCount ?? 0,
    detail: c?.detail,
  };
}

function toAgent(a: ApiAgent): EnabledAgent {
  return {
    id: a.id,
    name: a.name,
    type: (a.type as AgentType) || "prompt",
    model: a.model,
    definition: a.definition ?? {},
    health: a.health as Health,
    publishTo: a.publishTo as PublishTarget[],
    calls30d: a.calls30d,
    note: a.note,
    memoryStore: a.memoryStore || undefined,
  };
}

function toApplication(a: ApiApplication): Application {
  return {
    id: a.id,
    name: a.name,
    description: a.description ?? "",
    owner: a.owner ?? "",
    namespace: a.namespace,
    repoURL: a.repoURL,
    chart: a.chart,
    targetRevision: a.targetRevision,
    values: a.values,
    exposeService: a.exposeService ?? "",
    exposePort: a.exposePort ?? 80,
    wiring: a.wiring ?? [],
    dependencies: a.dependencies ?? [],
    createdAt: a.createdAt,
    ownerName: a.ownerName,
    platform: a.platform ?? a.owner === "",
    owned: Boolean(a.owned),
    entitled: Boolean(a.entitled),
    enabled: Boolean(a.enabled),
    health: (a.health as Application["health"]) || undefined,
    syncStatus: a.syncStatus || undefined,
    healthStatus: a.healthStatus || undefined,
    waiting: Boolean(a.waiting),
  };
}

function toInfrastructure(i: ApiInfrastructure): Infrastructure {
  return {
    id: i.id,
    name: i.name,
    description: i.description ?? "",
    owner: i.owner ?? "",
    bicepModule: i.bicepModule ?? "",
    bicepParams: i.bicepParams ?? {},
    bicepOutputs: i.bicepOutputs ?? [],
    dependencies: i.dependencies ?? [],
    createdAt: i.createdAt,
    ownerName: i.ownerName,
    platform: i.platform ?? i.owner === "",
    owned: Boolean(i.owned),
    entitled: Boolean(i.entitled),
    enabled: Boolean(i.enabled),
    infraState: i.infraState || undefined,
    health: (i.health as Infrastructure["health"]) || undefined,
    waiting: Boolean(i.waiting),
    pendingDelete: Boolean(i.pendingDelete),
  };
}

function toStore(s: ApiMemoryStore): MemoryStore {
  return {
    id: s.id,
    name: s.name,
    description: s.description,
    owner: s.owner ?? "",
    definition: normalizeStoreDefinition(s.definition),
    createdAt: s.createdAt,
    ownerName: s.ownerName,
    platform: s.platform ?? s.owner === "",
    owned: Boolean(s.owned),
    entitled: Boolean(s.entitled),
    enabled: Boolean(s.enabled),
    health: (s.health as MemoryStore["health"]) || undefined,
  };
}

/* ── Public fetchers ──────────────────────────────────────────────────────── */

export interface Me {
  name: string;
  email: string;
  role: Role;
  tid: string;
  oid: string;
  tenant: TenantSummary | null;
  tenants: TenantSummary[];
}

export const getMe = cache(async (): Promise<Me> => {
  const m = await apiGet<ApiMe>("/api/me");
  return {
    name: m.name,
    email: m.email,
    role: m.role,
    tid: m.tid,
    oid: m.oid,
    tenant: m.tenant ? toSummary(m.tenant) : null,
    tenants: (m.tenants ?? []).map(toSummary),
  };
});

export const getFleet = cache(
  async (): Promise<{ stats: FleetStats; tenants: TenantSummary[] }> => {
    const f = await apiGet<ApiFleet>("/api/fleet");
    return { stats: f.stats, tenants: (f.tenants ?? []).map(toSummary) };
  },
);

export interface TenantContext {
  tenant: TenantContextInfo;
  summary: TenantSummary;
  agents: EnabledAgent[];
  infrastructure: Infrastructure[];
  applications: Application[];
  stores: MemoryStore[];
}

const context = cache(async (path: string): Promise<TenantContext> => {
  const c = await apiGet<ApiTenantContext>(path);
  return {
    tenant: toContext(c.tenant),
    summary: toSummary(c.tenant),
    agents: (c.agents ?? []).map(toAgent),
    infrastructure: (c.infrastructure ?? []).map(toInfrastructure),
    applications: (c.applications ?? []).map(toApplication),
    stores: (c.stores ?? []).map(toStore),
  };
});

export const getMyContext = () => context("/api/tenant/context");
export const getTenantContext = (slug: string) =>
  context(`/api/tenants/${encodeURIComponent(slug)}/context`);

/* ── Catalog + registry ───────────────────────────────────────────────────── */

interface ApiCatalogAgent {
  id: string;
  name: string;
  description: string;
  type: string;
  model: string;
  owner?: string;
  definition?: AgentDefinition;
  latestVersion?: string;
  versions?: { version: string; channel: string; notes?: string; rolloutPercent: number; definition: AgentDefinition; createdAt: string }[];
  createdAt: string;
  ownerName?: string;
  platform?: boolean;
  owned?: boolean;
  entitled: boolean;
  enabled: boolean;
}

function toCatalogAgent(a: ApiCatalogAgent): CatalogAgent {
  // The backend may still return a versions array until its own strip lands; take
  // the single current definition from `definition` when present, else the latest
  // version's definition.
  const versions = a.versions ?? [];
  const latest = versions.find((v) => v.version === a.latestVersion) ?? versions[versions.length - 1];
  return {
    id: a.id,
    name: a.name,
    description: a.description,
    type: (a.type as AgentType) || "prompt",
    model: a.model,
    owner: a.owner ?? "",
    definition: a.definition ?? latest?.definition ?? {},
    createdAt: a.createdAt,
    ownerName: a.ownerName,
    platform: a.platform ?? (a.owner ?? "") === "",
    owned: Boolean(a.owned),
    entitled: Boolean(a.entitled),
    enabled: Boolean(a.enabled),
  };
}

export const getCatalog = async (): Promise<CatalogAgent[]> => (await getResources()).agents;

interface ApiRegistryRow extends ApiTenant {
  entitledAgents: string[];
  entitledCount: number;
  entitledStores: string[];
  entitledDeployments: string[];
  entitledInfrastructure: string[];
}

export const getTenantsRegistry = cache(async (): Promise<TenantRegistryRow[]> => {
  const c = await apiGet<{ tenants: ApiRegistryRow[] }>("/api/tenants");
  return (c.tenants ?? []).map((t) => ({
    id: t.id,
    name: t.name,
    tenantId: t.tenantId,
    region: t.region,
    plan: t.plan as Plan,
    enrollment: t.enrollment as EnrollmentStatus,
    agentCount: t.agentCount,
    lastHeartbeatMs: ms(t.lastHeartbeat),
    monthlyCalls: t.monthlyCalls,
    entitledAgents: t.entitledAgents ?? [],
    entitledCount: t.entitledCount,
    entitledStores: t.entitledStores ?? [],
    entitledDeployments: t.entitledDeployments ?? [],
    entitledInfrastructure: t.entitledInfrastructure ?? [],
    lifecycle: (t.lifecycle ?? "enrolling") as Lifecycle,
    enabled: t.enabled ?? true,
  }));
});

/* ── Memberships (platform-hosted tenant assignments) ─────────────────────── */

export interface TenantMember {
  email: string;
  oid: string;
  role: string;
  createdAt: string;
}

export const getTenantMembers = cache(async (slug: string): Promise<TenantMember[]> => {
  const r = await apiGet<{ members: TenantMember[] }>(
    `/api/tenants/${encodeURIComponent(slug)}/members`,
  );
  return r.members ?? [];
});

/* ── Applications (Helm deployments → Argo CD) ────────────────────────────── */

interface ApiApplication {
  id: string;
  name: string;
  description?: string;
  owner?: string;
  namespace: string;
  repoURL: string;
  chart: string;
  targetRevision: string;
  values?: string;
  exposeService?: string;
  exposePort?: number;
  wiring?: WireLink[] | null;
  dependencies?: Dependency[] | null;
  createdAt: string;
  ownerName?: string;
  platform?: boolean;
  owned?: boolean;
  entitled?: boolean;
  enabled?: boolean;
  health?: string;
  syncStatus?: string;
  healthStatus?: string;
  waiting?: boolean;
}

export const getApplications = async (): Promise<Application[]> =>
  (await getResources()).applications;

/* ── Infrastructure (Azure/Bicep → control plane) ─────────────────────────── */

interface ApiInfrastructure {
  id: string;
  name: string;
  description?: string;
  owner?: string;
  bicepModule?: string;
  bicepParams?: Record<string, unknown> | null;
  bicepOutputs?: string[] | null;
  dependencies?: Dependency[] | null;
  createdAt: string;
  ownerName?: string;
  platform?: boolean;
  owned?: boolean;
  entitled?: boolean;
  enabled?: boolean;
  infraState?: string;
  health?: string;
  waiting?: boolean;
  pendingDelete?: boolean;
}

export const getInfrastructure = async (): Promise<Infrastructure[]> =>
  (await getResources()).infrastructure;

/* ── Memory stores ────────────────────────────────────────────────────────── */

interface ApiMemoryStore {
  id: string;
  name: string;
  description: string;
  owner: string;
  definition?: Partial<MemoryStoreDefinition> | null;
  createdAt: string;
  ownerName?: string;
  platform?: boolean;
  owned?: boolean;
  entitled?: boolean;
  enabled?: boolean;
  health?: string;
}

/** Fill in a complete definition from a possibly-partial API payload, so the UI
 * always has concrete values (matching the server + Foundry defaults). */
function normalizeStoreDefinition(d?: Partial<MemoryStoreDefinition> | null): MemoryStoreDefinition {
  return {
    chatModel: d?.chatModel || "gpt-4o",
    embeddingModel: d?.embeddingModel || "text-embedding-3-small",
    userProfileEnabled: d?.userProfileEnabled ?? true,
    userProfileDetails: d?.userProfileDetails ?? "",
    chatSummaryEnabled: d?.chatSummaryEnabled ?? true,
    proceduralMemoryEnabled: d?.proceduralMemoryEnabled ?? true,
    ttlSeconds: d?.ttlSeconds ?? 0,
  };
}

export const getMemoryStores = async (): Promise<MemoryStore[]> =>
  (await getResources()).stores;

/* ── Combined resource fetch (one call powers every catalog view) ─────────── */

interface ApiResources {
  infrastructure?: ApiInfrastructure[] | null;
  applications?: ApiApplication[] | null;
  agents?: ApiCatalogAgent[] | null;
  memoryStores?: ApiMemoryStore[] | null;
}

export interface Resources {
  infrastructure: Infrastructure[];
  applications: Application[];
  agents: CatalogAgent[];
  stores: MemoryStore[];
}

// One request returns every catalog entity the caller can see (role-aware on the
// control plane). Cached per request, so the four public fetchers above share a
// single round-trip when a page needs more than one kind.
export const getResources = cache(async (): Promise<Resources> => {
  const r = await apiGet<ApiResources>("/api/resources");
  return {
    infrastructure: (r.infrastructure ?? []).map(toInfrastructure),
    applications: (r.applications ?? []).map(toApplication),
    agents: (r.agents ?? []).map(toCatalogAgent),
    stores: (r.memoryStores ?? []).map(toStore),
  };
});
