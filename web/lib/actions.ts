"use server";

import { revalidatePath } from "next/cache";
import { apiSend, ApiError } from "@/lib/api";
import type {
  AgentDefinition,
  AgentType,
  BicepOutputSpec,
  BicepParamSpec,
  ChartInterface,
  Dependency,
  MemoryStoreDefinition,
  WireLink,
} from "@/lib/types";

export type ActionResult = { ok: true } | { ok: false; error: string };

function errMsg(e: unknown): string {
  if (e instanceof ApiError) {
    const m = /\{"error":"([^"]+)"\}/.exec(e.message);
    if (m) return m[1];
    if (e.status === 401) return "Your session expired — please sign in again.";
  }
  return "Something went wrong. Please try again.";
}

async function run(fn: () => Promise<unknown>, paths: string[]): Promise<ActionResult> {
  try {
    await fn();
    for (const p of paths) revalidatePath(p);
    return { ok: true };
  } catch (e) {
    return { ok: false, error: errMsg(e) };
  }
}

/*
Every mutation flows through one generic resource surface on the control plane:

  POST   /api/resources                       create (mixed batch)
  PATCH  /api/resources/{kind}/{id}           edit a definition
  DELETE /api/resources/{kind}/{id}           remove a definition
  POST   /api/resources/{kind}/{id}/enable    enable in the caller's tenant
  DELETE /api/resources/{kind}/{id}/enable    disable in the caller's tenant
  PATCH  /api/tenants/{slug}/all-entitlements grant/revoke every kind at once

`kind` is infrastructure | application | agent | memory_store. The typed
functions below are thin, name-stable wrappers so views don't carry the URL
vocabulary.
*/

type ResourceKind = "infrastructure" | "application" | "agent" | "memory_store";

const enablePaths: Record<ResourceKind, string[]> = {
  infrastructure: ["/infrastructure"],
  application: ["/deployments"],
  agent: ["/", "/agents"],
  memory_store: ["/memory-stores"],
};

function resourcePath(kind: ResourceKind, id: string): string {
  return `/api/resources/${kind}/${encodeURIComponent(id)}`;
}

async function deleteResource(kind: ResourceKind, id: string): Promise<ActionResult> {
  return run(() => apiSend("DELETE", resourcePath(kind, id)), enablePaths[kind]);
}

async function enableResource(
  kind: ResourceKind,
  id: string,
  body?: unknown,
): Promise<ActionResult> {
  return run(() => apiSend("POST", `${resourcePath(kind, id)}/enable`, body), enablePaths[kind]);
}

async function disableResource(kind: ResourceKind, id: string): Promise<ActionResult> {
  return run(() => apiSend("DELETE", `${resourcePath(kind, id)}/enable`), enablePaths[kind]);
}

/* ── Inspectors (authoring tooling, not CRUD) ─────────────────────────────── */

// Inspect a Bicep module's published interface (input params + outputs) so the
// infrastructure form can render a typed form and show wireable outputs before save.
export type InspectResult =
  | { ok: true; params: BicepParamSpec[]; outputs: BicepOutputSpec[]; resolved: boolean }
  | { ok: false; error: string };

export async function inspectInfraModule(bicepModule: string): Promise<InspectResult> {
  const ref = bicepModule.trim();
  if (ref === "") return { ok: true, params: [], outputs: [], resolved: false };
  try {
    const d = await apiSend<{ params: BicepParamSpec[]; outputs: BicepOutputSpec[]; resolved: boolean }>(
      "POST",
      "/api/infrastructure/inspect",
      { bicepModule: ref },
    );
    return { ok: true, params: d.params ?? [], outputs: d.outputs ?? [], resolved: Boolean(d.resolved) };
  } catch (e) {
    return { ok: false, error: errMsg(e) };
  }
}

// Inspect a Helm chart's authoring surface (default values + optional JSON Schema)
// so the deployment modal can render a typed, searchable values builder. `resolved`
// is false when the helm toolchain is absent (the console then falls back to a raw
// YAML editor) or the ref is blank; a bad/unreachable chart surfaces as an error.
export type InspectChartResult =
  | { ok: true; resolved: boolean; iface?: ChartInterface }
  | { ok: false; error: string };

export async function inspectChart(
  repoURL: string,
  chart: string,
  version: string,
): Promise<InspectChartResult> {
  const repo = repoURL.trim();
  const name = chart.trim();
  if (repo === "" || name === "") return { ok: true, resolved: false };
  try {
    const d = await apiSend<{
      resolved: boolean;
      name?: string;
      version?: string;
      description?: string;
      defaults?: Record<string, unknown>;
      schema?: Record<string, unknown>;
    }>("POST", "/api/applications/inspect-chart", { repoURL: repo, chart: name, version: version.trim() });
    if (!d.resolved) return { ok: true, resolved: false };
    return {
      ok: true,
      resolved: true,
      iface: {
        name: d.name || name,
        version: d.version ?? "",
        description: d.description,
        defaults: d.defaults ?? {},
        schema: d.schema,
      },
    };
  } catch (e) {
    return { ok: false, error: errMsg(e) };
  }
}

/* ── Entitlements + tenant access (platform) ──────────────────────────────── */

// Grant/revoke every kind for a tenant in one call (the consolidated panel sends
// the full desired state each save).
export async function setAllEntitlements(
  slug: string,
  byKind: Record<ResourceKind, string[]>,
): Promise<ActionResult> {
  return run(
    () =>
      apiSend("PATCH", `/api/tenants/${encodeURIComponent(slug)}/all-entitlements`, {
        infrastructure: byKind.infrastructure,
        applications: byKind.application,
        agents: byKind.agent,
        memoryStores: byKind.memory_store,
      }),
    [`/tenants/${slug}`, "/agents", "/deployments", "/infrastructure", "/memory-stores"],
  );
}

// Enable/disable a tenant's access (console + reconciler). Platform admins only.
export async function setTenantEnabled(slug: string, enabled: boolean): Promise<ActionResult> {
  return run(
    () => apiSend("PATCH", `/api/tenants/${encodeURIComponent(slug)}/enabled`, { enabled }),
    [`/tenants/${slug}`, "/"],
  );
}

// Re-provision a tenant's footprint: re-submit the (idempotent) footprint template
// so config fixes + new platform features reach an already-provisioned tenant.
// Platform admins only.
export async function reprovisionFootprint(slug: string): Promise<ActionResult> {
  return run(
    () => apiSend("POST", `/api/tenants/${encodeURIComponent(slug)}/reprovision`),
    [`/tenants/${slug}`, "/"],
  );
}

// Create a platform-hosted tenant (platform's own subscription, a dedicated RG
// per tenant). Platform admins only. The provisioner deploys its footprint next
// sweep; users are then assigned via memberships.
export async function createPlatformTenant(input: {
  name: string;
  region?: string;
  plan?: string;
}): Promise<ActionResult> {
  return run(
    () => apiSend("POST", "/api/tenants", { name: input.name, region: input.region ?? "", plan: input.plan ?? "" }),
    ["/tenants", "/"],
  );
}

// Assign a user (by email) to a tenant. Platform admins only.
export async function addTenantMember(slug: string, email: string): Promise<ActionResult> {
  return run(
    () => apiSend("POST", `/api/tenants/${encodeURIComponent(slug)}/members`, { email }),
    [`/tenants/${slug}`],
  );
}

// Revoke a user's assignment to a tenant. Platform admins only.
export async function removeTenantMember(slug: string, email: string): Promise<ActionResult> {
  return run(
    () => apiSend("DELETE", `/api/tenants/${encodeURIComponent(slug)}/members/${encodeURIComponent(email)}`),
    [`/tenants/${slug}`],
  );
}

/* ── Agents ───────────────────────────────────────────────────────────────── */

export async function createCatalogAgent(input: {
  name: string;
  description: string;
  type: AgentType;
  model: string;
  definition: AgentDefinition;
}): Promise<ActionResult> {
  return run(() => apiSend("POST", "/api/resources", { agents: [input] }), ["/agents"]);
}

export async function updateCatalogAgent(
  id: string,
  input: { name: string; description: string; model: string; definition: AgentDefinition },
): Promise<ActionResult> {
  return run(() => apiSend("PATCH", resourcePath("agent", id), input), ["/agents"]);
}

export async function deleteCatalogAgent(id: string): Promise<ActionResult> {
  return deleteResource("agent", id);
}

export async function enableAgent(input: {
  catalogAgentId: string;
  publishTo: string[];
}): Promise<ActionResult> {
  return enableResource("agent", input.catalogAgentId, { publishTo: input.publishTo });
}

export async function disableAgent(agentId: string): Promise<ActionResult> {
  return disableResource("agent", agentId);
}

export async function connectAgentStore(agentId: string, storeId: string): Promise<ActionResult> {
  return run(
    () => apiSend("POST", `/api/tenant/agents/${encodeURIComponent(agentId)}/store`, { storeId }),
    ["/", "/agents", "/memory-stores"],
  );
}

/* ── Memory stores ────────────────────────────────────────────────────────── */

export async function createMemoryStore(input: {
  name: string;
  description: string;
  definition: MemoryStoreDefinition;
}): Promise<ActionResult> {
  return run(() => apiSend("POST", "/api/resources", { memoryStores: [input] }), ["/memory-stores"]);
}

// A store's definition is immutable (the Foundry resource has no update surface),
// so only its name + description can be edited.
export async function updateMemoryStore(
  id: string,
  input: { name: string; description: string },
): Promise<ActionResult> {
  return run(() => apiSend("PATCH", resourcePath("memory_store", id), input), ["/memory-stores"]);
}

export async function deleteMemoryStore(id: string): Promise<ActionResult> {
  return deleteResource("memory_store", id);
}

export async function enableStore(storeId: string): Promise<ActionResult> {
  return enableResource("memory_store", storeId);
}

export async function disableStore(storeId: string): Promise<ActionResult> {
  return disableResource("memory_store", storeId);
}

/* ── Deployments (Helm → Argo CD) ─────────────────────────────────────────── */

export async function createApplication(input: {
  name: string;
  description: string;
  namespace: string;
  repoURL: string;
  chart: string;
  targetRevision: string;
  values: string;
  exposeService: string;
  exposePort: number;
  wiring: WireLink[];
  dependencies: Dependency[];
}): Promise<ActionResult> {
  return run(() => apiSend("POST", "/api/resources", { applications: [input] }), ["/deployments"]);
}

export async function updateApplication(
  id: string,
  input: {
    name: string;
    description: string;
    namespace: string;
    repoURL: string;
    chart: string;
    targetRevision: string;
    values: string;
    exposeService: string;
    exposePort: number;
    wiring: WireLink[];
    dependencies: Dependency[];
  },
): Promise<ActionResult> {
  return run(() => apiSend("PATCH", resourcePath("application", id), input), ["/deployments"]);
}

export async function deleteApplication(id: string): Promise<ActionResult> {
  return deleteResource("application", id);
}

export async function enableDeployment(id: string): Promise<ActionResult> {
  return enableResource("application", id);
}

export async function disableDeployment(id: string): Promise<ActionResult> {
  return disableResource("application", id);
}

/* ── Infrastructure (Azure/Bicep) ─────────────────────────────────────────── */

export async function createInfrastructure(input: {
  name: string;
  description: string;
  bicepModule: string;
  bicepParams: Record<string, unknown>;
  dependencies: Dependency[];
}): Promise<ActionResult> {
  return run(() => apiSend("POST", "/api/resources", { infrastructure: [input] }), ["/infrastructure"]);
}

export async function updateInfrastructure(
  id: string,
  input: {
    name: string;
    description: string;
    bicepModule: string;
    bicepParams: Record<string, unknown>;
    dependencies: Dependency[];
  },
): Promise<ActionResult> {
  return run(() => apiSend("PATCH", resourcePath("infrastructure", id), input), ["/infrastructure"]);
}

export async function deleteInfrastructure(id: string): Promise<ActionResult> {
  return deleteResource("infrastructure", id);
}

export async function enableInfrastructure(id: string): Promise<ActionResult> {
  return enableResource("infrastructure", id);
}

export async function disableInfrastructure(id: string): Promise<ActionResult> {
  return disableResource("infrastructure", id);
}
