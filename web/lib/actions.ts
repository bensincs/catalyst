"use server";

import { revalidatePath } from "next/cache";
import { apiSend, ApiError } from "@/lib/api";
import type { AgentDefinition, AgentType, MemoryStoreDefinition } from "@/lib/types";

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

export async function createCatalogAgent(input: {
  name: string;
  description: string;
  type: AgentType;
  model: string;
  definition: AgentDefinition;
}): Promise<ActionResult> {
  return run(() => apiSend("POST", "/api/catalog", input), ["/catalog"]);
}

export async function publishVersion(
  agentId: string,
  input: { version: string; channel: string; notes: string; rolloutPercent: number; definition: AgentDefinition },
): Promise<ActionResult> {
  return run(
    () => apiSend("POST", `/api/catalog/${encodeURIComponent(agentId)}/versions`, input),
    ["/catalog"],
  );
}

export async function setEntitlements(
  slug: string,
  entitledAgents: string[],
): Promise<ActionResult> {
  return run(
    () => apiSend("PATCH", `/api/tenants/${encodeURIComponent(slug)}/entitlements`, { entitledAgents }),
    [`/tenants/${slug}`, "/catalog"],
  );
}

export async function enableAgent(input: {
  catalogAgentId: string;
  publishTo: string[];
}): Promise<ActionResult> {
  return run(() => apiSend("POST", "/api/tenant/agents", input), ["/", "/catalog", "/agents"]);
}

export async function disableAgent(agentId: string): Promise<ActionResult> {
  return run(
    () => apiSend("DELETE", `/api/tenant/agents/${encodeURIComponent(agentId)}`),
    ["/", "/catalog", "/agents"],
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

