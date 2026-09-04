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

## Applications bring their own roles

An application declares `roles` in its registration — its own vocabulary for
describing a person. Insight declares `org_admin`, `mg_admin`, `member` and
`guest`, which is exactly what its `role_utils.ORG_ROLE_PRIORITY` uses.

Nothing about this service appears in an application's manifest. The
registration hook here asks for `{{role}}`, and the reconciler calls it once per
role the application declared. A hook that does not mention `{{role}}` is about
the application itself and is called once, not multiplied. An application that
declares no roles still gets one (`user`), so it stays grantable rather than
becoming invisible to whoever manages access.

Left over: every application registered before this carries a `user` role that
its code may never refer to — Insight has one. There is no route that deletes a
role, so these cannot be tidied up through the API.

## Production posture

Verified against the running tenant rather than assumed:

- **Fails closed.** When SpiceDB is unreachable `/v1/authz/decide` answers 503
  with `allowed: false`. Answering "allow" would hand out access precisely when
  the service knows nothing about the caller.
- **The data plane does not depend on Entra.** `decide` verifies no tokens, so
  an identity-provider outage stops new logins but does not fail requests
  already in flight. Only `/v1/ui/*` verifies OIDC.
- **Resources are adequate.** Measured 62Mi against a 512Mi limit with no
  restarts and no OOM kills — roughly eight times headroom. Left alone.
- **Both workloads survive a drain.** Two replicas each, spread across nodes,
  with a PodDisruptionBudget keeping one serving. SpiceDB previously ran as a
  single replica while answering every authenticated request in the tenant.

### Known gaps

- **NetworkPolicy is not enforced anywhere on this cluster.** AKS reports
  `networkPolicy: none`, so no policy object has any effect. The policies
  already shipped in the Insight chart are decorative. Enabling an engine on an
  existing cluster is disruptive, so this is a deliberate decision to take
  rather than something to quietly add. Until then, treat every in-cluster
  caller as able to reach this Service directly — which is why identity comes
  from a verified token and never from the gateway's headers.
- **There is one administrator.** The last-administrator guard now prevents
  removing them, but a single account is still fragile. Add a second.
- **The database is a single point of failure.** `authz-3760de3866` is
  Burstable with high availability disabled and seven days of backups. Zone
  redundancy needs the General Purpose tier — a cost decision.
- Every application registered before roles were declarable still carries a
  `user` role. Insight's has been removed; `authz-admin` keeps its own, left
  alone deliberately because the sole administrator's access runs through it.
