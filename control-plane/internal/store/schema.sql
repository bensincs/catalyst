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
-- Azure Lighthouse delegation reachability, probed by the control-plane infra worker.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS infra_delegated boolean NOT NULL DEFAULT false;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS infra_detail    text    NOT NULL DEFAULT '';
-- Per-tenant footprint (reconciler + Foundry + AKS) provisioned by the control plane via Lighthouse.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS footprint_state  text NOT NULL DEFAULT ''; -- '' | provisioning | ready | failed
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS footprint_detail text NOT NULL DEFAULT '';
-- One-shot request (platform admin) to re-submit the footprint template over an
-- already-provisioned tenant, so footprint changes (config fixes, new features)
-- reach existing tenants. Consumed + cleared by the provisioner's next sweep.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS footprint_reprovision boolean NOT NULL DEFAULT false;

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

-- ── Split: Infrastructure (Bicep/Azure) vs Applications (Helm) ──────────────
-- A "deployment" used to be one row that bundled a Helm chart AND its Azure
-- infra (Bicep). They are now two first-class catalog entities with a typed
-- dependency graph:
--     infrastructure → infrastructure
--     application    → infrastructure | application | agent
--     agent          → memory store
-- Each is authored (platform owner_tenant='' or a tenant), entitled, and enabled
-- per tenant. Infrastructure is provisioned cross-tenant by the control plane
-- (ARM via Lighthouse); applications are stamped as Argo CD Applications by the
-- reconciler, with a dependency infrastructure's outputs wired into their values.

CREATE TABLE IF NOT EXISTS infrastructure (
  id            text PRIMARY KEY,                 -- slug
  name          text NOT NULL,
  description   text NOT NULL DEFAULT '',
  owner_tenant  text NOT NULL DEFAULT '',          -- '' = platform-authored; else tenant slug
  bicep         text NOT NULL DEFAULT '',          -- OCI Bicep-module ref (br:acr.../bicep/db:1.0.0)
  arm_template  text NOT NULL DEFAULT '',          -- resolved ARM template (baked at save)
  bicep_params  jsonb  NOT NULL DEFAULT '{}',       -- author-supplied module params
  bicep_outputs text[] NOT NULL DEFAULT '{}',       -- resolved module output names (for wiring)
  dependencies  jsonb  NOT NULL DEFAULT '[]',        -- [{kind,id}] — infrastructure deps only
  created_by    text NOT NULL DEFAULT '',
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS infrastructure_owner_idx ON infrastructure(owner_tenant);
-- pending_delete: the definition is being deleted (the UI shows it "Deleting");
-- it's removed once its last provisioned instance has been torn down.
ALTER TABLE infrastructure ADD COLUMN IF NOT EXISTS pending_delete boolean NOT NULL DEFAULT false;

-- Per-tenant infrastructure enablement/instance (mirrors tenant_deployments).
-- The control-plane infra worker provisions it and reports state + resolved
-- outputs back here; applications that depend on it wire those outputs in.
CREATE TABLE IF NOT EXISTS tenant_infrastructure (
  tenant_slug   text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  infra_id      text NOT NULL,
  infra_state   text NOT NULL DEFAULT '',          -- '' | provisioning | ready | failed
  infra_outputs jsonb NOT NULL DEFAULT '{}',         -- resolved ARM outputs
  health        text NOT NULL DEFAULT 'reconciling', -- reconciling | live | blocked (derived)
  auto          boolean NOT NULL DEFAULT false,       -- true = auto-enabled via an application dependency
  sort_order    int  NOT NULL DEFAULT 0,
  PRIMARY KEY (tenant_slug, infra_id)
);
CREATE INDEX IF NOT EXISTS tenant_infrastructure_tenant_idx ON tenant_infrastructure(tenant_slug);

-- pending_delete: the instance is being torn down in Azure (the UI shows it
-- "Deprovisioning"); the control-plane provisioner deletes the resources its ARM
-- deployment created, then removes the row. Replaces the old teardown queue —
-- keeping the row lets the UI show the removal in progress rather than vanishing.
ALTER TABLE tenant_infrastructure ADD COLUMN IF NOT EXISTS pending_delete boolean NOT NULL DEFAULT false;
DROP TABLE IF EXISTS infra_teardowns;

-- Which platform infrastructure entities a tenant is entitled to enable.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS entitled_infrastructure text[] NOT NULL DEFAULT '{}';

-- Auto-enable flag on deployments (mirrors tenant_stores/tenant_infrastructure):
-- true = enabled as a dependency of another enabled entity, pruned when no longer needed.
ALTER TABLE tenant_deployments ADD COLUMN IF NOT EXISTS auto boolean NOT NULL DEFAULT false;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS auto boolean NOT NULL DEFAULT false;

-- Typed dependency edges on an application: [{kind,id}] where kind ∈
-- {infrastructure, application, agent}. Replaces the untyped depends_on text[].
ALTER TABLE applications ADD COLUMN IF NOT EXISTS dependencies jsonb NOT NULL DEFAULT '[]';
-- Gateway exposure: the in-cluster Service the app publishes that the ingress
-- routes to (empty ⇒ cluster-internal, no Ingress), + its port.
ALTER TABLE applications ADD COLUMN IF NOT EXISTS expose_service text NOT NULL DEFAULT '';
ALTER TABLE applications ADD COLUMN IF NOT EXISTS expose_port    int  NOT NULL DEFAULT 80;

-- One-time migration: split each application's Bicep half into a first-class
-- `infrastructure` row, make the application depend on it, and re-point its
-- wiring at that infrastructure id; carry any per-tenant infra state onto
-- tenant_infrastructure; convert the untyped depends_on into typed dependencies.
DO $$
DECLARE r RECORD; new_infra text; wl jsonb; dep jsonb; deps jsonb; d text;
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
             WHERE table_name='applications' AND column_name='bicep') THEN
    FOR r IN SELECT * FROM applications LOOP
      deps := '[]'::jsonb;
      -- Bicep half → infrastructure entity + an application→infrastructure edge.
      IF coalesce(r.bicep,'') <> '' OR coalesce(r.arm_template,'') <> '' THEN
        new_infra := r.id || '-infra';
        INSERT INTO infrastructure (id, name, description, owner_tenant, bicep, arm_template, bicep_params, bicep_outputs, created_by, created_at)
          VALUES (new_infra, r.name || ' infrastructure', r.description, r.owner_tenant,
                  coalesce(r.bicep,''), coalesce(r.arm_template,''), coalesce(r.bicep_params,'{}'::jsonb),
                  coalesce(r.bicep_outputs,'{}'), r.created_by, r.created_at)
          ON CONFLICT (id) DO NOTHING;
        deps := deps || jsonb_build_object('kind','infrastructure','id',new_infra);
        -- Re-point wiring entries at the new infrastructure id.
        IF coalesce(r.wiring,'[]'::jsonb) <> '[]'::jsonb THEN
          UPDATE applications SET wiring = (
            SELECT coalesce(jsonb_agg(w || jsonb_build_object('infrastructure', new_infra)), '[]'::jsonb)
            FROM jsonb_array_elements(r.wiring) w
          ) WHERE id = r.id;
        END IF;
        -- Carry any per-tenant infra state onto tenant_infrastructure + enable it.
        INSERT INTO tenant_infrastructure (tenant_slug, infra_id, infra_state, infra_outputs, sort_order)
          SELECT td.tenant_slug, new_infra, coalesce(td.infra_state,''), coalesce(td.infra_outputs,'{}'), td.sort_order
          FROM tenant_deployments td WHERE td.app_id = r.id
          ON CONFLICT (tenant_slug, infra_id) DO NOTHING;
        -- Entitle tenants entitled to the app to the split-out infrastructure too.
        UPDATE tenants SET entitled_infrastructure =
          (SELECT coalesce(array_agg(DISTINCT e),'{}') FROM unnest(entitled_infrastructure || ARRAY[new_infra]) e)
          WHERE r.id = ANY(entitled_deployments);
      END IF;
      -- Untyped depends_on → typed dependencies (kind inferred from where the id lives).
      IF r.depends_on IS NOT NULL THEN
        FOREACH d IN ARRAY r.depends_on LOOP
          IF EXISTS (SELECT 1 FROM applications WHERE id = d) THEN
            deps := deps || jsonb_build_object('kind','application','id',d);
          ELSIF EXISTS (SELECT 1 FROM catalog_agents WHERE id = d) THEN
            deps := deps || jsonb_build_object('kind','agent','id',d);
          END IF;
        END LOOP;
      END IF;
      UPDATE applications SET dependencies = deps WHERE id = r.id;
    END LOOP;
    -- Retire the bundled Bicep columns + untyped depends_on from applications.
    ALTER TABLE applications DROP COLUMN IF EXISTS bicep;
    ALTER TABLE applications DROP COLUMN IF EXISTS arm_template;
    ALTER TABLE applications DROP COLUMN IF EXISTS bicep_params;
    ALTER TABLE applications DROP COLUMN IF EXISTS bicep_outputs;
    ALTER TABLE applications DROP COLUMN IF EXISTS depends_on;
  END IF;
END $$;

-- ── Platform-hosted tenants (same platform subscription, per-tenant RG) ─────
-- A tenant can be hosted in the platform's OWN subscription (hosting_mode =
-- 'platform') instead of a customer's Lighthouse-delegated one ('delegated', the
-- default). Platform-hosted tenants have no distinct Entra directory
-- (tenant_id NULL) — users are assigned to them explicitly (memberships) and
-- their reconciler is identified by its pre-created managed-identity principal
-- (reconciler_principal_id) rather than the token's directory id. resource_group
-- + region are the per-tenant deploy target the control plane provisions into.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS hosting_mode            text NOT NULL DEFAULT 'delegated';
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS resource_group          text NOT NULL DEFAULT '';
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS reconciler_principal_id text NOT NULL DEFAULT '';

-- tenant_id is the Entra directory id for delegated tenants (one tenant per
-- directory) but NULL for platform-hosted ones (which share the platform
-- directory). Replace the plain UNIQUE with a partial unique index so only
-- delegated tenants are constrained to one-per-directory.
ALTER TABLE tenants ALTER COLUMN tenant_id DROP NOT NULL;
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'tenants_tenant_id_key') THEN
    ALTER TABLE tenants DROP CONSTRAINT tenants_tenant_id_key;
  END IF;
END $$;
CREATE UNIQUE INDEX IF NOT EXISTS tenants_tenant_id_uidx
  ON tenants (tenant_id) WHERE tenant_id IS NOT NULL AND hosting_mode = 'delegated';

-- Explicit user → tenant assignment. Delegated tenants still derive access from
-- the token's directory id; platform-hosted tenants (whose users all share the
-- platform directory) are accessed only through a membership. A membership is
-- created by email; the user's Entra oid is bound on first sign-in. role is
-- 'admin' for now (membership == full tenant access; room to grow).
CREATE TABLE IF NOT EXISTS memberships (
  tenant_slug text NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  email       text NOT NULL,
  oid         text,
  role        text NOT NULL DEFAULT 'admin',
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_slug, email)
);
CREATE INDEX IF NOT EXISTS memberships_oid_idx   ON memberships(oid);
CREATE INDEX IF NOT EXISTS memberships_email_idx ON memberships(lower(email));


