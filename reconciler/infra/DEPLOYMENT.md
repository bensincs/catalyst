# Cortex reconciler — direct deployment

The reconciler normally ships as an **Azure Marketplace Managed Application**
([`main.bicep`](./main.bicep) + [`createUiDefinition.json`](./createUiDefinition.json)):
a customer installs it into their own subscription and it runs in their tenant.

This guide deploys the **same `main.bicep` directly** into a subscription with the
Azure CLI — no Marketplace offer required. Useful for dev, evaluation, sovereign
installs, or bootstrapping a tenant before the offer is published.

```
 customer subscription (one per tenant)
 ┌───────────────────────────────────────────────────────────────┐
 │  cortex-reconciler  (Container App, no ingress)                │
 │        │  identity token (Foundry User)                         │
 │        ▼                                                        │
 │  cortex-ai-<hash>  (Foundry account) / agents-prod  (project)  │
 │        ├─ gpt-4o                  (chat model deployment)      │
 │        └─ text-embedding-3-small  (embedding — memory stores)  │
 └───────────────────────────────────────────────────────────────┘
          │  identity token (CORTEX_API_SCOPE), tid → tenant
          ▼
   https://api.catalyst.msft.ae   (Cortex control plane, /recon/*)
```

Everything is **inferred or defaulted** except your organization name: region comes
from the resource group, `tenantId`/`subscriptionId` from the deployment context, the
Foundry account name from a hash of the resource group, and the project endpoint is
derived from that name.

| Resource | Name | Notes |
| --- | --- | --- |
| Log Analytics | `cortex-recon-logs` | Container App logs |
| User-assigned identity | `cortex-recon` | the reconciler's Entra identity |
| Foundry account (AI Services) | `cortex-ai-<hash>` | `kind: AIServices`, `allowProjectManagement`, Entra-only (`disableLocalAuth`) |
| Foundry project | `agents-prod` | where agents are converged |
| Model deployment | `gpt-4o` | what agents run on |
| Embedding deployment | `text-embedding-3-small` | required by memory stores to index memories |
| Role assignment | — | identity → **Foundry User** on the account (the `AIServices/agents/*` data plane) |
| Managed environment | `cortex-recon-env` | Container Apps env |
| Container app | `cortex-reconciler` | outbound-only worker, 1 replica |

---

## Prerequisites

- **Azure CLI** with Bicep: `az upgrade && az bicep upgrade`
- The **target subscription**, and rights to create resources **and role
  assignments** — Owner, or Contributor **+** Role Based Access Control
  Administrator (the template assigns a role, Step 3).
- **Model quota**: `gpt-4o` `GlobalStandard` capacity in your region (default 30k
  TPM). Override `modelName`/`modelVersion`/`modelSkuName`/`modelCapacity` if your
  region or quota differs, or point at a region that has it.
- **Embedding quota**: `text-embedding-3-small` `Standard` capacity (default 30k
  TPM) — memory stores can't be created without an embedding deployment. Override
  `embeddingModelName`/`embeddingModelVersion`/`embeddingSkuName`/`embeddingCapacity`
  to match your region.
- The **Cortex Entra app** must be provisioned (admin-consented) in the target
  tenant so the reconciler identity can obtain Cortex API tokens — this is the
  same multi-tenant app from the control plane
  ([`../../control-plane/infra/DEPLOYMENT.md`](../../control-plane/infra/DEPLOYMENT.md)
  Step 1). No per-reconciler secret is needed; auth is identity-based.
- A registry the Container App can pull the reconciler image from (Step 2).

```bash
az login
az account set --subscription "<CUSTOMER_SUBSCRIPTION_ID>"

export RG="cortex-reconciler"
export LOCATION="eastus2"            # a region with Foundry + your model
az group create -n "$RG" -l "$LOCATION"
```

---

## 2. Build & push the reconciler image

The image builds **from the repo root** (it needs the shared module + root
`go.mod`) using [`reconciler/Dockerfile`](../Dockerfile). The container app has no
registry credentials, so the simplest path is a registry it can pull anonymously.

```bash
export ACR="cortexrecon$RANDOM"     # globally-unique name
az acr create -g "$RG" -n "$ACR" --sku Basic
az acr update  -n "$ACR" --anonymous-pull-enabled true

# build in the cloud (no local Docker needed), from the repo root:
az acr build -r "$ACR" -t cortex-reconciler:0.1.0 -f reconciler/Dockerfile .

export RECONCILER_IMAGE="$ACR.azurecr.io/cortex-reconciler:0.1.0"
```

> For a **private** registry instead, grant the `cortex-recon` identity `AcrPull`
> and add a `registries` block to the container app (see [Hardening](#hardening)).

---

## 3. Deploy

`tenantName` is the only required parameter. Everything else has a default; pass
`reconcilerImage` from Step 2 (unless you're using a published public image).

```bash
az deployment group create \
  -g "$RG" -f reconciler/infra/main.bicep \
  -p tenantName="<Your Org Name>" \
  -p reconcilerImage="$RECONCILER_IMAGE"
```

Common overrides:

```bash
  -p controlPlaneUrl="https://api.catalyst.msft.ae" \   # default
  -p cortexApiScope="api://33e1686e-d227-454a-9974-4978c567720b" \   # default
  -p foundryProjectName="agents-prod" \
  -p modelName="gpt-4o" -p modelVersion="2024-11-20" -p modelCapacity=30
```

The template assigns a role, so it can take a minute for the identity's principal
to replicate before the assignment succeeds; a re-run is idempotent if it races.

> **Backing-only / run the reconciler elsewhere.** Add `-p deployReconcilerApp=false`
> to deploy just the Foundry account/project/model + identity + RBAC and skip the
> in-Azure container app (and its image). Then run the reconciler wherever you like —
> e.g. locally with `FOUNDRY_PROJECT_ENDPOINT` set to the `foundryProjectEndpoint`
> output. Grant *your* principal **Foundry User** on the account too (the template
> only grants the managed identity):
>
> ```bash
> az role assignment create --assignee "$(az ad signed-in-user show --query id -o tsv)" \
>   --role "Foundry User" \
>   --scope "$(az deployment group show -g "$RG" -n main --query properties.outputs.foundryAccountName.value -o tsv | xargs -I{} az cognitiveservices account show -g "$RG" -n {} --query id -o tsv)"
> ```
> Data-plane RBAC can take several minutes to propagate before the API returns 200.

---

## 4. Verify

Read the outputs, then watch the reconciler converge:

```bash
az deployment group show -g "$RG" -n main --query properties.outputs -o json
# foundryProjectEndpoint, foundryAccountName, reconcilerClientId, reconcilerPrincipalId

# reconciler logs — expect "cortex reconciler starting" then "reconciled ..."
az containerapp logs show -g "$RG" -n cortex-reconciler --follow --tail 50
```

What healthy looks like:

- The log lines `reconciled desired=<n> healthy=<n>` each poll interval.
- The tenant appears in the Cortex **Fleet** (`https://catalyst.msft.ae`) on the
  first heartbeat — no pre-registration needed; the heartbeat enrolls it.
- Any **prompt** agents entitled to this tenant show up as agents in the Foundry
  project (portal → your `cortex-ai-<hash>` account → `agents-prod`). Agents must
  reference a model that is deployed here — the template deploys `gpt-4o`, so an
  agent with `model: gpt-4o` runs as-is; other models need matching deployments.

> **Hosted (bring-your-own-container) agents** are a separate compute path and are
> reported `blocked` by this reconciler build — only prompt agents converge into
> Foundry today.

---

## How auth works (no shared secrets)

The reconciler presents its **user-assigned managed identity** everywhere:

- **To the control plane** — a token for `CORTEX_API_SCOPE`. The API validates it
  against Entra's JWKS and maps the token's `tid` to the tenant. There is no
  enrollment key.
- **To Foundry** — a token for `https://ai.azure.com/.default`, authorized by the
  **Foundry User** role this template grants on the account (the GA rename of "Azure
  AI User"). The Agent Service checks the
  `Microsoft.CognitiveServices/accounts/AIServices/agents/*` data action for agent
  create/update/delete, which this role's `Microsoft.CognitiveServices/*` covers.
  Note the OpenAI-family roles (Cognitive Services OpenAI User's
  `OpenAI/assistants/*`) do **not** — the GA Agent Service left that namespace.

---

## Configuration reference

Parameters (`main.bicep`) — all optional except `tenantName`:

| Param | Default | Purpose |
| --- | --- | --- |
| `tenantName` | — (required) | org display name in the Fleet |
| `location` | resource group's | region for all resources |
| `controlPlaneUrl` | `https://api.catalyst.msft.ae` | Cortex API base |
| `cortexApiScope` | `api://33e1686e-…` | Entra scope for the Cortex API |
| `plan` | `enterprise` | plan tier reported |
| `foundryAccountName` | `cortex-ai-<hash>` | Foundry account (globally unique) |
| `foundryProjectName` | `agents-prod` | project agents converge into |
| `modelName` / `modelVersion` | `gpt-4o` / `2024-11-20` | model for agents |
| `modelSkuName` / `modelCapacity` | `GlobalStandard` / `30` | deployment throughput |
| `embeddingModelName` / `embeddingModelVersion` | `text-embedding-3-small` / `1` | embedding model for memory stores |
| `embeddingSkuName` / `embeddingCapacity` | `Standard` / `30` | embedding deployment throughput |
| `reconcilerImage` | `ghcr.io/inception42/cortex-reconciler:latest` | container image |
| `reconcilerVersion` | `0.1.0` | version reported to the control plane |
| `pollIntervalSeconds` | `30` | reconcile + heartbeat interval |
| `deployReconcilerApp` | `true` | set `false` to deploy only the Foundry backing (account/project/model + identity + RBAC) and run the reconciler elsewhere — e.g. locally |

Env injected into the container maps 1:1 to `reconciler/internal/config/config.go`
(`CONTROL_PLANE_URL`, `CORTEX_API_SCOPE`, `TENANT_ID`, `FOUNDRY_PROJECT_ENDPOINT`,
`AZURE_CLIENT_ID`, …). `FOUNDRY_API_VERSION` (`v1`, the Foundry Agents API) and
`FOUNDRY_SCOPE` (`https://ai.azure.com/.default`) fall back to safe defaults in code.

---

## Updating

Rebuild the image and roll it onto the running app — not by re-running the template
(a full template PUT is fine here since there are no CLI-bound custom domains, but
`update` is faster):

```bash
az acr build -r "$ACR" -t cortex-reconciler:0.1.1 -f reconciler/Dockerfile .
az containerapp update -g "$RG" -n cortex-reconciler \
  --image "$ACR.azurecr.io/cortex-reconciler:0.1.1"
```

---

## Hardening

The template optimizes for a clean first deployment. For production:

- **Private registry** — instead of anonymous pull, grant `cortex-recon` the
  `AcrPull` role on the registry and add a `registries` block to the container app
  referencing the identity.
- **Least-privilege role** — the template uses **Foundry User**, whose broad
  `Microsoft.CognitiveServices/*` data actions include the agents plane. For tighter
  scope, define a custom role granting only
  `Microsoft.CognitiveServices/accounts/AIServices/agents/*` and swap
  `foundryUserRoleId`.
- **Private networking** — VNet-inject the managed environment and give the Foundry
  account a private endpoint (drop `publicNetworkAccess`).
- **Pin the image** to an immutable tag (`:<git-sha>`) for auditable rollouts.

---

## Teardown

```bash
az group delete -n "$RG" --yes --no-wait
```

Deleting the resource group removes the reconciler, the Foundry account/project,
the model deployment, and the role assignment. The tenant remains in the Cortex
Fleet until removed from the control plane (it simply stops heartbeating).
