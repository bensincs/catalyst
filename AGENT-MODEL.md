# Cortex Agent Model — canonical standard

> Locked. This is the authoritative definition of what an "agent" is in Cortex and how
> it flows from catalog → tenant → reconciler → Foundry. It supersedes any earlier
> "schema-driven per-tenant config" notion. When agent code or copy disagrees with this
> document, this document wins.

## 1. Two agent types (mirrors Microsoft Foundry)

Every catalog agent is exactly one **type**, fixed at authoring time and immutable:

### `prompt` — declarative agent (no code)
The **definition is the agent**. It is pure configuration the reconciler applies to a
Foundry prompt/declarative agent:

- `model` — model deployment (e.g. `gpt-4o`)
- `instructions` — the system prompt
- `tools[]` — built-in Foundry tools: `file_search`, `code_interpreter`,
  `function` (function calling), `web` (web grounding), …
- `knowledge[]` — publisher-provided grounding sources baked into the definition
- `params` — `temperature`, `topP`, `maxTokens`, …

The reconciler **creates/updates a Foundry prompt agent** from this definition.

### `hosted` — bring-your-own-code agent (container)
The definition points at a **built artifact** the publisher ships:

- `image` — container image reference (the publisher's built agent)
- `endpoint` — path/port the agent serves on
- `resources` — `cpu` / `memory`
- `env[]` — non-secret runtime configuration (secrets come from the tenant's Key Vault,
  referenced by name, never held by Cortex)

The reconciler **deploys the container** into the tenant's own Foundry/compute and wires
its endpoint.

## 2. The definition owns the substance (publisher, versioned)

The **catalog agent version carries the full definition** — a snapshot authored by the
publisher. This is the core principle: the substance of an agent lives in its definition,
not in per-tenant configuration.

- Authoring an agent: choose `type` (immutable) + name + description.
- Publishing a **version**: edit the definition (a new prompt / tools / image is a new
  version). `channel` (`stable` | `beta`) + `rolloutPercent` gate availability — they do
  **not** auto-apply.
- There is **no per-tenant schema-driven config form.** Tenants do not author definitions.

## 3. Tenant scope is light (enable)

Enabling an entitled agent is deliberately thin — the definition already carries the agent:

- **Publish targets** — `api` / `teams` / `m365` (where it's reachable).
- Nothing else is tenant-configurable. Sovereignty holds: models, knowledge, containers,
  and secrets stay in the tenant's subscription.

> **Deferred:** per-tenant **knowledge binding** — a prompt agent grounding on the tenant's
> *own* data source (an Azure AI Search index, etc.). It's a real future enhancement but is
> out of scope until real Foundry grounding is wired; it would add exactly one optional field
> at enable time. Not part of the model today.

## 4. Reconciliation — desired vs. actual

Per enabled agent, **desired state** = `{ type, version, definition, publishTo }`. The
reconciler converges the tenant's Foundry to it and reports **actual**.

- **Drift** = the desired definition (its version) ≠ the actual converged one. This now
  covers version bumps *and* any definition change (prompt, tools, image, params).
- The agent drill-in's desired→actual diff surfaces it: `converging → in sync`, exactly the
  pattern already built for version drift, now definition-aware.

## 5. Concrete data model

| Store | Change |
|---|---|
| `catalog_agents` | `+ type text` (`prompt` \| `hosted`), immutable |
| `catalog_versions` | `+ definition jsonb` — the versioned definition payload |
| `agents` (enabled) | type/version/definition resolved from catalog; publish targets on the row |
| `shared.DesiredAgent` | `+ Type`, `+ Definition` |

The reconciler reads `type` + `definition` from desired state and provisions the matching
Foundry object. Real Foundry provisioning is stubbed today; the stub honors `type` +
`definition` and converges on any change.

## 6. UI

- **Catalog authoring:** pick `type` on create; version-publish edits the typed definition
  (prompt: instructions / model / tools / knowledge / params — hosted: image / endpoint /
  resources / env).
- **Enable:** publish targets.
- **Agent drill-in:** a **Prompt / Hosted** type badge, the definition rendered read-only,
  and the desired→actual drift diff.
