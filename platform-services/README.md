# Platform services

Services the platform itself publishes and offers to tenants — as opposed to the
control plane (`control-plane/`), the in-tenant agent (`reconciler/`) and the
console (`web/`), which are how the platform *runs*.

A platform service is something a tenant can end up running in its own cluster
and subscription. Each one lives in its own directory here and ships three
artifacts, all published as OCI:

| Artifact | Published to | Registered in the catalog as |
|---|---|---|
| container image | `images/<name>` | referenced by the chart's values |
| Helm chart | `charts/<name>` | an **application** |
| Bicep module | `bicep/<name>` | **infrastructure** |

They are separate on purpose. Infrastructure is provisioned by the control plane
into the tenant's subscription and reports its outputs back; the application is
stamped into the tenant's cluster as an Argo CD Application, with those outputs
wired into its Helm values. A service that needs no Azure resources ships no
Bicep module; one that is pure infrastructure ships no chart.

## Layout

```
platform-services/<service>/
  app/      source + Dockerfile + tests   → the container image
  chart/    Helm chart                    → the application
  infra/    Bicep module                  → the infrastructure
  README.md what it is and how it is registered
```

## Secrets do not travel in values

A credential is never written into a chart's values, and never passed as a Bicep
parameter. Values are copied verbatim into the Argo Application, and a Bicep
parameter is baked into the ARM template and kept in Azure's deployment history
permanently — both are readable by anyone with the relevant read access.

Instead a chart takes an `existingSecret` (a NAME) and reads the credential from
a Kubernetes Secret at runtime. The platform declares which keys exist as a
**secret store**; each tenant supplies its own values, which go to that tenant's
own Key Vault and are delivered into the cluster by the reconciler. See
`datamodel.md`.

A module that needs a credential should generate it rather than accept one.
