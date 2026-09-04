# agentgateway

One gateway for LLM, MCP and agent-to-agent traffic. Registered as a platform
service; the admin UI is published at `agentgateway.apps.msft.ae` behind the
platform's login.

## Why standalone, and not upstream's Kubernetes mode

agentgateway ships two deployment modes. The Kubernetes one is built on the
Gateway API, with a controller and four CRDs of its own. **That mode cannot be
installed through this platform as it is configured today**, and it is worth
being precise about why rather than rediscovering it later.

Argo applies resources client-side, which records the whole manifest in the
`kubectl.kubernetes.io/last-applied-configuration` annotation. Annotations are
capped at 262144 bytes. Three of agentgateway's CRDs are far past that:

| CRD | bytes |
| --- | --- |
| `agentgatewaybackends` | 1,041,093 |
| `agentgatewaypolicies` | 627,771 |
| `agentgatewaymodels` | 309,320 |

Server-side apply is the normal answer and is exactly what upstream's install
instructions use (`kubectl apply --server-side --force-conflicts`). It is
deliberately switched off here — Argo v2.13 against Kubernetes 1.35 fails the
structured-merge diff on `.status.terminatingReplicas`, every application goes
`Unknown`, and auto-sync stops. `TestApplicationDoesNotUseServerSideApply` pins
that.

This is the same wall external-secrets hit. Upgrading Argo is the prerequisite
for both.

Standalone mode has no CRDs and no controller, so none of this applies. If the
Kubernetes mode is ever wanted, the Argo upgrade comes first.

## What was learned by trying it

Three assumptions were wrong, each caught by running the image rather than
reasoning about it:

- **It will not start in Kubernetes without a configuration file.** Outside a
  cluster it writes itself a default one and the docs lean on that. Inside a
  cluster it exits: `configuration is required when running in Kubernetes`.
- **The image is distroless.** No shell, no `cp`. The init container that puts
  the configuration in place therefore uses busybox, mirrored into the platform
  registry.
- **The UI is not exposed by default.** It is served only on the admin
  interface, which listens on localhost. It reaches the platform's port only
  because the config attaches it to a named gateway.

## The database, and why the configuration needs one

agentgateway reads its configuration file as a **baseline**. With
`config.storage.mode: hybrid` everything managed from the UI is written to a
database overlay instead of back to the file, so the two never compete: the
ConfigMap says what the platform deploys, the database holds what an operator
has since changed.

Without a database the UI still works, but its changes are written to the file
in the pod — lost on restart, and never agreed on by two replicas. The database
is therefore what makes the UI worth having, and what allows more than one
replica to run at all. It holds the request log too, which is where the token
and cost accounting comes from.

Because the UI no longer writes to the file, the ConfigMap is mounted directly
and read-only. In `file` mode that same mount makes every save in the UI fail.

`config.storage.mode` is **nested under `config`**, not top level. At the top
level agentgateway rejects it outright and lists the fields it does accept —
`storage` is not among them, in either v1.4.1 or v1.5.0.

Turning the database off is supported (`database.enabled: false`), but then
`replicas` must be 1: the chart falls back to a `Recreate` strategy so two pods
holding different configurations never overlap, and drops the
PodDisruptionBudget, which against a single replica would permit no disruption
at all and block node drains.

## Known: the database password is printed to the pod log

agentgateway echoes its fully-resolved configuration at startup, and the
resolved value includes the database URL **with the password in it**. It appears
twice in the first few seconds of every pod's log.

The credential is only assembled in the environment — it is never written into
the ConfigMap, and `/api/config` returns the unsubstituted
`$AGENTGATEWAY_DATABASE_URL` rather than the resolved value. The startup echo is
the one place it escapes, and it is upstream behaviour rather than anything this
chart does.

Rotating does not fix it: a new password is echoed the same way on the next
start. What limits it is the network. The server has
`publicNetworkAccess: Disabled` and is reachable only through its private
endpoint, so the credential is useless without access inside the tenant's own
virtual network. Anyone who can read pod logs there could already reach the
database.

Worth raising upstream. Until then, treat these pod logs as sensitive and keep
them out of any aggregator with a wider audience than the cluster.

## Authentication

`authRequired` puts oauth2-proxy and Oathkeeper in front, so the UI is
unreachable without a completed Entra login. Upstream strongly recommends
exactly this when the UI is exposed off the admin interface — its own admin
surface has no authentication, which is why `adminAddr` stays on localhost.

The Entra redirect URI is **not** automated by the platform.
`https://agentgateway.apps.msft.ae/oauth2/callback` was added by hand to app
`6eb003c0-169c-4b12-bc5e-aa5d6af04f2b`.

## Testing against a real cluster

Two things caught me out and are worth knowing before repeating this.

The **published JSON schema describes the newest release**, not whichever
version you are running. `config.storage` was designed against it and then
rejected by v1.4.1, which is what prompted the move to v1.5.0. Check the version
you actually deploy.

**Argo tracks ownership by the `app.kubernetes.io/instance` label**, which Helm
sets from the release name. Rendering this chart with `helm template
agentgateway` into a scratch namespace produced resources carrying the same
label as the real application, and Argo pruned them within seconds — the objects
were reported created and then simply vanished. Use a different release name
when testing.

## Registering it

A platform service is not visible to a tenant until two separate things happen,
in two different roles:

1. **Entitle** it (platform role): `PATCH /api/tenants/{slug}/all-entitlements`.
   This **replaces the whole set**, so read the current entitlements first and
   send them back with the addition — omitting one silently withdraws it.
2. **Enable** it (tenant role): `POST /api/resources/application/{id}/enable`.

Switching between the two means toggling `PLATFORM_ADMIN_EMAILS` on
`cortex-cp-api` and waiting about a minute. Setting it demotes you to the tenant
role; removing it restores the platform role.

## What is not configured yet

Nothing is routed through it. It is installed, healthy and reachable, with no
LLM providers, MCP targets or traffic listeners defined — those are decisions
about what it should carry, not about whether it runs.
