"use server";

import { revalidatePath } from "next/cache";
import { apiSend, ApiError } from "@/lib/api";
import type {
  AgentDefinition,
  AgentType,
  BicepOutputSpec,
  BicepParamSpec,
  ChartInterface,
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

// Inspect a Bicep module's published interface (input params + outputs) so the
// deployment modal can render a typed form and show wireable outputs before save.
export type InspectResult =
  | { ok: true; params: BicepParamSpec[]; outputs: BicepOutputSpec[]; resolved: boolean }
  | { ok: false; error: string };

export async function inspectModule(bicepModule: string): Promise<InspectResult> {
  const ref = bicepModule.trim();
  if (ref === "") return { ok: true, params: [], outputs: [], resolved: false };
  try {
    const d = await apiSend<{ params: BicepParamSpec[]; outputs: BicepOutputSpec[]; resolved: boolean }>(
      "POST",
      "/api/applications/inspect",
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

export async function createCatalogAgent(input: {
  name: string;
  description: string;
  type: AgentType;
  model: string;
  definition: AgentDefinition;
}): Promise<ActionResult> {
  return run(() => apiSend("POST", "/api/catalog", input), ["/agents"]);
}

export async function publishVersion(
  agentId: string,
  input: { version: string; channel: string; notes: string; rolloutPercent: number; definition: AgentDefinition },
): Promise<ActionResult> {
  return run(
    () => apiSend("POST", `/api/catalog/${encodeURIComponent(agentId)}/versions`, input),
    ["/agents"],
  );
}

export async function setEntitlements(
  slug: string,
  entitledAgents: string[],
): Promise<ActionResult> {
  return run(
    () => apiSend("PATCH", `/api/tenants/${encodeURIComponent(slug)}/entitlements`, { entitledAgents }),
    [`/tenants/${slug}`, "/agents"],
  );
}

// Enable/disable a tenant's access (console + reconciler). Platform admins only.
export async function setTenantEnabled(slug: string, enabled: boolean): Promise<ActionResult> {
  return run(
    () => apiSend("PATCH", `/api/tenants/${encodeURIComponent(slug)}/enabled`, { enabled }),
    [`/tenants/${slug}`, "/"],
  );
}

export async function enableAgent(input: {
  catalogAgentId: string;
  publishTo: string[];
}): Promise<ActionResult> {
  return run(() => apiSend("POST", "/api/tenant/agents", input), ["/", "/agents"]);
}

export async function disableAgent(agentId: string): Promise<ActionResult> {
  return run(
    () => apiSend("DELETE", `/api/tenant/agents/${encodeURIComponent(agentId)}`),
    ["/", "/agents"],
  );
}

/* ── Memory stores ────────────────────────────────────────────────────────── */

export async function createMemoryStore(input: {
  name: string;
  description: string;
  definition: MemoryStoreDefinition;
}): Promise<ActionResult> {
  return run(() => apiSend("POST", "/api/memory-stores", input), ["/memory-stores"]);
}

// A store's definition is immutable (the Foundry resource has no update surface),
// so only its name + description can be edited.
export async function updateMemoryStore(
  id: string,
  input: { name: string; description: string },
): Promise<ActionResult> {
  return run(
    () => apiSend("PATCH", `/api/memory-stores/${encodeURIComponent(id)}`, input),
    ["/memory-stores"],
  );
}

export async function deleteMemoryStore(id: string): Promise<ActionResult> {
  return run(
    () => apiSend("DELETE", `/api/memory-stores/${encodeURIComponent(id)}`),
    ["/memory-stores"],
  );
}

export async function setStoreEntitlements(
  slug: string,
  entitledStores: string[],
): Promise<ActionResult> {
  return run(
    () => apiSend("PATCH", `/api/tenants/${encodeURIComponent(slug)}/store-entitlements`, { entitledStores }),
    [`/tenants/${slug}`, "/memory-stores"],
  );
}

export async function connectAgentStore(agentId: string, storeId: string): Promise<ActionResult> {
  return run(
    () => apiSend("POST", `/api/tenant/agents/${encodeURIComponent(agentId)}/store`, { storeId }),
    ["/", "/agents", "/memory-stores"],
  );
}

// Enable/disable a memory store in the caller's tenant — the store lifecycle
// mirror of enabling/disabling an agent.
export async function enableStore(storeId: string): Promise<ActionResult> {
  return run(
    () => apiSend("POST", `/api/tenant/stores/${encodeURIComponent(storeId)}`),
    ["/memory-stores"],
  );
}

export async function disableStore(storeId: string): Promise<ActionResult> {
  return run(
    () => apiSend("DELETE", `/api/tenant/stores/${encodeURIComponent(storeId)}`),
    ["/memory-stores"],
  );
}

/* ── Deployments — catalog entities (like memory stores) ──────────────────── */

export async function createApplication(input: {
  name: string;
  description: string;
  namespace: string;
  repoURL: string;
  chart: string;
  targetRevision: string;
  values: string;
  bicepModule: string;
  bicepParams: Record<string, unknown>;
  wiring: WireLink[];
  dependsOn: string[];
}): Promise<ActionResult> {
  return run(() => apiSend("POST", "/api/applications", input), ["/deployments"]);
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
    bicepModule: string;
    bicepParams: Record<string, unknown>;
    wiring: WireLink[];
    dependsOn: string[];
  },
): Promise<ActionResult> {
  return run(
    () => apiSend("PATCH", `/api/applications/${encodeURIComponent(id)}`, input),
    ["/deployments"],
  );
}

export async function deleteApplication(id: string): Promise<ActionResult> {
  return run(
    () => apiSend("DELETE", `/api/applications/${encodeURIComponent(id)}`),
    ["/deployments"],
  );
}

export async function setDeploymentEntitlements(
  slug: string,
  entitledDeployments: string[],
): Promise<ActionResult> {
  return run(
    () =>
      apiSend("PATCH", `/api/tenants/${encodeURIComponent(slug)}/deployment-entitlements`, {
        entitledDeployments,
      }),
    [`/tenants/${slug}`, "/deployments"],
  );
}

export async function enableDeployment(id: string): Promise<ActionResult> {
  return run(
    () => apiSend("POST", `/api/tenant/deployments/${encodeURIComponent(id)}`),
    ["/deployments"],
  );
}

export async function disableDeployment(id: string): Promise<ActionResult> {
  return run(
    () => apiSend("DELETE", `/api/tenant/deployments/${encodeURIComponent(id)}`),
    ["/deployments"],
  );
}
