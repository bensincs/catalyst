# Cortex — Lighthouse onboarding & the infra/data-plane split

**Status:** Design + verification spike (no product code changed yet)
**Decision:** Provision Azure **infrastructure from the control plane via Azure Lighthouse**; keep a
**thin in-tenant reconciler for the data-plane** (Foundry agents/memory + in-cluster GitOps).

This supersedes the earlier "move app Bicep infra to the control plane" note — that becomes **step one** of
this onboarding architecture.

---

## 1. The deciding fact: control-plane vs data-plane

Azure Lighthouse projects a managing tenant's principals onto a customer's **Azure Resource Manager
(control-plane) RBAC**. It does **not** delegate **data-plane** access. Straight from the docs
([cross-tenant management experience](https://learn.microsoft.com/en-us/azure/lighthouse/concepts/cross-tenant-management-experience)):

> Azure Lighthouse supports requests handled by Azure Resource Manager (`https://management.azure.com`).
> It doesn't support requests handled by an instance of a resource type, such as Key Vault secrets access
> or storage data access… These are typically **data operations rather than management operations**.

That single boundary decides the whole architecture:

| Work | Plane | Who does it |
|---|---|---|
| AKS cluster, networking, storage, Key Vault, **the reconciler container app**, managed identities | ARM control-plane | **Control plane** (Lighthouse) |
| Each deployment's **Bicep/ARM infra** | ARM control-plane | **Control plane** (Lighthouse) |
| Foundry **agents / memory stores** | **data-plane** | **Reconciler** (in-tenant identity) |
| Argo CD / Helm inside the cluster | k8s API (data-plane) | **Reconciler** (in-tenant) |

Lighthouse explicitly supports "deploy and manage clusters in customer tenants" (AKS) and "deploy and
manage Azure Virtual Network… within managed tenants", so everything in the top rows is in scope. The
bottom rows are data-plane and stay with the reconciler — which is exactly why the reconciler doesn't
disappear.

## 2. Target architecture

```
                       ┌──────────────────────────────────────────────┐
   Platform tenant     │  Cortex control plane (SaaS)                  │
   (Inception42)       │   • Postgres desired/actual state            │
                       │   • NEW: Azure ARM client (platform SP)       │
                       │   • NEW: infra reconcile worker               │
                       └───────────────┬──────────────────────────────┘
                                       │  ARM control-plane, cross-tenant
                                       │  (token from platform tenant,
                                       │   authorized by Lighthouse)
                       ┌───────────────▼──────────────────────────────┐
   Customer tenant     │  Delegated RG: cortex-infra                   │
   (per tenant)        │   • app Bicep infra (storage, KV, Postgres…)  │
                       │                                               │
                       │  Cluster RG (in-tenant)                       │
                       │   • AKS + Envoy ingress (Argo pulls)          │
                       │   • Reconciler container app + user MI ───────┼─▶ Foundry (data-plane)
                       └──────────────────────────────────────────────┘
```

- **Control plane** holds a platform-tenant **service principal**. Via Lighthouse it deploys each app's
  resolved ARM into the customer's delegated `cortex-infra` RG, reads outputs, computes the wiring, and
  bakes the resolved Helm values + `InfraState` into the desired state it already serves.
- **Reconciler** (unchanged in spirit) uses its **own in-tenant user-assigned managed identity** for the
  data-plane: Foundry agents/memory + stamping Argo Applications. It **no longer touches ARM/Bicep infra**.

## 3. Onboarding flow

One customer action: **deploy the Cortex onboarding template** into their subscription (portal
"Deploy to Azure" / `az deployment sub create`). That single subscription-scoped deployment does two
things:

1. **In-tenant footprint** (today's `reconciler/infra/main.bicep`): AKS, the reconciler container app, its
   user-assigned managed identity, and the identity's **data-plane** role grants (Foundry, AKS). The
   identity **no longer needs `Contributor` for infra**.
2. **Lighthouse delegation** (`onboarding/lighthouse-delegation.bicep`): a
   `registrationDefinition` + RG-scoped `registrationAssignment` granting the **control-plane SP**
   `Contributor` on the dedicated `cortex-infra` resource group.

After that, enrollment is just: reconciler heartbeats in **and** the delegation is visible to the platform
(`az account list` shows the customer sub under `managedByTenants`).

> The existing Entra admin-consent to the multi-tenant Cortex app is still required — but only for console
> sign-in + the ingress JWT, as today. It is **not** needed for Foundry in this split, because the
> reconciler's own managed identity does that in-tenant.

## 4. Delegation specifics (verified against docs)

- **Built-in roles only.** Lighthouse authorizations *cannot* use custom roles, `Owner`, or any role with
  `DataActions`. So the grant is the built-in **Contributor** (`b24988ac-6180-42a0-ab88-20f7382dd24c`),
  scoped to the `cortex-infra` RG. (My earlier "RG-scoped custom role" suggestion was wrong — corrected
  here.) Least privilege comes from **RG scoping**, not a custom role.
- **Role assignments to the app infra.** `Contributor` cannot create RBAC assignments. If a module needs to
  grant a role (e.g. a managed identity `Storage Blob Data Contributor`), add the limited **User Access
  Administrator** to the delegation with `delegatedRoleDefinitionIds` — the *only* supported UAA use
  (assigning roles to managed identities). Prefer designing app modules to **not** require role
  assignments.
- **No managed-application lock.** The current onboarding is a plain deployable Bicep + `createUiDefinition`
  (not a `Microsoft.Solutions` managed app), so there is **no system deny-assignment** blocking the managing
  tenant. Good — do not convert it to a managed application, or Lighthouse would be denied on those
  resources.
- **National clouds:** delegation across a national cloud boundary isn't supported (same-cloud only).
- **Visibility:** Lighthouse assignments don't appear in the customer's IAM / `az role assignment list`;
  they're under **Delegations** or the Lighthouse API.

## 5. What changes in code (later — not in this spike)

Control plane (new):
- An **Azure client + credential** (`azidentity`, platform SP) — the control plane has none today.
- Store **`cluster_infra_resource_group`** per tenant (we already store `subscription_id`).
- An **infra reconcile worker**: for each enabled deployment, `PUT` the resolved `arm_template` to
  `…/subscriptions/{customerSub}/resourceGroups/cortex-infra/providers/Microsoft.Resources/deployments/…`,
  poll, read outputs, `applyWiring`, persist `InfraState` + wired values.
- Serve the **already-wired Helm values** + `InfraState` in `DesiredState`.

Reconciler (shrinks):
- Delete `provisionInfra` / `submitDeployment` / `deploymentOutputs` / `applyWiring` (the ARM path).
- Keep Foundry + Argo/Helm. Honor `InfraState` (hold the chart until `ready`) — the dep-hold machinery
  already exists.

Shared/contract: `DesiredApplication.Values` arrives pre-wired; `InfraState` set by the control plane.

## 6. Security & sovereignty tradeoffs (be explicit)

- **Standing cross-tenant access.** The platform SP holds continuous `Contributor` on each customer's
  `cortex-infra` RG. That's a bigger trust surface than today (where only the tenant's own MI acts). Scope
  to a **dedicated RG**, keep the SP credential in a platform Key Vault / workload identity, and consider
  **PIM-JIT** activation for the SP rather than standing access.
- **Blast radius / scale.** The control plane now performs N tenants' ARM deploys centrally — one component
  holding many delegations. Needs queueing, per-tenant isolation, and careful audit (Lighthouse activity is
  logged in the customer's Activity Log with the managing identity).
- **The data-plane stays in-tenant**, which preserves the strongest part of the "runs in your tenant"
  story for the sensitive bits (Foundry).

## 7. Verification — the spike

**Question to answer before refactoring:** *can a principal authenticated in the managing (platform) tenant
create an ARM deployment in the customer's delegated `cortex-infra` RG, and read its outputs, using only
managing-tenant credentials?* If yes, the whole control-plane-provisions-infra design holds.

Artifacts in `onboarding/`:

- **`lighthouse-delegation.bicep`** + **`lighthouse-assignment.bicep`** — deploy in a *stand-in customer*
  subscription to delegate a `cortex-infra` RG to your platform SP.
- **`verify-crosstenant-deploy.sh`** — signs in as the **managing** principal and deploys a real AVM module
  (the storage account we already use) into the delegated RG, then prints its outputs. Success = deployment
  succeeds + outputs returned **without ever authenticating to the customer tenant**.

Run order (see `onboarding/README` steps inside the script):

```bash
# 1. As the CUSTOMER admin (owns the stand-in sub): delegate cortex-infra (creates the RG too).
az deployment sub create --subscription <CUSTOMER_SUB> --location uksouth \
  --template-file onboarding/lighthouse-delegation.bicep \
  --parameters controlPlaneTenantId=<PLATFORM_TENANT> controlPlanePrincipalId=<PLATFORM_SP_OBJECT_ID>

# 2. As the MANAGING principal (platform SP): prove cross-tenant deploy.
./onboarding/verify-crosstenant-deploy.sh <CUSTOMER_SUB> cortex-infra
```

**What "pass" looks like:**
- `az account list` (as the managing principal) shows `<CUSTOMER_SUB>` with a `managedByTenants` entry.
- The deployment reaches `Succeeded` and outputs (`resourceId`, `primaryBlobEndpoint`, …) print.
- You never ran `az login` against the customer tenant.

**What would fail / block the design:** the deployment 403s (delegation/role wrong), or the target resource
type isn't creatable cross-tenant (none of our infra types are data-plane, so this isn't expected).

## 8. Open decisions

1. **PIM-JIT vs standing** for the platform SP's Contributor.
2. **Which RG** — a dedicated `cortex-infra` (recommended) vs the cluster RG (simpler, broader).
3. **Infra worker placement** — in the control-plane API process vs a separate worker service.
4. **Wiring secrets** — this also relocates the "secret outputs land inline in Helm values" concern to the
   control plane; pairs with the still-open Key Vault/Secret wiring hardening.
5. **Rollout** — dual-run (reconciler still capable) during migration, then delete the reconciler ARM path.
