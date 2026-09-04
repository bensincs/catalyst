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

## The configuration is not persistent

The configuration is seeded from a ConfigMap and copied into an `emptyDir` at
startup, because the UI writes changes back to the file it was given and a
ConfigMap mounts read-only — mounted directly, every save in the UI fails.

The consequence: **changes made in the UI are lost when the pod restarts.** The
durable place to change configuration is `chart/templates/configmap.yaml`.

This is also why there is one replica and a `Recreate` strategy. Two replicas
would keep separate configurations behind one Service, and the UI would show
different state depending on which pod answered.

There is no PodDisruptionBudget, deliberately. With a single replica,
`minAvailable: 1` permits no disruption at all, which blocks node drains and
stalls cluster upgrades.

## Authentication

`authRequired` puts oauth2-proxy and Oathkeeper in front, so the UI is
unreachable without a completed Entra login. Upstream strongly recommends
exactly this when the UI is exposed off the admin interface — its own admin
surface has no authentication, which is why `adminAddr` stays on localhost.

The Entra redirect URI is **not** automated by the platform.
`https://agentgateway.apps.msft.ae/oauth2/callback` was added by hand to app
`6eb003c0-169c-4b12-bc5e-aa5d6af04f2b`.

## No infrastructure

Standalone agentgateway keeps its state in a file and needs no database, cache
or object store, so nothing is provisioned. It gains dependencies only when
something is configured that has them — a global rate limit needs Redis, and
request logging to Postgres needs a database. Add them when a feature is turned
on, not before.

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
