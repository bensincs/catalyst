"use server";

import { revalidatePath } from "next/cache";
import { apiSend, ApiError } from "@/lib/api";
import type { AgentDefinition, AgentType } from "@/lib/types";

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

