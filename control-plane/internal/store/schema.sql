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


