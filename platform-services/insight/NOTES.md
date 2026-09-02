# Bringing InSight onto the platform — what was needed, and what blocked

A record of the work, kept because several of the decisions are not obvious from
the result and one of them is a wall.

## The blocker: the application images

**The nine first-party InSight images cannot be reached from this environment.**
Everything else here works; this does not, and no amount of platform work fixes
it.

What was tried:

| Source | Result |
|---|---|
| `acrcortex2dev001`, `acrcortex2prod001` | not present in any subscription reachable from here |
| `acrcortex2customer1001` | reachable, and **empty** — zero repositories |
| `ghcr.io/cortex-inception/cortex/insight-saas-*` | 404 for every path tried |
| local source checkout | none — `../insight` is an unrelated Teams app |

`acrcortex2customer1001` needed two temporary changes to inspect at all
(public network access, and its export policy, which blocks `az acr import`).
**Both were restored to exactly their original values**, and the two role
assignments granted to do it were removed.

So the platform side is built and provable; the application cannot start until
its images exist somewhere reachable. `register.json` points `imageRegistry` at
the platform registry — change it, or import the images there, and the service
runs unchanged.

## What the platform was missing

InSight needs far more than the todo app, and most of it was genuinely absent.

### Private networking

Nearly every resource it provisions refuses public access, so it needs ~12
private endpoints and a private DNS zone per service type. The footprint created
neither.

Added to the footprint:
- a private-endpoint subnet in the cluster's own virtual network
  (`footprint-network.bicep`, scoped to the AKS node resource group, because
  that is where AKS puts the network and an endpoint must sit in a subnet of the
  network the pods are on)
- eight private DNS zones, each linked to that network — without the link a zone
  resolves nothing, and the resource's public FQDN would not resolve to its
  private address from inside the cluster

**The vnet name cannot be computed.** AKS derives it (`aks-vnet-15641340`) by
means that are not `uniqueString` of anything available, so guessing it produced
a module pointed at a network that does not exist. The control plane now looks
it up and passes it in. It is empty on a first stamp — the cluster does not
exist yet — so the private networking appears on the next sweep. That is why the
footprint is re-submitted rather than stamped once.

### Getting tenant facts into a module

A module cannot enumerate the subscription it is deployed into, so three more
deploy-time tokens were added alongside `{{tenantHash}}` and `{{vaultName}}`:

```
{{peSubnetId}}  {{aksOidcIssuerUrl}}  {{dnsZoneResourceGroup}}
```

The eight DNS zone ids are **derived** in the module rather than passed. Their
names are fixed by Azure — a private endpoint's DNS zone group only resolves
against those exact names — so eight parameters would have been eight chances to
get one wrong.

### External Secrets Operator

The InSight chart ships its own `SecretStore` and `ExternalSecret` resources,
which do nothing without that controller. Cortex's own secret stores do not need
it (the reconciler writes those Secrets itself), but a vendor chart that brings
its own does. Registered as a platform service in its own right, pulled straight
from the upstream Helm repository — it is public, and a mirror is a copy to keep
current for no benefit.

## The credential path

The interesting part, and the reason the module looks the way it does.

```
tenant types 4 values
   -> secret store  -> the TENANT's Key Vault      (control plane cannot read them back)
   -> module reads them as Key Vault REFERENCES    (never as parameters)
   -> writes the 6 contract secrets the chart expects
   -> chart's ExternalSecrets read them via workload identity
   -> pods
```

Nothing sensitive is in the catalog, the control plane's database, the ARM
template, or the deployment history. The compiled module has **no secure
parameters at all** — verified, not assumed.

Two things forced the shape:

- `vault.getSecret()` is only valid as a **module parameter**. It compiles to a
  reference resolved at deploy time, never to a value, so a caller cannot
  interpolate one into a string. The three Postgres connection strings therefore
  had to move into `modules/contract-secrets.bicep`, where the parameters are
  real values and can be composed.
- The chart's `ExternalSecret` remote keys are hardcoded to six specific names.
  Rather than fork a vendor chart, the module republishes the tenant's four
  values under those names.

## The wire

One line does the whole configuration handoff:

```json
{ "sourceKind": "infrastructure", "sourceId": "insight-infra",
  "output": "appInfra", "helmPath": "appInfra" }
```

`appInfra` is an **object** — 28 keys plus a nested block — and this is the first
time wiring has carried anything but a scalar. Confirmed by test
(`objwire_test.go`) before relying on it: `applyWiring` merges it as a map rather
than flattening or stringifying it.

## Deliberately reduced for a dev deployment

The vendor's defaults are production-shaped and slow/expensive to stand up.
Lowered in `register.json`, not in the module, so the defaults stay honest:
`GP_Standard_D2ds_v5` and 32 GB Postgres, 7-day backups, no geo-redundancy,
one search replica, standard-tier Key Vault.

## Still to prove

Everything up to the images. When a reachable image source exists, the remaining
unknowns are the chart's own behaviour: whether the ExternalSecrets bind, whether
the ten services come up in the right order, and whether the frontend serves.

## Provisioning run — findings

Deployment reached **13 of ~20 resources in swedencentral**. Four defects were
found and fixed, two blockers remain.

### Fixed

1. **Every private endpoint failed with the cluster vnet "not found".** The
   application-infrastructure resource group is created in the *provisioner's*
   region (uaenorth); this tenant's cluster is in swedencentral. A private
   endpoint must be in the same region as its subnet, so the reference resolved
   to nothing. `{{region}}` is now the tenant's own region.

2. **Postgres rejected `GP_Standard_D2ds_v5`.** This API version wants a bare
   SKU name with the tier passed separately, and the module hardcoded
   `GeneralPurpose`, so a Burstable SKU could never have been selected. The tier
   is now a parameter.

3. **A failed deployment locked its own Key Vault name.** The module hardcoded
   purge protection, so the vault created by the first (wrong-region) attempt is
   recoverable-only until 2026-09-09 and cannot be recreated in the right
   region. Purge protection is now a parameter (still defaulting to true).

4. **`enablePurgeProtection: false` is rejected outright** — "cannot be set to
   false ... irreversible". The property must be *absent*, not disabled, so it
   is now `purgeProtection ? true : null`.

### Blocked

5. **Azure Cache for Redis cannot be created at all** in this subscription:
   "Azure Cache for Redis is retiring, create Azure Managed Redis instead".
   Premium *and* Standard are refused. Note `az deployment group validate`
   returns success for these templates — the block only appears on a real
   create, so validation cannot be trusted here.

   Moving to Azure Managed Redis is not a SKU swap. It is a different resource
   type (`Microsoft.Cache/redisEnterprise` plus a `databases` child), a
   different private-endpoint group id, port 10000, different auth — and it
   needs a private DNS zone the platform does not create:
   `privatelink.<region>.redisenterprise.cache.azure.net`. The footprint's zone
   list (`onboarding/footprint.bicep`) is flat and region-independent, so this
   is a **platform footprint change**, not just a module change.

6. **The nine first-party InSight images still do not exist anywhere
   reachable**, so the application cannot run even once its infrastructure is
   complete. See above.

### Operational note

The control plane's Postgres server (`cortex-cp-pg-6hy6uurw`) was found
**Stopped** — Azure flexible servers auto-stop after 7 days idle. Symptoms were
misleading: the API kept serving reads from an already-running revision while
every write (PATCH) hung until the 240s ingress cap, and a newly deployed
revision crash-looped on "startup probe failed". Worth a health check that
distinguishes "cannot reach the database" from "starting".
