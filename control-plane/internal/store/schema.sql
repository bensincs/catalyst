-- Cortex control-plane schema (Postgres). Idempotent: safe to run on every boot.

CREATE TABLE IF NOT EXISTS tenants (
  id                  text PRIMARY KEY,               -- stable slug
  name                text NOT NULL,
  tenant_id           text UNIQUE,                    -- Entra directory id (tid)
  region              text NOT NULL DEFAULT '—',
  plan                text NOT NULL DEFAULT 'team',
  enrollment          text NOT NULL DEFAULT 'pending',
  version             text NOT NULL DEFAULT '',
  agent_count         int    NOT NULL DEFAULT 0,
  reconciling_count   int    NOT NULL DEFAULT 0,
  monthly_calls       bigint NOT NULL DEFAULT 0,
  drift               int    NOT NULL DEFAULT 0,
  last_heartbeat      timestamptz,
  subscription_id     text NOT NULL DEFAULT '',
  reconciler_identity text NOT NULL DEFAULT '',
  foundry_project     text NOT NULL DEFAULT '',
  reconciler_version  text NOT NULL DEFAULT '',
  installed_at        text NOT NULL DEFAULT '',
  is_platform         boolean NOT NULL DEFAULT false,
  created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agents (
  id           text PRIMARY KEY,                       -- slug: <tenant>:<agent>
  tenant_slug  text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  agent_id     text NOT NULL,
  name         text NOT NULL,
  version      text NOT NULL,
  channel      text NOT NULL DEFAULT 'stable',
  model        text NOT NULL,
  health       text NOT NULL DEFAULT 'reconciling',
  publish_to   text[] NOT NULL DEFAULT '{}',
  calls_30d    bigint NOT NULL DEFAULT 0,
  note         text NOT NULL DEFAULT '',
  sort_order   int NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS agents_tenant_idx ON agents(tenant_slug);

CREATE TABLE IF NOT EXISTS users (
  oid         text PRIMARY KEY,
  tid         text NOT NULL,
  name        text NOT NULL DEFAULT '',
  email       text NOT NULL DEFAULT '',
  role        text NOT NULL DEFAULT 'tenant',
  tenant_slug text,
  last_login  timestamptz NOT NULL DEFAULT now()
);

-- ── Catalog (the publisher's versioned agent definitions) ──────────────────
CREATE TABLE IF NOT EXISTS catalog_agents (
  id          text PRIMARY KEY,               -- slug
  name        text NOT NULL,
  description text NOT NULL DEFAULT '',
  type        text NOT NULL DEFAULT 'prompt',  -- prompt | hosted (AGENT-MODEL.md)
  model       text NOT NULL DEFAULT 'gpt-4o',
  created_by  text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS catalog_versions (
  id              text PRIMARY KEY,           -- <agent>:<version>
  agent_id        text NOT NULL REFERENCES catalog_agents(id) ON DELETE CASCADE,
  version         text NOT NULL,
  channel         text NOT NULL DEFAULT 'stable',
  notes           text NOT NULL DEFAULT '',
  rollout_percent int  NOT NULL DEFAULT 100,
  definition      jsonb NOT NULL DEFAULT '{}', -- the versioned agent definition
  created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS catalog_versions_agent_idx ON catalog_versions(agent_id);

-- Which catalog agents a tenant is entitled to enable.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS entitled_agents text[] NOT NULL DEFAULT '{}';

-- Access gate: only enabled tenants may sign in to the console/API or run a
-- reconciler. Existing tenants stay enabled (default true); new tenants are
-- created DISABLED (pending platform approval) by EnsureTenantForTID.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS enabled boolean NOT NULL DEFAULT true;

-- Tenant-level health was replaced by the derived lifecycle (enrollment +
-- heartbeat freshness), computed in Go. Per-agent health lives on agents.
ALTER TABLE tenants DROP COLUMN IF EXISTS health;

-- Agent model (AGENT-MODEL.md): two types + a versioned definition.
ALTER TABLE catalog_agents   ADD COLUMN IF NOT EXISTS type text NOT NULL DEFAULT 'prompt';
ALTER TABLE catalog_versions ADD COLUMN IF NOT EXISTS definition jsonb NOT NULL DEFAULT '{}';

-- ── Memory stores (platform-authored + tenant-created) ─────────────────────
-- A memory store is a first-class Foundry memory_store resource (kind "default")
-- that agents connect to. Its definition — the chat + embedding model
-- deployments and which memory kinds are extracted — is modeled as typed
-- columns (never an opaque JSON blob). Platform-authored stores
-- (owner_tenant = '') are granted to tenants via entitlements; tenant-created
-- stores (owner_tenant = <slug>) are private to their tenant.
CREATE TABLE IF NOT EXISTS memory_stores (
  id                        text PRIMARY KEY,               -- slug
  name                      text NOT NULL,
  description               text NOT NULL DEFAULT '',
  owner_tenant              text NOT NULL DEFAULT '',        -- '' = platform-authored; else tenant slug
  chat_model                text NOT NULL DEFAULT 'gpt-4o',              -- Foundry chat deployment
  embedding_model           text NOT NULL DEFAULT 'text-embedding-3-small', -- Foundry embedding deployment
  user_profile_enabled      boolean NOT NULL DEFAULT true,
  user_profile_details      text    NOT NULL DEFAULT '',
  chat_summary_enabled      boolean NOT NULL DEFAULT true,
  procedural_memory_enabled boolean NOT NULL DEFAULT true,
  ttl_seconds               int     NOT NULL DEFAULT 0,       -- 0 = never expire
  created_by                text NOT NULL DEFAULT '',
  created_at                timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS memory_stores_owner_idx ON memory_stores(owner_tenant);

-- Migrate legacy stores off the opaque `config` jsonb blob to typed definition
-- columns (real Foundry memory-store schema).
ALTER TABLE memory_stores ADD COLUMN IF NOT EXISTS chat_model                text    NOT NULL DEFAULT 'gpt-4o';
ALTER TABLE memory_stores ADD COLUMN IF NOT EXISTS embedding_model           text    NOT NULL DEFAULT 'text-embedding-3-small';
ALTER TABLE memory_stores ADD COLUMN IF NOT EXISTS user_profile_enabled      boolean NOT NULL DEFAULT true;
ALTER TABLE memory_stores ADD COLUMN IF NOT EXISTS user_profile_details      text    NOT NULL DEFAULT '';
ALTER TABLE memory_stores ADD COLUMN IF NOT EXISTS chat_summary_enabled      boolean NOT NULL DEFAULT true;
ALTER TABLE memory_stores ADD COLUMN IF NOT EXISTS procedural_memory_enabled boolean NOT NULL DEFAULT true;
ALTER TABLE memory_stores ADD COLUMN IF NOT EXISTS ttl_seconds               int     NOT NULL DEFAULT 0;
ALTER TABLE memory_stores DROP COLUMN IF EXISTS config;

-- Which platform memory stores a tenant is entitled to. Auto-extended when a
-- tenant is entitled to (or enables) an agent that references a store.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS entitled_stores text[] NOT NULL DEFAULT '{}';

-- Per-tenant override: an enabled agent may connect to a store the tenant owns
-- or is entitled to ('' = inherit the catalog definition's memoryStore).
ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_store text NOT NULL DEFAULT '';

-- ── Unified ownership + lifecycle (agents ⇔ memory stores) ─────────────────
-- Agents, like memory stores, can be authored by the platform (owner_tenant = '')
-- or by a tenant (owner_tenant = <slug>, private to that tenant). Platform-owned
-- catalog agents are shared with tenants via entitlements; tenant-owned ones are
-- private to their owner.
ALTER TABLE catalog_agents ADD COLUMN IF NOT EXISTS owner_tenant text NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS catalog_agents_owner_idx ON catalog_agents(owner_tenant);

-- A tenant explicitly ENABLES a memory store (one it owns or is entitled to),
-- mirroring how it enables an agent. The row is the store's per-tenant instance:
-- the reconciler provisions the store into the tenant's Foundry project and
-- reports its lifecycle back (reconciling → live → blocked), so a store has the
-- same reconcile status an agent does. Enabling an agent that references a store
-- auto-enables that store here.
CREATE TABLE IF NOT EXISTS tenant_stores (
  tenant_slug text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  store_id    text NOT NULL,
  health      text NOT NULL DEFAULT 'reconciling',   -- reconciling | live | blocked
  auto        boolean NOT NULL DEFAULT false,          -- true = auto-enabled via an agent reference
  sort_order  int  NOT NULL DEFAULT 0,
  PRIMARY KEY (tenant_slug, store_id)
);
CREATE INDEX IF NOT EXISTS tenant_stores_tenant_idx ON tenant_stores(tenant_slug);

-- Standardize the per-agent lifecycle vocabulary on 'live' (was 'healthy'), so
-- agents, memory stores, and tenants all speak the same words.
UPDATE agents SET health = 'live' WHERE health = 'healthy';

-- Backfill: enable (as auto rows) every store already bound by an enabled agent,
-- so moving to explicit store enablement doesn't strand existing agent memory.
-- Idempotent + also enforces the invariant "a referenced store is enabled".
INSERT INTO tenant_stores (tenant_slug, store_id, health, auto)
SELECT DISTINCT a.tenant_slug, sid.store_id, 'reconciling', true
FROM agents a
CROSS JOIN LATERAL (
  SELECT coalesce(nullif(a.memory_store, ''),
                  (SELECT v.definition->>'memoryStore' FROM catalog_versions v
                   WHERE v.agent_id = a.agent_id ORDER BY v.created_at DESC LIMIT 1)) AS store_id
) sid
WHERE sid.store_id IS NOT NULL AND sid.store_id <> ''
  AND EXISTS (SELECT 1 FROM memory_stores m WHERE m.id = sid.store_id)
ON CONFLICT (tenant_slug, store_id) DO NOTHING;

-- ── Kubernetes / GitOps ────────────────────────────────────────────────────
-- The reconciler provisions (via the managed-app Bicep) an AKS cluster per
-- tenant, bootstraps Argo CD into it, and reports the cluster's status here.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS cluster_name           text    NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS cluster_phase          text    NOT NULL DEFAULT '';   -- provisioning | ready | unreachable
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS cluster_k8s_version    text    NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS cluster_argo_installed boolean NOT NULL DEFAULT false;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS cluster_node_count     int     NOT NULL DEFAULT 0;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS cluster_detail         text    NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS cluster_ingress_installed boolean NOT NULL DEFAULT false;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS cluster_gateway_ip     text    NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS cluster_ingress_issuer text    NOT NULL DEFAULT '';

-- A Helm deployment a tenant runs in its cluster. The reconciler stamps each as
-- an Argo CD Application (Helm source); Argo CD installs the chart and keeps it
-- converged. sync_status/health_status mirror Argo's own vocabulary, reported
-- back via the heartbeat.
CREATE TABLE IF NOT EXISTS applications (
  id              text PRIMARY KEY,                     -- slug: <tenant>-<name>
  tenant_slug     text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  name            text NOT NULL,
  namespace       text NOT NULL DEFAULT 'default',
  repo_url        text NOT NULL DEFAULT '',              -- Helm repo (https) or OCI (oci://)
  chart           text NOT NULL DEFAULT '',
  target_revision text NOT NULL DEFAULT '',              -- chart version
  values          text NOT NULL DEFAULT '',              -- inline Helm values (YAML)
  sync_status     text NOT NULL DEFAULT 'pending',       -- Synced | OutOfSync | Unknown | pending
  health_status   text NOT NULL DEFAULT 'pending',       -- Healthy | Progressing | Degraded | pending
  sort_order      int  NOT NULL DEFAULT 0,
  created_by      text NOT NULL DEFAULT '',
  created_at      timestamptz NOT NULL DEFAULT now()
);

-- ── Deployments as catalog entities (unified with agents ⇔ memory stores) ────
-- A deployment (Helm chart) is authored by the platform (owner_tenant = '') or a
-- tenant (owner_tenant = <slug>, private), entitled to tenants, and explicitly
-- ENABLED per tenant (tenant_deployments) — exactly like an agent or memory
-- store. Only then does the reconciler stamp it as an Argo CD Application and
-- report the per-tenant sync/health lifecycle (reconciling → live → blocked)
-- back via the heartbeat.
ALTER TABLE applications ADD COLUMN IF NOT EXISTS owner_tenant text NOT NULL DEFAULT '';
ALTER TABLE applications ADD COLUMN IF NOT EXISTS description  text NOT NULL DEFAULT '';
-- Azure infra + wiring + dependencies. bicep holds an OCI Bicep-module reference
-- (e.g. br:acr.azurecr.io/bicep/db:1.0.0); the control plane bakes bicep_params
-- into it and resolves it to an ARM template (arm_template) + output names
-- (bicep_outputs) at save. wiring maps those outputs → Helm values paths;
-- depends_on gates deploy order.
ALTER TABLE applications ADD COLUMN IF NOT EXISTS bicep         text    NOT NULL DEFAULT '';
ALTER TABLE applications ADD COLUMN IF NOT EXISTS arm_template  text    NOT NULL DEFAULT '';
ALTER TABLE applications ADD COLUMN IF NOT EXISTS bicep_params  jsonb   NOT NULL DEFAULT '{}';
ALTER TABLE applications ADD COLUMN IF NOT EXISTS bicep_outputs text[]  NOT NULL DEFAULT '{}';
ALTER TABLE applications ADD COLUMN IF NOT EXISTS wiring        jsonb   NOT NULL DEFAULT '[]';
ALTER TABLE applications ADD COLUMN IF NOT EXISTS depends_on    text[]  NOT NULL DEFAULT '{}';
ALTER TABLE tenants      ADD COLUMN IF NOT EXISTS entitled_deployments text[] NOT NULL DEFAULT '{}';

CREATE TABLE IF NOT EXISTS tenant_deployments (
  tenant_slug   text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  app_id        text NOT NULL,
  health        text NOT NULL DEFAULT 'reconciling',   -- reconciling | live | blocked (derived)
  sync_status   text NOT NULL DEFAULT 'pending',        -- Argo: Synced | OutOfSync | Unknown | pending
  health_status text NOT NULL DEFAULT 'pending',        -- Argo: Healthy | Progressing | Degraded | pending
  sort_order    int  NOT NULL DEFAULT 0,
  PRIMARY KEY (tenant_slug, app_id)
);
CREATE INDEX IF NOT EXISTS tenant_deployments_tenant_idx ON tenant_deployments(tenant_slug);
ALTER TABLE tenant_deployments ADD COLUMN IF NOT EXISTS infra_state text NOT NULL DEFAULT '';  -- '' | provisioning | ready | failed
ALTER TABLE tenant_deployments ADD COLUMN IF NOT EXISTS infra_outputs jsonb NOT NULL DEFAULT '{}'; -- resolved ARM outputs (control-plane provisioned)
ALTER TABLE tenant_deployments ADD COLUMN IF NOT EXISTS waiting     boolean NOT NULL DEFAULT false; -- held for unmet dependencies

-- One-time migration from the legacy tenant-private applications model: adopt the
-- old tenant_slug as the owner, enable the app for that tenant (carrying its
-- runtime status), then retire the tenant_slug column + index.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_name = 'applications' AND column_name = 'tenant_slug') THEN
    UPDATE applications SET owner_tenant = tenant_slug
      WHERE owner_tenant = '' AND tenant_slug <> '';
    INSERT INTO tenant_deployments (tenant_slug, app_id, sync_status, health_status, sort_order)
      SELECT tenant_slug, id,
             coalesce(sync_status, 'pending'), coalesce(health_status, 'pending'), sort_order
      FROM applications WHERE tenant_slug <> ''
      ON CONFLICT DO NOTHING;
    ALTER TABLE applications DROP COLUMN tenant_slug;
  END IF;
END $$;

CREATE INDEX IF NOT EXISTS applications_owner_idx ON applications(owner_tenant);


