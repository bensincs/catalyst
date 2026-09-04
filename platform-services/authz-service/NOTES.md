# authz-service

Extracted from the Cortex platform chart
(`platform/charts/cortex-platform-0.1.0/cortex-platform/templates/authz-service.yaml`
in `cortex-post-mvp`), which shipped it as one 2187-line template alongside the
rest of the platform. `app/` is the service's own source, vendored here so it can
be changed — it is to become single-tenant.

## What was left behind

The legacy credential-rotation jobs, the in-cluster-to-managed Postgres
migration jobs, the Cilium L7 policies and the cert-manager Certificate for the
local Postgres. All of those exist to carry an already-running Cortex
installation forward; a first install has nothing to migrate from.

## What changed

**The datastore is Azure Database for PostgreSQL, not a pod.** It was in-cluster
Postgres while the service was being lifted out, which is fine for a throwaway
stamp and wrong for anything else: the authorization store is where every access
decision is ultimately resolved, so it should not live on a pod's ephemeral
disk. The password is read from the tenant's own Key Vault by ARM, so the
control plane never handles it.

**`app/` builds standalone.** `cortex-otel` was a path dependency two levels up
in the other repository; it is vendored under `app/libs/` and the lockfile and
Dockerfile repointed at it.

## Things that cost time

**The in-cluster Postgres could not start.** The official image's entrypoint
chowns its data directory and chmods `/var/run/postgresql` as root before
stepping down, and the cluster's admission policy requires every capability be
dropped — so both failed with "Operation not permitted". Running as the postgres
user from the outset avoids that path entirely. Moot now the datastore is
managed, but the same trap applies to any stock image assuming it starts as
root.

**SpiceDB will not serve against an unmigrated database.** It starts, fails to
read its own `metadata` table and exits — the error names a missing relation
rather than a missing migration. The migration now runs as an init container on
the SpiceDB pod, so it is tied to the thing that depends on it and cannot drift.

**The schema-load job carried both `helm.sh/hook` and `argocd.argoproj.io/hook`
annotations, and ran under neither.** SpiceDB came up with no schema, and the
service answered `/ready` with a 500 that said nothing about why. Argo-native
annotations only.

## Still to do

The service is single-tenant-to-be but currently multi-tenant in shape, and
nothing yet injects `x-cortex-sub`/`-tenant`/`-app` into requests reaching an
application. In Cortex that is EnvoyGateway's `SecurityPolicy` calling
`/v1/ext-authz` and forwarding the headers on an allow. This platform fronts
apps with oauth2-proxy instead, which has no ext-authz — so Insight still
receives no identity it recognises.
