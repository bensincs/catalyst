# Cortex — Implementation Plan

**Companion to [`PLAN.md`](./PLAN.md)** (product/architecture). This document is the **engineering execution
plan**: tech stack, repository layout, component contracts, the certificate-enrollment mechanics, the
reconciler internals, the managed-application package, and a milestone/task breakdown for **R1** (end-to-end
on the Azure managed spine). R2/R3 are covered at lower resolution.

> Decisions referenced as `D1–D5` are defined in `PLAN.md §16`. New **implementation** decisions are `I1–In` here.

---

## Table of contents

1. [Scope & strategy](#1-scope--strategy)
2. [Technology stack](#2-technology-stack)
3. [Repository layout](#3-repository-layout)
4. [Component contracts](#4-component-contracts)
5. [Data model (Cosmos)](#5-data-model-cosmos)
6. [Enrollment & certificate auth](#6-enrollment--certificate-auth)
7. [The reconciler](#7-the-reconciler)
8. [The Cortex managed application](#8-the-cortex-managed-application)
9. [Control plane API & console](#9-control-plane-api--console)
10. [Environments, CI/CD, testing](#10-environments-cicd-testing)
11. [Milestones & task breakdown (R1)](#11-milestones--task-breakdown-r1)
12. [R2/R3 implementation notes](#12-r2r3-implementation-notes)
13. [Implementation decisions (I1–In)](#13-implementation-decisions-i1in)
14. [Open technical questions](#14-open-technical-questions)

---

## 1. Scope & strategy

**Build order (R1 critical path):** `M0 spike → E11 foundations → E2 models → E1 Foundry runtime →
E5 control plane + enrollment + reconciler → E6 console slice → E8/E9 basics`.

**Strategy:**
- **Prove the risky seams first (M0).** Foundry CRUD via REST from a managed identity, ARM Foundry
  provisioning, agent-identity RBAC, the cert-enrollment loop, and the Teams/M365 publish wall — before
  committing the architecture (resolves D3/D4/D5).
- **Test the reconciler against a hand-created Foundry** (from M0) before the managed-app packaging exists.
  This decouples E1/E5 progress from Marketplace work.
- **One vertical slice end-to-end** (one prompt agent, one tenant, `publishTo: [api]`) before breadth.
- **GA-only on the critical path.** Toolbox, Teams/M365 publish, memory, hosted agents are behind flags.

---

## 2. Technology stack

| Layer | Choice | Notes |
|---|---|---|
| Control plane API | **Go** (net/http + `chi` router) | Same language as the reconciler; shared model/client libs. |
| Control plane DB | **Azure Cosmos DB** (NoSQL/SQL API) | Partitioned by `tenantId` / `agentId`. |
| Control plane host | **Azure Container Apps** | HTTPS ingress; separate ingress/app for reconciler-facing mTLS. |
| Console (UI) | **Next.js (App Router)** + **MSAL.js** | One consolidated console (platform + tenant admin views). |
| Reconciler | **Go** binary, containerized | Runs as a continuous Container App in the customer tenant. |
| Foundry access | **REST** (`https://<res>.services.ai.azure.com/api/projects/<proj>`) | No official Go Foundry-agents SDK → call REST directly (**I1**). |
| Azure control ops | **azure-sdk-for-go** (`armcognitiveservices`, `armauthorization`, `azsecrets`, `azidentity`) | ARM provisioning + role assignments + Key Vault. |
| Managed app package | **Bicep** (compiled to ARM) + `createUiDefinition.json` | `Microsoft.Solutions/applicationDefinitions`. |
| Control-plane IaC | **Terraform + Azure Verified Modules** | Landing zone, Cosmos, Container Apps, ACR, Key Vault. |
| CI/CD | **GitHub Actions** | SHA-tagged images to **ACR**; managed-app package versioning. |
| Auth (users) | **Entra ID multi-tenant app** | Admin-consent at first login; token validation middleware. |
| Auth (reconciler↔CP) | **Per-tenant client certificate** (self-CA) | mTLS to a dedicated ingress (**D5**, **I2**). |

---

## 3. Repository layout

Monorepo (`cortex/`):

```
cortex/
├── control-plane/
│   ├── api/                  # Go: chi handlers, auth middleware, Cosmos repo, CA/enroll
│   │   ├── cmd/api/
│   │   └── internal/{publisher,tenant,sync,enroll,ca,store,authz}/
│   └── web/                  # Next.js console (platform + tenant admin)
├── reconciler/               # Go: loop, foundry REST client, arm ops, state cache
│   ├── cmd/reconciler/
│   └── internal/{loop,foundry,arm,enroll,cache}/
├── shared/                   # Go: models, foundry client, auth helpers (imported by both)
│   └── {model,foundry,azauth}/
├── managed-app/              # Managed Application package
│   ├── mainTemplate.bicep    # reconciler CA + Foundry + KV + MI + role assignments
│   ├── createUiDefinition.json
│   └── viewDefinition.json
├── catalog/                  # Seed agent definitions (versioned JSON), CI-validated
├── infra/                    # Terraform (control-plane landing zone)
├── spikes/                   # M0 scripts (throwaway-but-kept)
└── .github/workflows/        # build, test, image push, package publish
```

**Module boundary (I3):** `shared/foundry` and `shared/model` are imported by **both** the reconciler and the
control-plane API (the API needs Foundry types for validation/preview; the reconciler needs them to act).

---

## 4. Component contracts

### Control plane API surface

**Publisher** (Platform Admin; Entra token, home tenant = ours; `platform.admin` role):
```
GET   /platform/agents                              list catalog
POST  /platform/agents/{id}/versions                release a version {def, rolloutPercent, channel}
POST  /platform/agents/{id}/versions/{v}/graduate   promote canary → stable
PATCH /platform/tenants/{id}/entitlements           set plan / entitledAgents / maxAgents
POST  /platform/tenants/{id}/suspend
GET   /platform/fleet                               heartbeats: tenants × versions × health
```

**Tenant** (Tenant Admin; Entra token, home tenant = customer; `tenant.admin` role, scoped to their `tenantId`):
```
GET   /tenant/catalog                               entitled agents + versions (their slice)
GET   /tenant/desired                               current enabled agents + config
PUT   /tenant/desired                               enable/disable/configure agents, publishTo
GET   /tenant/status                                actual state + enrollment + health
POST  /tenant/install                               mint enrollment token → returns deploy URL + params
```

**Reconciler** (per-tenant client cert; mTLS ingress):
```
POST  /enroll                                        one-time token → issue client cert (once)
POST  /enroll/renew                                  cert rotation (authenticated by current cert)
GET   /sync/catalog        (ETag)                    entitled + enabled slice for this tenant
POST  /sync/status                                   heartbeat: actual state + usage summary
```

**Auth middleware (I4):** three chains — (a) Entra JWT validation for `/platform/*` + `/tenant/*` (validate
`iss/aud/exp`, map `tid`→tenant, enforce role + tenant scope); (b) mTLS client-cert validation for
`/sync/*` + `/enroll/renew` (validate CA signature + CN=`tenantId` + not-revoked); (c) one-time bearer token
for `/enroll`.

---

## 5. Data model (Cosmos)

Containers (NoSQL API):

| Container | Partition key | Holds |
|---|---|---|
| `catalog` | `/agentId` | Agent definitions + versions (see `PLAN.md §8`). |
| `tenants` | `/tenantId` | One doc per tenant: `entitlement`, `desiredState`, `registry` (enrollment + fleet status), `certThumbprints[]`. |
| `metering` | `/tenantId` | Time-bucketed usage from heartbeats (showback + entitlement checks). |
| `enrollTokens` | `/tenantId` | Single-use enrollment tokens (jti, exp, consumed flag) — TTL-expired. |

**`tenants` doc shape** (composition of the `PLAN.md §8` models):
```jsonc
{
  "id": "acme", "tenantId": "acme",
  "entitlement": { "plan": "enterprise", "entitledAgents": ["contract-reviewer"], "maxAgents": 25 },
  "desiredState": { "enabledAgents": [ /* … */ ] },
  "registry": { "status": "active", "enrollment": "bound",
                "reconcilerIdentity": "…", "currentVersions": {}, "lastHeartbeat": "…" },
  "certThumbprints": ["AB12…"]   // active certs for mTLS validation (supports rotation overlap)
}
```

**Concurrency (I5):** desired-state writes use Cosmos **ETag optimistic concurrency**; the reconciler's Sync
returns an ETag the client caches and sends as `If-None-Match`.

---

## 6. Enrollment & certificate auth

Implements **D5**. The control plane is its own CA (root key in the control-plane Key Vault).

### Sequence
```
Tenant Admin (console)                Control Plane                 Reconciler (customer tenant)
        │  POST /tenant/install            │                                  │
        │─────────────────────────────────▶│  create tenant record            │
        │                                   │  mint one-time token (JWT:       │
        │                                   │   tenantId, jti, exp≈30m, aud)   │
        │  ◀── deploy URL + {token, cpUrl} ─│                                  │
        │  (launches Marketplace deploy)    │                                  │
        │                                                                      │
        │            Managed App deploys reconciler + Foundry + KV + MI ──────▶│
        │                                   │      POST /enroll {token}        │
        │                                   │◀─────────────────────────────────│
        │                                   │  validate token (sig, exp,       │
        │                                   │   jti unused) → mark consumed     │
        │                                   │  issue client cert (CA-signed,    │
        │                                   │   CN=tenantId, ~90d)              │
        │                                   │  store thumbprint on tenant doc   │
        │                                   │──── cert + key (once) ───────────▶│ store in tenant Key Vault
        │                                                                      │
        │                                   │◀═══ mTLS /sync, /sync/status ════│ (cert on every call)
        │                                   │  POST /enroll/renew (pre-expiry) │ rotate; overlap thumbprints
```

### Implementation notes
- **Token**: signed JWT (control-plane signing key), `jti` tracked in `enrollTokens` for single-use, short TTL.
- **CA (I2)**: root cert+key in control-plane Key Vault; `internal/ca` issues leaf certs. Keep a CRL/allow-list
  via `certThumbprints` on the tenant doc (revoke by removing on offboard).
- **mTLS**: dedicated Container Apps ingress in **client-certificate-required** mode; middleware validates
  chain + `CN` + thumbprint-active. (Fallback if ingress mTLS is limiting: app-level **signed client
  assertion** JWT signed by the cert key, validated at `/sync`.)
- **Rotation**: reconciler renews at ~75% cert lifetime via `/enroll/renew`; control plane keeps both
  thumbprints valid during overlap.
- **Revocation**: offboard removes thumbprints → next mTLS call fails closed.

---

## 7. The reconciler

Go binary, continuous loop (default 60s; jittered). See `PLAN.md §7` for the loop logic.

### Internal packages
- `internal/loop` — orchestration, backoff, drift detection, status reporting.
- `internal/foundry` — REST client (`shared/foundry`): agents CRUD, connections, threads (smoke test).
  Auth: `azidentity.NewManagedIdentityCredential` → token for `https://ai.azure.com/.default`.
- `internal/arm` — `armcognitiveservices` (account/project/deployments), `armauthorization` (assign roles to
  `agentIdentityId`), quota-aware model deployment with graceful failure surfacing.
- `internal/enroll` — enrollment + cert load/rotate from local Key Vault (`azsecrets`).
- `internal/cache` — last-known-good desired state (survives control-plane outage) in local Storage/Table.

### Key behaviors
- **Idempotent**: everything keyed by `cortex-managed` tag + `agentId`; safe to re-run.
- **Quota-aware (I6)**: `ensureModelDeployment` catches quota errors, marks the agent `blocked:quota`,
  surfaces via heartbeat → console; retries with backoff. Prefer serverless/MaaS where available.
- **Publish**: `api` fully automated (stable endpoint); `teams/m365` sets `pending-admin-approval` and emits a
  deep-link task in status (D3).
- **RBAC-on-publish (critical)**: after publish, read the new distinct `agentIdentityId` from the agent
  application resource JSON and re-assign the agent's data-plane roles (else tool access breaks).

---

## 8. The Cortex managed application

`managed-app/` — a `Microsoft.Solutions` Managed Application published to Partner Center.

**`createUiDefinition.json`** collects: company name, admin group/users for tenant-admin, Foundry region,
model choice, consent acknowledgement. **Enrollment token + control-plane URL** are passed through (the
`/tenant/install` response injects them).

**`mainTemplate` (Bicep) deploys:**
- User-assigned **managed identity** (reconciler).
- **Foundry** account (`Microsoft.CognitiveServices/accounts`, kind=Foundry) + **project** + default model deployment.
- **Container App** (reconciler image from our ACR) with the MI, env: `CONTROL_PLANE_URL`, `ENROLL_TOKEN` (secure), `TENANT_ID`.
- **Key Vault** (stores the issued client cert) + **Storage** (local cache) + **App Insights**.
- **Role assignments**: reconciler MI → `Foundry User` at project scope; Storage/KV data roles; (publish path) `Azure Bot Service Contributor` on a bot RG.

**Permissions mode (D13):** decide publisher-managed vs customer-managed. Recommend **customer-managed** (no
deny assignment; agents survive if the relationship ends; we don't need managed-RG access because the
reconciler self-updates by pulling advertised image tags). **Update path (I7):** managed-app definition
versioning + reconciler self-update (control plane advertises the target image SHA in `/sync`).

---

## 9. Control plane API & console

**API**: Go/chi, layered handlers → `store` (Cosmos repo) → domain. Stateless; horizontally scalable on
Container Apps. OpenAPI spec generated and used to type the console client.

**Console (Next.js)**:
- **Auth**: MSAL.js against the multi-tenant app; role + `tid` drive which views render.
- **Tenant Admin views**: catalog browse (entitled), enable/configure agent (form-driven by the agent
  definition's config schema), choose publish targets, **Install Cortex** (calls `/tenant/install`,
  launches the Marketplace deploy), status/health, usage/showback.
- **Platform Admin views**: catalog authoring, release version + canary %, graduate, entitlements per tenant,
  fleet dashboard from heartbeats.
- **Definition-owned agents (I8)**: every catalog agent is a `prompt` or `hosted` type, and each
  version carries the full **definition** (see AGENT-MODEL.md) — the publisher owns the substance;
  tenant enable is light (publish targets + optional knowledge binding). No per-tenant config schema.

---

## 10. Environments, CI/CD, testing

**Environments:** `dev` → `qa` → `uat` → `prod` (control plane). A dedicated **test customer subscription**
for managed-app install E2E.

**CI/CD (GitHub Actions):**
- `build-test`: Go build/vet/test + Next.js build/test; `catalog/` schema validation.
- `image`: build + **SHA-tag** control-plane-api, reconciler, console → ACR; sign (cosign) optional.
- `package`: build the managed-app `.zip` (mainTemplate + createUiDefinition), version it, publish to Partner Center (R2).
- `deploy`: Terraform apply per environment (OIDC to Azure, no secrets); progressive rollout.
- Security gates: **GHAS** (CodeQL, secret scanning, push protection), Trivy (blocking), dependency review.

**Testing strategy:**
- **Unit**: handlers, CA/enroll, resolver, reconciler diff logic (table-driven).
- **Integration (I9)**: reconciler against a **real hand-created Foundry** (M0 output) in a test sub — create/
  update/delete an agent, assign identity RBAC, run a thread. This is the truth test; mocks lie about Foundry.
- **E2E**: `/tenant/install` → managed-app deploy → enroll → enable agent → agent answers via API endpoint.
- **Cert loop test**: enroll → mTLS sync → force rotation → revoke → fail-closed.

---

## 11. Milestones & task breakdown (R1)

Each milestone lists ticket-ready tasks and a **DoD**. Sequence respects the critical path.

### M0 — De-risking spike `[week 1]`
- [ ] Bicep-deploy Foundry account + project + `gpt-4o` (from `foundry-samples`). *(E11 seed)*
- [ ] From a **managed identity**, create a prompt agent via the **new** project REST API; attach a GA tool +
      one MCP connection; run a thread; assert completion. **Record the GA `api-version`.** *(D4)*
- [ ] Read `agentIdentityId`; `az role assignment create` a data role; prove a tool call hits that resource.
- [ ] Stub `/enroll`; mint token → issue self-CA cert → store in KV → reconciler authenticates via mTLS →
      rotate. *(D5)*
- [ ] Attempt Teams/M365 publish headlessly; document the portal/approval wall. *(D3)*
- **DoD:** `spikes/FINDINGS.md` answers D3/D4/D5; the SDK/client choice for the reconciler is locked.

### M1 — Foundations (E11) `[R1]`
- [ ] Monorepo scaffolding (§3); Go workspaces; Next.js app; lint/test CI.
- [ ] Terraform landing zone: RG, Cosmos, Container Apps env, ACR, control-plane Key Vault (incl. **CA root**).
- [ ] Image build + SHA-tag → ACR; OIDC deploy to `dev`.
- [ ] GHAS + Trivy gates on.
- **DoD:** empty API + console deploy to `dev` via CI; Cosmos + ACR + KV provisioned reproducibly.

### M2 — Control plane API + catalog (E5.1) `[R1]`
- [ ] Cosmos repo (`store`) + containers (§5); ETag concurrency.
- [ ] Entra JWT middleware; `platform.admin` / `tenant.admin` + tenant-scope authz.
- [ ] Publisher endpoints (release, graduate, entitle, suspend); `catalog/` seed + schema validation.
- [ ] Tenant endpoints (`/tenant/catalog`, `/tenant/desired`).
- **DoD:** a Platform Admin releases a version + entitles a tenant; a Tenant Admin reads their slice and writes desired state (via API/tests).

### M3 — Enrollment + reconciler core (E5.2 + E1) `[R1]`
- [ ] `internal/ca` + `/enroll` + `/enroll/renew`; `enrollTokens` single-use; `certThumbprints` on tenant doc.
- [ ] mTLS ingress + client-cert middleware for `/sync/*`.
- [ ] Reconciler loop: Sync (ETag) → Foundry REST client → create/update/delete agent → `ensureModelDeployment`
      → `ensureConnections` → assign agent-identity RBAC → heartbeat.
- [ ] Local last-known-good cache; quota-aware failure surfacing.
- [ ] Run against the **M0 hand-created Foundry** (no managed app yet).
- **DoD:** an enabled agent in desired state is created/updated/removed in a real Foundry by the reconciler, authenticated by cert; heartbeats populate `/platform/fleet`.

### M4 — Managed application package (E5.3 + E11) `[R1]`
- [ ] `mainTemplate.bicep` (reconciler CA-app + Foundry + KV + MI + Storage + App Insights + role assignments).
- [ ] `createUiDefinition.json`; wire `/tenant/install` → deploy URL + `{token, cpUrl}`.
- [ ] Reconciler self-update from advertised image SHA; managed-app definition versioning.
- [ ] Install into the **test customer subscription**; complete enroll end-to-end.
- **DoD:** clicking **Install** in the console deploys the app into a real subscription, it enrolls, and reconciles the first agent.

### M5 — Console slice (E6) `[R1]`
- [ ] MSAL login; role/tenant-driven routing.
- [ ] Tenant Admin: catalog browse, light enable (publish targets + knowledge binding), Install button, status page.
- [ ] Platform Admin: release + canary, entitlements, fleet dashboard.
- **DoD:** a non-developer completes purchase-less onboarding: log in → install → enable → agent answers over the API endpoint.

### M6 — Spine basics: E2 / E3 / E8 / E9 `[R1]`
- [ ] E2: automate model deployment (quota-aware); optional per-tenant APIM GenAI gateway behind a flag.
- [ ] E3: provision AI Search + Document Intelligence in-tenant; wire one agent to agentic retrieval.
- [ ] E8: App Insights (control plane + reconciler + Foundry traces); SLOs + alerts; per-tenant showback from heartbeats.
- [ ] E9 basics: Conditional Access, least-privilege MI, Key Vault + CMK, Defender, GHAS.
- **DoD (R1 exit):** one **production** tenant runs an agent end-to-end with knowledge, SLOs/alerting, and enforced security gates.

---

## 12. R2/R3 implementation notes

- **R2 — Enterprise + buyable:** full E5 (lifecycle: suspend/offboard/GDPR delete), E4 (Teams/M365 guided
  publish + Logic Apps connectors), E6 (builder UI), E7 (Foundry Evaluations CI gates), full E9 (Sentinel,
  Purview, pentest), **E10** (Marketplace SaaS offer + Managed App offer, metered dimension, dual certification).
- **R3 — Scale + differentiate:** domain skill packs, **Toolbox** (when GA) replacing per-agent connections,
  hosted agents (container-packaged), fine-tuning, air-gapped/Arc for disconnected estates, and the
  **WIF migration** for secretless reconciler auth (retire per-tenant certs).

---

## 13. Implementation decisions (I1–In)

| # | Decision | Rationale |
|---|---|---|
| I1 | **Reconciler calls Foundry via REST**, not an SDK | No official Go Foundry-agents SDK; REST is documented/GA and keeps one language. |
| I2 | **Control plane is its own CA** (self-signed root in KV) | Avoids per-tenant Entra app-registration sprawl; simple issue/validate/revoke. |
| I3 | **`shared/` Go module** imported by API + reconciler | Single source of truth for models + Foundry client. |
| I4 | **Three auth chains** (Entra JWT / mTLS / one-time token) | Distinct trust for admins vs machines vs enrollment. |
| I5 | **Cosmos ETag optimistic concurrency** | Safe concurrent desired-state edits + cheap Sync change detection. |
| I6 | **Quota-aware reconcile with UI surfacing** | Model quota is the #1 install-time failure. |
| I7 | **Reconciler self-update via advertised image SHA** | Update in-tenant software without managed-RG access. |
| I8 | **Definition-owned agents (prompt / hosted)** | Substance lives in the versioned definition, not per-tenant config. See AGENT-MODEL.md. |
| I9 | **Integration tests hit real Foundry** | Foundry behavior (identity, publish, quota) can't be mocked faithfully. |

---

## 14. Open technical questions

Resolve in M0 or early M1–M3:

1. **New-model agent CRUD `api-version` GA?** (D4) — determines exact REST endpoints/shapes.
2. **Container Apps client-cert (mTLS) fidelity** — if ingress mTLS is limiting, fall back to signed client
   assertions at `/sync` (§6).
3. **Cross-tenant behavior of the reconciler MI** — the MI is in the customer tenant; confirm it only ever
   needs an `ai.azure.com` token *within* that tenant (it does — Foundry is local) and never a token for our tenant.
4. **Managed-app permissions mode** (D13) — customer-managed vs publisher-managed; affects update + escape hatch.
5. **Per-tenant APIM cost/benefit** — default off in v1; confirm which advanced gateway features (if any) a design partner requires.
6. **Model default + region availability** — Foundry / AI Search / Speech in the target region(s) before R1 locks.
