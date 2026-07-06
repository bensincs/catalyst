# Cortex — Product Plan

**Status:** Draft v2 · **Target:** Azure public / commercial · **Substrate:** Microsoft Foundry (new, GA)

Cortex is a **production, enterprise-grade, buyable multi-tenant AI-agent platform**, assembled from Azure
managed services and differentiated by a thin product layer: **one central SaaS control plane** plus a
lightweight **in-tenant agents app** (a reconciler + a Foundry project) that customers install into their
own Azure subscription.

We **adopt** every capability Microsoft ships as a managed service and spend engineering only on the
differentiated multi-tenant product layer on top.

---

## Table of contents

1. [Product definition](#1-product-definition)
2. [Guiding principles](#2-guiding-principles)
3. [Architecture](#3-architecture)
4. [Install & enrollment](#4-install--enrollment)
5. [Target architecture (Azure-managed spine)](#5-target-architecture-azure-managed-spine)
6. [Core flow: Publish → Entitle → Install → Reconcile](#6-core-flow-publish--entitle--install--reconcile)
7. [The reconciler](#7-the-reconciler)
8. [Data models](#8-data-models)
9. [Foundry capability scope (GA-only) & decisions](#9-foundry-capability-scope-ga-only--decisions)
10. [What we build (the moat)](#10-what-we-build-the-moat)
11. [Requirements — epics E1–E11](#11-requirements--epics-e1e11)
12. [Security, identity & compliance](#12-security-identity--compliance)
13. [Observability & FinOps](#13-observability--finops)
14. [Commercialization / buyable](#14-commercialization--buyable)
15. [Roadmap (phased)](#15-roadmap-phased)
16. [Risks & open decisions](#16-risks--open-decisions)
17. [Week-1 spikes & next steps](#17-week-1-spikes--next-steps)

---

## 1. Product definition

Cortex lets an organization **run a fleet of AI agents on Microsoft Foundry** without building or operating
agent infrastructure. A **platform team** (us / the publisher) curates a versioned catalog of agents and
skill packs in the control plane; each **customer tenant** installs the Cortex app, enables the
agents it is entitled to, configures them, and publishes them to its users — all under the customer's own
Entra identity, running in the customer's own subscription.

**Cortex is:** *a central control plane (catalog + entitlement + admin/builder UX) + an in-tenant reconciler
+ governance + a commercial wrapper*, sitting on top of Foundry and the Azure managed spine.

**Cortex is NOT:**
- A generic app-deployment platform (no per-app App Services, no arbitrary containers per feature).
- An identity/OAuth provider (Entra ID and Entra Agent ID handle all identity).
- An agent runtime (Foundry Agent Service runs the agents; Agent Framework orchestrates them).
- A model host or LLM gateway (Azure OpenAI / Foundry models, optionally fronted by APIM GenAI gateway).

Our unique value is **multi-tenant fleet management** — define an agent once, version it, gate it by plan,
roll it out gradually, and manage it across the whole customer base from one console — plus the **buyable
product surface** (builder, admin, Marketplace) that makes it transactable.

---

## 2. Guiding principles

1. **Adopt, don't rebuild.** If Azure/Microsoft ships it managed, we adopt it. We build only what differentiates us.
2. **Enterprise by inheritance.** Identity, security, compliance, residency come from **Entra, Purview, Defender, Sentinel** — not custom code.
3. **Buyable is the finish line.** End state = a **transactable Azure Marketplace offer** with metering and self-serve onboarding.
4. **Thin, multi-tenant, differentiated.** Our IP = the control plane + reconciler + builder/admin UX + governance + domain agent packs.
5. **End-to-end before breadth.** A working end-to-end product for one tenant beats half-built breadth.
6. **Migrate by parallel-run then cut over.** Stand the managed service up beside anything self-hosted, prove parity, then decommission.
7. **GA on the critical path; preview behind a flag.** Ship v1 on generally available Foundry surface; keep preview features (Toolbox, Teams/M365 publish, memory, web search) opt-in and non-blocking.
8. **Agents run in the customer's tenant.** The control plane is central SaaS; data, models, and agents stay in the customer's own subscription. Hard isolation and direct Azure consumption by default.

---

## 3. Architecture

Two components, one system of record. A **central control plane** (SaaS, in our tenant) is where all
authoring, entitlement, and administration happen. A **Cortex app** (installed by each customer into
their own subscription) runs a **reconciler** that pulls its tenant's desired state and provisions agents in
a **Foundry project** that lives in the customer's tenant. The reconciler only ever talks *outbound* to the
control plane.

```
              browser login (Entra, multi-tenant app)
   Platform Admins ─┐        Tenant Admins ─┐
                    ▼                        ▼
┌───────────────────────────────────────────────────────────────┐
│ CORTEX CONTROL PLANE — SaaS, in YOUR (vendor) tenant           │
│  UI + API + DB (Cosmos)                                        │
│  Anyone can sign in; access gated by role:                    │
│    • Platform Admin (you)     • Tenant Admin (customer)        │
│  System of record:                                            │
│    catalog · entitlements · desired state (per tenant) ·      │
│    tenant registry · rollouts · metering · fleet dashboard    │
│  API: Publisher · Enroll · Sync · Heartbeat · MP fulfillment  │
└───────────────────────────────────────────────────────────────┘
                         ▲  outbound HTTPS only
                         │  enroll → pull desired → heartbeat
                         │  (NO inbound access to customer tenant)
┌────────────────────────┴──────────────────────────────────────┐
│ CUSTOMER TENANT — their Azure subscription                     │
│  "Cortex" app  (Marketplace Managed Application)         │
│   ├── Reconciler        (Container App + managed identity)      │
│   ├── Microsoft Foundry resource + project                     │
│   ├── Model deployments · agent connections / MCP              │
│   ├── Key Vault · Storage (local cache) · App Insights         │
│   └── (optional) APIM GenAI gateway · AI Search index          │
│                                                                │
│  Agents run HERE. End users chat via their own Entra identity; │
│  Foundry auto-issues an Entra Agent ID per agent.              │
└───────────────────────────────────────────────────────────────┘
```

### Access model

- The control plane is a **multi-tenant Entra app**. Any Entra user can *authenticate*, but they are
  *authorized* to nothing until (a) their tenant is enrolled and (b) they hold a role.
- **Platform Admin** (our staff, home tenant = ours): authors the catalog, releases versions, manages
  entitlements, sees the whole fleet.
- **Tenant Admin** (customer staff, home tenant = theirs): sees only their tenant's slice — enables/
  configures entitled agents, chooses publish targets, views their tenant's health and usage.
- The initial Tenant Admin is established at purchase/enrollment (the Marketplace purchaser, or an invite);
  they can invite further admins.

### Security invariants

1. **The reconciler never touches the control-plane DB.** It calls the Sync API, which returns only that
   tenant's entitled + enabled slice. Compromising one tenant exposes nothing about others.
2. **One multi-tenant Entra app** for control-plane login (customer admin-consents once). No per-agent app
   registrations — agents receive Entra Agent IDs through Foundry automatically.
3. **Managed Identity everywhere in-tenant.** The reconciler's MI holds only Foundry-scoped RBAC
   (`Foundry User` at project scope) plus narrowly-scoped roles for the resources agents ground on — never
   subscription Contributor.
4. **Outbound-only, poll-based.** The reconciler polls Sync (ETag, ~60s) and posts heartbeats. No inbound
   connectivity to customer tenants is required; firewall-friendly. Optional webhook for faster propagation later.
5. **Steady-state survives control-plane outages.** Agents keep running in Foundry regardless; the reconciler
   caches last-known-good desired state and keeps self-healing drift even if the control plane is briefly unreachable.

---

## 4. Install & enrollment

There is **one deployment model**: central control plane + per-tenant Cortex app. The reconciler and
Foundry always run in the customer's subscription.

### Who runs what / who pays

| Concern | Location | Billing |
|---|---|---|
| Control plane (UI/API/DB, catalog, admin UX) | **Our** tenant (AKS / Container Apps) | Our cost; recovered via the Cortex subscription |
| Reconciler + Foundry + models + agents | **Customer's** tenant/subscription | **Customer pays Azure directly** for consumption |
| Cortex license/entitlements | — | **Marketplace subscription** (flat tier + optional metered dimension, e.g. active agents) |

Consequence: Cortex does **not** meter model tokens for billing (the customer pays Azure). The control
plane's metering is for **entitlement enforcement, fleet visibility, and optional cost showback**.

### Enrollment handshake (answers "how does it authenticate?")

1. A Tenant Admin, signed into the control plane, clicks **Install Cortex**.
2. The control plane creates/updates the tenant record and mints a **one-time, short-lived enrollment token**
   bound to that `tenantId`.
3. The admin is taken to the Marketplace deployment of the **Cortex Managed Application**, with the
   **enrollment token + control-plane URL** passed as deployment parameters.
4. The managed app provisions: reconciler (Container App + **user-assigned managed identity**), Foundry
   resource + project, Key Vault, storage; and assigns the reconciler MI → `Foundry User` on the project.
5. On first boot the reconciler calls `POST /enroll` with the token. The control plane — acting as its own
   trust anchor — issues a **per-tenant client certificate** (short-lived, auto-rotated before expiry),
   returned once and stored in the customer's Key Vault. The reconciler presents this certificate (mTLS /
   signed client assertion) on every call to the Cortex API thereafter. No per-tenant Entra app registration
   is needed; the enrollment token is single-use and expires quickly, and the certificate is the durable credential.
6. The reconciler is now bound to the tenant and begins the **Sync + Heartbeat** loop.

> *Future option:* migrate to secretless **workload-identity federation** (reconciler MI federated to Cortex's
> app) once cross-tenant FIC is validated — an R2+ hardening, not required for v1.

> A fully vendor-hosted variant (Foundry in *our* tenant, no customer install) is possible but is **not** the
> design center — it forfeits the isolation, ownership, and direct-consumption benefits customers want. Only
> pursue it for a specific "zero-footprint" segment if demand appears.

---

## 5. Target architecture (Azure-managed spine)

Everything below is **Adopt/Configure/Keep** unless tagged **Build**. Our engineering concentrates on the
Build rows.

| Capability | Microsoft-managed service | Approach |
|---|---|---|
| Agent runtime + memory + agent identity | **Microsoft Foundry Agent Service** | Adopt |
| Orchestration (agent + workflow) | **Microsoft Agent Framework 1.0 (GA)** | Adopt |
| LLM gateway (rate-limit, cost, cache, safety) | **Azure API Management — GenAI gateway** | Adopt (per-tenant, optional) |
| Models / inference / fine-tune / versioning | **Azure AI Foundry / Azure OpenAI** | Adopt |
| Speech (STT/TTS) | **Azure AI Speech** | Adopt |
| RAG + knowledge (ingest, OCR, vector, hybrid) | **Azure AI Search + Document Intelligence** | Adopt |
| Evaluation | **Azure AI Foundry Evaluations** | Adopt |
| Enterprise connectors | **Azure Logic Apps + Graph/Copilot connectors** | Adopt |
| Channels (Teams / Web / Mobile) | **Copilot Studio / Azure Bot Service** | Adopt + Build (preview risk — see §9) |
| Identity (user) | **Microsoft Entra ID / External ID** | Adopt |
| Fine-grained authz (ReBAC) | Entra roles/groups (+ SpiceDB only if a customer needs true ReBAC) | Keep/Build |
| Secrets | **Azure Key Vault + Managed Identity** | Adopt |
| Observability | **Azure Monitor + Application Insights** | Adopt |
| FinOps / cost | **Microsoft Cost Management** + APIM metrics | Configure |
| Threat / SIEM / DLP / compliance | **Defender + Sentinel + Purview + GitHub Advanced Security** | Adopt |
| Compute / hosting (control plane) | **AKS** or **Azure Container Apps** | Keep |
| Compute / hosting (in-tenant reconciler) | **Azure Container Apps** (managed-app packaged) | Keep |
| IaC / CI-CD | **Terraform + Azure Verified Modules + GitHub Actions** | Keep |
| **Control plane + in-tenant reconciler** | — | **Build (moat)** |
| **Builder / Admin / Publisher UX** | — | **Build (moat)** |
| **Governance layer** | — | **Build (moat)** |
| **Commercial wrapper (Marketplace)** | — | **Build (moat)** |
| **Domain agent / skill packs** | — | **Build (moat)** |

**Grounding:** [Foundry Agent Service](https://learn.microsoft.com/en-us/azure/foundry/agents/overview) ·
[Agent Framework 1.0](https://learn.microsoft.com/en-us/agent-framework/overview/) ·
[APIM GenAI gateway](https://learn.microsoft.com/en-us/azure/api-management/genai-gateway-capabilities) ·
[Agent identity](https://learn.microsoft.com/en-us/azure/foundry/agents/concepts/agent-identity) ·
[Publish to Teams/M365](https://learn.microsoft.com/en-us/azure/foundry/agents/how-to/publish-copilot) ·
[Deploy Foundry via Bicep](https://learn.microsoft.com/en-us/azure/foundry/how-to/create-resource-template) ·
[Managed Applications](https://learn.microsoft.com/en-us/azure/azure-resource-manager/managed-applications/overview)

---

## 6. Core flow: Publish → Entitle → Install → Reconcile

Availability and installation are deliberately separate: the control plane governs **what CAN be installed**
(entitlement, versions, canary); the tenant admin governs **what IS installed** (explicit choice + config).

```
1. PUBLISH (control plane, Platform Admin)
   POST /platform/agents/contract-reviewer/versions {v1.2.0, rolloutPercent: 25}
   → new version snapshotted in the catalog

2. ENTITLE (control plane, Platform Admin)
   PATCH /platform/tenants/acme/entitlements { entitledAgents += contract-reviewer }
   → tenant is now allowed to install it

3. INSTALL (Tenant Admin)
   Signs into the control plane → clicks "Install Cortex" → deploys the Managed
   Application into their subscription (reconciler + Foundry project) → reconciler ENROLLS
   to the control plane (see §4).

4. ENABLE (control plane, Tenant Admin)
   Sees "Contract Reviewer v1.2.0 AVAILABLE" → clicks Enable → writes desired state:
     enabledAgents += { contract-reviewer, channel: stable, config, publishTo: [api] }

5. RECONCILE (in-tenant reconciler)
   pull desired (Sync) ≠ actual → ensure model + connections → create/update the Foundry
     agent → assign agent-identity RBAC → expose API endpoint → heartbeat to control plane

6. PUBLISH TO USERS
   - API endpoint: automated.
   - Teams / M365 Copilot: guided admin action in the Foundry portal (preview — see §9).

7. UNINSTALL / REVOKE (reverse)
   Admin disables → reconciler removes the agent. Platform Admin revoking the entitlement
   removes it too. Uninstalling the Managed App removes the in-tenant footprint.
```

**Upgrade semantics (Decision D2):** `rolloutPercent`/canary controls **availability**, not auto-apply. A
tenant pinned to a version stays there; moving to a new version is an **explicit admin action** (or an opt-in
"always latest" channel per agent). Production agents never silently change underneath a customer.

---

## 7. The reconciler

Single loop, idempotent, GA-scoped. Runs **in the customer's tenant**. Pulls desired state from the control
plane; discovers actual state from Foundry (`cortex-managed` tag) each loop, so it needs almost no local
persistence beyond a last-known-good cache.

```go
func (r *Reconciler) Reconcile(ctx context.Context) error {
    catalog, desired, _ := r.controlPlane.Sync(ctx)    // Sync API, ETag-cached; entitled + enabled slice
    actual, _ := r.foundry.ListManagedAgents(ctx)      // tagged cortex-managed (rebuilt each loop)

    r.ensureProject(ctx)                               // Foundry project (provisioned by the managed app)

    for _, want := range desired.EnabledAgents {
        def, ok := resolve(catalog, want)              // entitled? version? deps?
        if !ok { r.flag(want, "not entitled"); continue }

        r.ensureModelDeployment(ctx, def.ModelRequirements) // quota-aware; surfaces failures
        r.ensureConnections(ctx, def.Tools)                 // per-agent MCP/tool connections (GA)
                                                            // (Toolbox = preview, opt-in later)

        switch cur := actual.Find(want.AgentId); {
        case cur == nil:
            id := r.foundry.CreateAgent(ctx, def, want.Config)
            r.assignAgentIdentityRBAC(ctx, id, def)    // grant tool resource roles to agentIdentityId
        case cur.Version != def.Version || cur.ConfigHash != hash(want.Config):
            r.foundry.UpdateAgent(ctx, cur.FoundryAgentId, def, want.Config)
        }

        r.ensurePublishing(ctx, want.PublishTo)        // "api" automated; "teams"/"m365" => guided task
    }

    r.removeOrphans(ctx, actual, desired)              // disabled/revoked agents
    r.controlPlane.Heartbeat(ctx, summary)             // actual state + usage → fleet dashboard + metering
    return nil
}
```

**Critical GA gotcha baked in:** publishing an agent mints a **new distinct Entra agent identity** and RBAC
does **not** carry over from the shared project identity. `assignAgentIdentityRBAC` must (re)apply the
agent's data-plane roles (e.g. `Storage Blob Data Contributor` on the grounding source) to the current
`agentIdentityId`, or published agents silently lose tool access.

---

## 8. Data models

### Control plane (central, SaaS) — system of record

```jsonc
// agent-catalog item
{
  "id": "contract-reviewer",
  "displayName": "Contract Reviewer",
  "kind": "prompt-agent",              // v1: prompt-agent | later: hosted-agent (container)
  "packRef": "legal-pack@3",           // optional: part of a domain skill pack
  "versions": [{
    "version": "1.2.0",
    "stable": true,
    "channel": "stable",               // stable | beta
    "rolloutPercent": 25,              // availability gate, NOT auto-apply
    "modelRequirements": { "family": "gpt-4o", "minCapacity": 10 },
    "definition": {
      "instructions": "You are a contract review assistant...",
      "tools": ["file_search", "azure_ai_search", "openapi:contracts-api"],  // GA tools
      "mcpConnections": ["sharepoint"],   // per-agent connection (Toolbox deferred)
      "guardrailProfile": "strict"
    },
    "dependencies": { "connection:sharepoint": ">=1", "model:gpt-4o": ">=10" }
  }]
}

// entitlement (per tenant)
{
  "id": "acme", "plan": "enterprise",
  "entitledAgents": ["contract-reviewer", "hr-assistant"],
  "entitledPacks": ["legal-pack"],
  "allowBeta": false, "maxAgents": 25
}

// desired state (per tenant — authored by the Tenant Admin in the central UI)
{ "tenantId": "acme", "enabledAgents": [
  { "agentId": "contract-reviewer", "channel": "stable", "pinnedVersion": "1.2.0",
    "config": { "departmentSharePoint": "https://acme.sharepoint.com/legal" },
    "publishTo": ["api", "teams"] }        // "api" auto; "teams" => guided admin task
]}

// tenant-registry (enrollment + fleet status)
{ "id": "acme", "status": "active", "enrollment": "bound",
  "reconcilerIdentity": "…/managedIdentities/cortex-recon",
  "controlPlaneUrl": "https://app.cortex.ai",
  "currentVersions": { "contract-reviewer": "1.2.0" },
  "lastHeartbeat": "2026-07-03T12:00:00Z" }
```

### Reconciler (in-tenant) — local cache only

```jsonc
// actual state — rebuilt from Foundry each loop, cached locally; posted on heartbeat
{ "agents": [
  { "agentId": "contract-reviewer", "version": "1.2.0", "foundryAgentId": "asst_abc123",
    "agentIdentityId": "…", "publish": { "api": "healthy", "teams": "pending-admin-approval" },
    "status": "healthy" }
]}
```

---

## 9. Foundry capability scope (GA-only) & decisions

v1 is built on the **new Foundry** (not classic; classic agents retire 2027) and on **generally available**
surface. Previews are opt-in and never on the critical path.

| Capability | Status | v1 approach |
|---|---|---|
| Prompt-agent CRUD (project API, MI auth) | **GA** | Adopt. Auth `https://ai.azure.com/.default`; `Foundry User` at project scope. |
| Model deployment via ARM/Bicep | **GA** | Adopt (`Microsoft.CognitiveServices/accounts` + `/projects` + `/deployments`). |
| Entra Agent ID (auto-provisioned, MI-federated) | **GA** | Adopt. Re-assign RBAC to distinct identity on publish. |
| Per-agent tools/connections (file search, AI Search, OpenAPI, code interpreter, function) | **GA** | Adopt as the dependency mechanism. |
| Hosted agents (container-packaged) | GA-ish | **Defer** to R2/R3; prompt agents cover v1. |
| **Toolbox** (curated tool sets, versioned, single MCP endpoint) | **Preview** (`V1Preview`) | **Defer**; use per-agent connections in v1. Fast-follow when GA. |
| **Publish to Teams / M365 Copilot** | **Early Access Preview** | **Guided admin action** in Foundry portal, tracked by heartbeat — NOT a headless pipeline. |
| Memory / web search / deep research / computer use | Preview | Off the v1 path. |

> **Decision (D3 — publishing):** Cortex fully automates agent + model + connections + **API endpoint**. For
> Teams/M365, Cortex provisions what it can (Azure Bot Service via ARM, manifest generation) and **deep-links
> the customer admin** into the Foundry publish flow; org-wide distribution still requires the customer's
> **M365 admin approval** (a human gate that exists regardless). Known preview limits: no streaming, no
> citations, no file upload / image-gen in M365 (works in Teams), no Private Link.

> **Decision (D4 — CRUD surface):** confirm in the week-1 spike whether the **new** Foundry model's
> agent-CRUD API version is GA, or whether only the classic assistants API is GA against the new project
> endpoint. This determines which SDK/client the reconciler is built on.

> **Decision (D5 — reconciler ↔ control-plane auth):** **per-tenant client certificate**, issued during a
> one-time enrollment-token handshake and stored in the customer's Key Vault; the reconciler authenticates to
> the Cortex API with certificate-based client credentials (mTLS / signed client assertion). The control plane
> is its own trust anchor (issues + validates certs, short-lived + auto-rotated) — no per-tenant Entra app
> registration. Secretless workload-identity federation is a possible R2+ hardening, not a v1 dependency.

---

## 10. What we build (the moat)

Everything in §5 is Adopt/Configure. Engineering concentrates on five differentiators:

1. **Multi-tenant control plane** — catalog, entitlement, per-tenant desired state, enrollment, fleet management; the reconciler that applies it in-tenant.
2. **Builder & Admin experience** — agent builder, tenant admin, and platform/publisher admin UIs; the buyable product surface.
3. **Governance layer** — tenant-scoped access, connector-credential governance, policy, data-deletion (GDPR).
4. **Commercial wrapper** — Azure Marketplace transactable offer (SaaS subscription + Managed App), metering, billing, self-serve onboarding.
5. **Domain agent / skill packs** — differentiated IP customers pay for, distributed via the catalog.

---

## 11. Requirements — epics E1–E11

Each epic is a milestone; bullets are ticket-ready. Tags: `[Adopt] [Build] [Configure] [Keep]`.

### E1 — Managed AI Runtime `[Adopt]`
**Goal:** Agents run on Foundry Agent Service; orchestration on Agent Framework; managed memory + per-agent Entra identity.
- Provision Foundry resource + project (in the customer tenant, via the Cortex app); prompt agents in v1 (hosted agents deferred).
- Orchestration on **Microsoft Agent Framework 1.0** (agent + deterministic workflow).
- Per-agent **Entra Agent ID** + OBO for tool auth; RBAC re-assignment on publish.
- Adopt Foundry **managed memory** (opt-in; short/long) once GA on the target region.
- **DoD:** an agent is created, runs, remembers across sessions, calls tools — all on Foundry. **Depends on:** E2, E11.

### E2 — AI Gateway & Models `[Adopt]`
**Goal:** Model traffic governed; models from Azure OpenAI / Foundry.
- Import Azure OpenAI + Foundry model endpoints; content-safety + rate limits via Foundry model deployments.
- **APIM GenAI gateway per-tenant (optional):** `llm-token-limit`, `llm-semantic-cache`, token-metric policies where a tenant needs advanced gateway features (weigh per-tenant APIM cost).
- Model-agnostic backend selection (Azure OpenAI primary; secondary backend behind the same interface).
- **DoD:** every model call is governed (native or APIM) with per-tenant token limits + cost metrics. **Depends on:** E11.

### E3 — Knowledge & RAG `[Adopt]`
**Goal:** AI Search + Document Intelligence for ingest and retrieval.
- Provision AI Search (integrated vectorization) + Document Intelligence in the tenant.
- Ingestion: source → Document Intelligence (OCR/layout) → AI Search skillset → index.
- **Per-tenant index isolation** + security trimming via Entra groups (natural — it's the tenant's own resource).
- Wire agents to AI Search retrieval (agentic retrieval / "on your data").
- **DoD:** multi-format docs ingested + retrievable per-tenant. **Depends on:** E1, E9.

### E4 — Connectors & Channels `[Adopt + Build]`
**Goal:** Enterprise connectors + multi-channel via managed services. (Reclassified from `[Adopt]` — Teams/M365 publish is preview.)
- Channels: **API endpoint (auto)**; **Teams/M365 via guided admin publish** (preview, portal-first); Web/Mobile via Bot Service.
- Connectors: **SAP, ServiceNow, Salesforce, M365** via Logic Apps / Graph connectors.
- Tenant connector-credential governance (Key Vault + Entra OBO).
- **DoD:** an agent is reachable from Teams (admin-published); a tenant connects SAP + M365 without custom code. **Depends on:** E1, E9.

### E5 — Control Plane & Enrollment `[Build — moat]`
**Goal:** The central control plane + in-tenant install/enroll of §3–§4.
- Control plane: UI + API + DB; catalog, entitlements, per-tenant desired state, rollouts, tenant registry, Sync + Heartbeat APIs, fleet dashboard.
- **Multi-tenant login** (Entra) with Platform Admin / Tenant Admin roles and per-tenant authorization.
- **Cortex Managed Application**: reconciler + Foundry + Key Vault + storage; RBAC bootstrap.
- **Enrollment handshake** (D5): one-time token → **per-tenant certificate** (mTLS / signed assertion); outbound-only reconciler auth.
- Lifecycle: enroll, suspend, offboard, **data deletion (GDPR)**; in-tenant reconciler update mechanism.
- **DoD:** a Tenant Admin logs in, installs the app, and a tenant is provisioned + enrolled end-to-end via one flow. **Depends on:** E1, E11.

### E6 — Builder & Admin Experience `[Build — moat]`
**Goal:** The product UX (all served from the central control plane).
- Agent builder UI on Foundry APIs (create/configure/test agents, tools, prompts, knowledge).
- Tenant admin UI (users, access, connectors, usage, cost, enable/configure agents, publish targets, install status).
- Platform/publisher admin UI (catalog, versions, canary, entitlements, fleet health, billing).
- **DoD:** a non-developer builds + publishes an agent and manages a tenant from the UI. **Depends on:** E1–E5.

### E7 — Evaluation & Quality `[Adopt]`
**Goal:** Continuous eval + CI gates via Foundry Evaluations.
- Adopt Foundry eval SDK; port benchmark datasets.
- CI eval gates on agent/prompt changes; safety + quality evaluators; regression baselines + dashboards.
- **DoD:** agent changes are gated by automated evals in CI. **Depends on:** E1.

### E8 — Observability & FinOps `[Adopt / Configure]`
**Goal:** Full telemetry, alerting, cost showback.
- App Insights + Azure Monitor for the control plane and each in-tenant reconciler + Foundry agent traces.
- Curated Workbooks / Managed Grafana; golden-signal SLOs; alert rules + action groups; runbooks.
- FinOps: Cost Management + APIM token metrics → **per-tenant showback** via heartbeat → fleet dashboard.
- **DoD:** SLOs + alerting live; per-tenant cost visible. **Depends on:** E1, E2.

### E9 — Security & Compliance `[Adopt]`
**Goal:** Enterprise security/compliance by inheritance.
- Entra ID / External ID; Conditional Access; per-agent identity; least-privilege reconciler MI (Foundry-scoped).
- Defender for Cloud/Containers; **Sentinel** SIEM; **GitHub Advanced Security** (secret scanning, CodeQL, push protection).
- **Purview** classification/DLP/audit; **Compliance Manager** (SOC 2 / ISO 27001 controls).
- Key Vault + Managed Identity + CMK; network policy + Private Link.
- Threat model dispositioned; external penetration test.
- **DoD:** GHAS + Defender + Sentinel + Purview live; controls mapped; pentest passed. **Depends on:** E11.

### E10 — Commercialization / Buyable `[Build — moat]`
**Goal:** Customers can buy and self-onboard.
- **Azure Marketplace transactable SaaS offer** (SaaS Fulfillment API) for the Cortex subscription + **Managed Application offer** for the in-tenant Cortex app.
- Metering / billing (Marketplace metered dimensions, e.g. active agents; consumption stays on the customer's Azure bill).
- Pricing / packaging (tiers, quotas via entitlements); trial + self-serve onboarding.
- Commercial: EULA, SLA, support model.
- **DoD:** a customer purchases via Marketplace, installs the app, and self-onboards a tenant. **Depends on:** E5, E6, E9.

### E11 — Platform Foundation / DevOps `[Keep / Configure]`
**Goal:** Solid hosting + delivery for both the control plane and the packaged in-tenant app.
- Azure landing zone + subscription/RG + networking for the control plane.
- Compute: AKS or **Azure Container Apps** for the control plane; Container Apps for the packaged reconciler.
- IaC: Terraform + **Azure Verified Modules**; the Cortex app as a versioned ARM/Bicep Managed-App package.
- CI/CD: GitHub Actions + GitOps; **SHA-tagged images**; progressive delivery; **managed-app update path** for in-tenant reconcilers.
- Environments: dev / qa / uat / prod; DR.
- **DoD:** reproducible, SHA-pinned deployments; a new reconciler version rolls out to enrolled tenants safely. **Depends on:** —.

---

## 12. Security, identity & compliance

- **User identity:** Entra ID / External ID; control-plane login is a multi-tenant app. Authorization gates everything by tenant + role (Platform Admin / Tenant Admin).
- **Agent identity:** Foundry auto-provisions an Entra Agent ID per agent (blueprint + identity, federated to the project MI — no stored secrets). Distinct identity on publish → RBAC re-assignment handled by the reconciler.
- **Reconciler ↔ control-plane auth (D5):** established at enrollment via a one-time token that yields a **per-tenant client certificate** (stored in the customer's Key Vault, short-lived, auto-rotated). Each reconciler authenticates as **its own certificate identity** (mTLS / signed client assertion) — never a shared app secret — so one tenant's compromise can't impersonate another. Outbound-only. (Secretless WIF is a possible R2+ hardening.)
- **Least privilege:** reconciler MI holds `Foundry User` at project scope + narrow data roles for grounding resources; publishing adds `Azure Bot Service Contributor` on the bot RG only. Never subscription Contributor.
- **Compliance by inheritance:** Purview (classification, DLP, audit), Defender, Sentinel, Compliance Manager (SOC 2 / ISO 27001). CMK + Private Link where required.
- **Guardrails:** Foundry content safety / XPIA mitigation via `guardrailProfile` per agent version.

---

## 13. Observability & FinOps

- **Traces:** Foundry agent tracing → App Insights (in-tenant); control-plane traces → our App Insights.
- **Metrics/SLOs:** Azure Monitor golden signals; Workbooks / Managed Grafana; alert rules + action groups.
- **Cost:** consumption is on the **customer's** Azure bill (their Foundry/models). Cortex surfaces **per-tenant showback** from heartbeat data for FinOps visibility; Cortex's own charge is the Marketplace subscription (+ optional metered dimension).
- **Fleet visibility:** reconciler heartbeats (`POST /sync/status`) roll up to the publisher dashboard (which tenants on which versions, enrollment status, health, drift).

---

## 14. Commercialization / buyable

- **Two Marketplace artifacts, one relationship:**
  - **Cortex subscription** — a **transactable SaaS offer** (SaaS Fulfillment API) that grants control-plane access + entitlements (plan tiers, entitled agents/packs, `maxAgents`).
  - **Cortex app** — a **Managed Application** the Tenant Admin installs into their subscription (reconciler + Foundry).
- **Consumption vs license:** model/compute consumption is the **customer's own Azure cost**; Cortex bills a **subscription** (flat tier) plus an optional **metered dimension** (e.g. active agents) via Marketplace metered billing.
- **Onboarding:** purchase → sign into control plane → install Cortex → enroll → enable agents.
- **Certification:** Partner Center review for **both** the SaaS offer and the Managed Application (allow slack for first submission).

---

## 15. Roadmap (phased)

| Phase | Theme | Epics | Window (est.) | Exit criteria |
|---|---|---|---|---|
| **MVP** | Demoable end-to-end (single tenant, manually-provisioned Foundry) | E1–E3 core, E5 slice | ~8 wks | A tenant is provisioned; an agent runs on Foundry; tools + basic RAG + chat work — design-partner grade. |
| **R1** | **End-to-end on the Azure managed spine** | E11, E2, E1, E3, E5 (enroll), E8*, E9* | **~8–12 wks** | Control plane + in-tenant reconciler live; a Tenant Admin installs the app, enrolls, and agents reconcile end-to-end; one production tenant with SLOs, alerting, enforced security gates. |
| **R2** | **Enterprise + Buyable** | E5 (full), E4, E6, E7, E9 (full), E10 | **~10–14 wks** | Enterprise-grade, multi-channel; **transactable on Azure Marketplace** (SaaS subscription + Managed App); customers self-onboard; Purview/Sentinel compliance + pentest passed. |
| **R3** | **Scale + Differentiate** | domain packs, Toolbox (GA), hosted agents, fine-tuning, certs; **air-gapped / Arc** for disconnected estates | **later** | Certified GA at scale (SOC 2 / ISO); differentiated agent packs; disconnected/sovereign estates supported via Arc. |

\* R1 takes the *basics* of E8/E9 (telemetry, Entra, Key Vault, Defender, GHAS); full compliance/SIEM/pentest lands in R2.

### Critical path
```
E11 (landing zone + managed-app package) → E2 (models/gateway) → E1 (Foundry runtime + Agent Framework)
   → E5 (control plane + enrollment + reconciler)
        → { E3 knowledge · E8 observability · E9 security } 
             → { E4 channels+connectors · E6 builder/admin UX · E7 eval } → E10 (Marketplace / buyable)
```

---

## 16. Risks & open decisions

| # | Risk / decision | Recommendation |
|---|---|---|
| D1 | **Topology** | **Settled:** central SaaS control plane + in-tenant Cortex app (reconciler + Foundry). Agents run in the customer's subscription. |
| D2 | **Upgrade semantics:** auto-upgrade vs pinned | Canary = availability only; upgrades **admin-gated** (opt-in "always latest" per agent). |
| D3 | **Teams/M365 publish is preview & portal-first** | **Guided admin action** in v1; automate Bot Service + manifest incrementally; don't design the critical path around a headless publish API that doesn't exist yet. |
| D4 | **New vs classic Foundry agent-CRUD GA surface** | Resolve in week-1 spike; pick the SDK/client accordingly. |
| D5 | **Reconciler ↔ control-plane auth** | **Settled:** enrollment token → **per-tenant client certificate** in Key Vault (short-lived, auto-rotated), cert-based client credentials; control plane is its own trust anchor. WIF is an optional R2+ hardening. |
| 6 | **Model quota is the #1 install-time failure** | Default to serverless / Models-as-a-Service where possible; quota-aware reconcile that surfaces failures in the UI; provisioned quota as an upgrade. |
| 7 | **In-tenant reconciler update path** | Managed-app update flow + self-update (pull advertised container tag); test rollout to enrolled tenants; never break steady-state. |
| 8 | **Per-tenant APIM cost** | APIM GenAI gateway is optional per tenant; rely on Foundry-native safety/limits for v1; add APIM where advanced gateway features are needed. |
| 9 | **Toolbox is preview** | Per-agent connections in v1; adopt Toolbox when GA. |
| 10 | **Data residency (e.g. UAE) on Azure public** | Naturally satisfied — Foundry runs in the customer's tenant/region; confirm region availability of Foundry / AI Search / Speech before R1 locks. |
| 11 | **Marketplace: two offers (SaaS + Managed App)** | Kick off fulfillment + managed-app packaging + metering spike at R2 start; budget dual certification. |
| 12 | **Fine-grained ReBAC** has no managed equal | Default to Entra roles/groups; SpiceDB only if a customer needs true ReBAC. |
| 13 | **Escape hatch on cancellation** | Uninstalling the Managed App removes the in-tenant footprint; offboarding = data-deletion flow. Decide whether agents are tombstoned or left running (managed-app permission mode). |

---

## 17. Week-1 spikes & next steps

**Spike (one script, one manually-created Foundry, run as a managed identity):**
1. `az deployment group create` a Foundry account + project + `gpt-4o` deployment (Bicep from `foundry-samples`).
2. Create a prompt agent via the **new** project API; attach a GA tool + one MCP connection; run a thread; assert completion. *(Resolves D4.)*
3. Read back the auto-provisioned `agentIdentityId`; `az role assignment create` a data role to it; prove a tool call hitting that resource.
4. **Enrollment handshake:** stand up a stub control-plane `/enroll`; prove the flow that mints a one-time token, issues a **per-tenant certificate**, stores it in Key Vault, and has the reconciler authenticate to the Cortex API with cert-based client credentials + auto-rotation. *(Implements D5.)*
5. Attempt Teams/M365 publish headlessly — document exactly where the portal/approval wall is. *(Confirms D3 scope.)*

**Next steps:**
1. Green-light the R1 scope line (E11 → E2 → E1 → E5) — topology and D5 (cert-based auth) are settled.
2. Run the week-1 spike; fold results back into E1/E4/E5.
3. Generate GitHub issues from E1–E11 (milestones = R1/R2/R3, one issue per bullet, labels per epic).
4. Decide default model + confirm target-region availability of Foundry / AI Search / Speech.
