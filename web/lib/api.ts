import "server-only";
import { cache } from "react";
import { cookies } from "next/headers";
import { decode } from "next-auth/jwt";
import type {
  AgentDefinition,
  AgentType,
  Application,
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
}
interface ApiFleet {
  stats: FleetStats;
  tenants: ApiTenant[];
}
interface ApiTenantContext {
  tenant: ApiTenant;
  agents: ApiAgent[];
}

/* ── Auth: forward the API access token (server-side only) ────────────────── */

// The access token (minted for this API) lives in the encrypted, httpOnly
// session cookie. We read + decode it on the server and forward it as a Bearer
// token; the Go API validates it against Entra's JWKS. Never sent to the browser.
const SESSION_COOKIES = ["authjs.session-token", "__Secure-authjs.session-token"];

async function getAccessToken(): Promise<string> {
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
    // Forward the ACTIVE tenant's access token (the user may hold several).
    const tenants = decoded.tenants as Record<string, { accessToken?: string; error?: string }> | undefined;
    const activeTid = decoded.activeTid as string | undefined;
    const active = tenants && activeTid ? tenants[activeTid] : undefined;
    if (active?.error) throw new ApiError(401, `token ${active.error}`);
    if (active?.accessToken) return active.accessToken;
  }
  throw new ApiError(401, "no access token in session");
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
  const token = await getAccessToken();
  const res = await fetch(`${API_URL}${path}`, {
    headers: { Authorization: `Bearer ${token}` },
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
  const token = await getAccessToken();
  const res = await fetch(`${API_URL}${path}`, {
    method,
    headers: {
      Authorization: `Bearer ${token}`,
      ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
    },
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
    version: t.version,
    lastHeartbeatMs: ms(t.lastHeartbeat),
    monthlyCalls: t.monthlyCalls,
    drift: t.drift,
    lifecycle: (t.lifecycle ?? "enrolling") as Lifecycle,
    enabled: t.enabled ?? true,
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
    version: a.version,
    desiredVersion: a.desiredVersion || a.version,
    drift: Boolean(a.drift),
    channel: a.channel as EnabledAgent["channel"],
    model: a.model,
    definition: a.definition ?? {},
    health: a.health as Health,
    publishTo: a.publishTo as PublishTarget[],
    calls30d: a.calls30d,
    note: a.note,
    memoryStore: a.memoryStore || undefined,
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
}

const context = cache(async (path: string): Promise<TenantContext> => {
  const c = await apiGet<ApiTenantContext>(path);
  return {
    tenant: toContext(c.tenant),
    summary: toSummary(c.tenant),
    agents: (c.agents ?? []).map(toAgent),
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
  latestVersion: string;
  versions: { version: string; channel: string; notes?: string; rolloutPercent: number; definition: AgentDefinition; createdAt: string }[];
  createdAt: string;
  ownerName?: string;
  platform?: boolean;
  owned?: boolean;
  entitled: boolean;
  enabled: boolean;
}

export const getCatalog = cache(async (): Promise<CatalogAgent[]> => {
  const c = await apiGet<{ agents: ApiCatalogAgent[] }>("/api/catalog");
  return (c.agents ?? []).map((a) => ({
    ...a,
    type: (a.type as AgentType) || "prompt",
    owner: a.owner ?? "",
    platform: a.platform ?? (a.owner ?? "") === "",
    owned: Boolean(a.owned),
    entitled: Boolean(a.entitled),
    enabled: Boolean(a.enabled),
    versions: (a.versions ?? []).map((v) => ({
      ...v,
      channel: v.channel === "beta" ? "beta" : "stable",
      definition: v.definition ?? {},
    })),
  }));
});

interface ApiRegistryRow extends ApiTenant {
  entitledAgents: string[];
  entitledCount: number;
  entitledStores: string[];
  entitledDeployments: string[];
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
    version: t.version,
    lastHeartbeatMs: ms(t.lastHeartbeat),
    monthlyCalls: t.monthlyCalls,
    entitledAgents: t.entitledAgents ?? [],
    entitledCount: t.entitledCount,
    entitledStores: t.entitledStores ?? [],
    entitledDeployments: t.entitledDeployments ?? [],
    lifecycle: (t.lifecycle ?? "enrolling") as Lifecycle,
    enabled: t.enabled ?? true,
  }));
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
  bicepModule?: string;
  bicepParams?: Record<string, unknown> | null;
  bicepOutputs?: string[] | null;
  wiring?: WireLink[] | null;
  dependsOn?: string[] | null;
  createdAt: string;
  ownerName?: string;
  platform?: boolean;
  owned?: boolean;
  entitled?: boolean;
  enabled?: boolean;
  health?: string;
  syncStatus?: string;
  healthStatus?: string;
  infraState?: string;
  waiting?: boolean;
}

export const getApplications = cache(async (): Promise<Application[]> => {
  const c = await apiGet<{ applications: ApiApplication[] }>("/api/applications");
  return (c.applications ?? []).map((a) => ({
    id: a.id,
    name: a.name,
    description: a.description ?? "",
    owner: a.owner ?? "",
    namespace: a.namespace,
    repoURL: a.repoURL,
    chart: a.chart,
    targetRevision: a.targetRevision,
    values: a.values,
    bicepModule: a.bicepModule ?? "",
    bicepParams: a.bicepParams ?? {},
    bicepOutputs: a.bicepOutputs ?? [],
    wiring: a.wiring ?? [],
    dependsOn: a.dependsOn ?? [],
    createdAt: a.createdAt,
    ownerName: a.ownerName,
    platform: a.platform ?? a.owner === "",
    owned: Boolean(a.owned),
    entitled: Boolean(a.entitled),
    enabled: Boolean(a.enabled),
    health: (a.health as Application["health"]) || undefined,
    syncStatus: a.syncStatus || undefined,
    healthStatus: a.healthStatus || undefined,
    infraState: a.infraState || undefined,
    waiting: Boolean(a.waiting),
  }));
});

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

export const getMemoryStores = cache(async (): Promise<MemoryStore[]> => {
  const c = await apiGet<{ stores: ApiMemoryStore[] }>("/api/memory-stores");
  return (c.stores ?? []).map((s) => ({
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
  }));
});
