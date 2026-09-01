---
version: 1
name: Cortex Console — Inception
description: >-
  Product-register design system for the Cortex control-plane console: a
  multi-tenant AI-agent fleet manager built on Microsoft Foundry. Near-monochrome
  precision (Stripe/Vercel lineage) — near-black ink on white islands over a
  light-grey page — carrying a single restrained violet accent. The accent is
  action, selection and focus; never decoration.
register: product
source_of_truth: web/app/globals.css
---

# Cortex Console — design system

This document describes the system **as shipped**. `web/app/globals.css` is the
source of truth; if the two disagree, the CSS is right and this file is stale.

> A previous version of this file described a lime accent taken from the
> inception42.ai marketing site. The console was deliberately re-skinned away
> from it (`1d5c2ce`, "re-skin UI to the cortex-post-mvp design system") and this
> file was not updated, so it misdescribed the product for months while the CSS
> claimed to mirror it. Kept as a note because the failure mode matters: a design
> doc nobody re-derives is worse than no design doc, since it is trusted.

## Register

**Product.** The user is in a task: watching reconciles land, entitling a tenant,
authoring a deployment. The interface should disappear into the work. Density is
welcome where it carries information; delight is saved for moments, not pages.

The bar is *earned familiarity* — an operator fluent in Linear, Stripe or Vercel
should sit down and trust it, not pause at every subtly-off control.

## Color

Strategy: **restrained**. Tinted neutrals plus one accent, used only for primary
action, current selection, focus and "in progress". Everything else is the
monochrome ramp and the status vocabulary.

### Light (default)

| Role | Value | Use |
|---|---|---|
| `--canvas` | `#f6f6f7` | Page background behind the content islands |
| `--surface` | `#ffffff` | Panels, tables, cards — the islands |
| `--surface-sunken` | `#ededed` | Wells, code blocks, table headers |
| `--surface-hover` | `rgb(41 41 41 / 0.05)` | Row and control hover |
| `--text` | `#292929` | Primary ink |
| `--text-secondary` | `rgb(41 41 41 / 0.74)` | Subheads, secondary emphasis |
| `--text-muted` | `rgb(41 41 41 / 0.6)` | Metadata, help text |
| `--text-disabled` | `rgb(41 41 41 / 0.38)` | Non-essential only — **fails 4.5:1, never for text a user must read** |
| `--border` | `rgb(41 41 41 / 0.08)` | Hairline dividers |
| `--border-strong` | `rgb(41 41 41 / 0.16)` | Emphasised separators |
| `--field-border` | `rgb(41 41 41 / 0.24)` | Input borders |
| `--primary` | `#292929` | Solid near-black button fills |
| `--accent` | `#a964f7` | Selection, focus, "in progress" |
| `--accent-ink` | `#7c3aed` | Accent-family **text** on light |
| `--accent-bg` | `rgb(169 100 247 / 0.12)` | Accent tint for badges and selection |

Primary actions are **near-black**, not accent. The accent marks state; the
solid ink marks the thing to press. Keeping those separate is what stops the
violet becoming decoration.

### Dark

Not an inversion — a separate, tuned set. `--canvas: #1a1a1a` with
`--surface: #232323`, so islands still read as raised. Status colors are
re-picked for the darker ground (`--success` goes `#126e40` → `#3cbd7d`,
`--danger` `#b5231b` → `#ff6b62`); the accent holds at `#a964f7` with a lighter
`--accent-ink: #c29bff` for text.

Component logic never branches on theme. Anything that needs a different value
in dark gets a token, not a conditional.

### Status vocabulary

`success` · `info` · `warning` · `danger` · `neutral`, each with an `-ink`
(text) and `-bg` (tint) companion. `info` deliberately shares the accent hue:
"reconciling" is the system working, and reads as the same family as selection.

## Typography

One family — Inter for everything, JetBrains Mono for identifiers. Product UI
does not need display/body pairing.

**Fixed rem scale, not fluid.** Operators view at consistent DPI; a clamp-sized
heading that shrinks inside a panel looks worse, not better.

| Token | Size | Use |
|---|---|---|
| `--text-display` | 2rem | Page title, sparingly |
| `--text-h1` | 1.5rem | View heading |
| `--text-h2` | 1.125rem | Panel heading |
| `--text-h3` | 0.9375rem | Sub-heading |
| `--text-body` | 0.875rem | Body |
| `--text-body-sm` | 0.8125rem | Dense rows, secondary |
| `--text-caption` | 0.75rem | Metadata |
| `--text-overline` | 0.6875rem | Labels, uppercase eyebrows *within* panels |

Ratio ≈1.15 between steps. More type roles than a marketing page needs, so
exaggerated contrast would read as noise.

**Identifiers are always mono.** Tenant slugs, object ids, Helm paths, image
refs. In a control plane an identifier is a thing you copy and compare, and the
monospace signals that.

## Space and shape

4px base, 8px rhythm: `--space-xs` 4 → `--space-3xl` 64.

Radii climb with the size of the thing: `--radius-xs` 4 (chips) →
`--radius-md` 8 (inputs, buttons) → `--radius-lg` 12 (panels) →
`--radius-island` 24 (page islands) → `--radius-full` (pills, avatars).

Structure: `--rail-w` 220 (collapsed 68), `--topbar-h` 60, `--content-max` 1440.

## Motion

`--dur-fast` 120ms · `--dur` 180ms · `--dur-slow` 240ms, easing
`--ease-out-quart` / `--ease-out-expo`.

Motion conveys **state**: a row settling, a drawer opening, a status changing.
No orchestrated page-load sequences — the operator came to do something, not to
watch it arrive. Every animation needs a `prefers-reduced-motion` alternative.

## Z-index

Semantic scale, never arbitrary values: `--z-rail` 10 → `--z-sticky` 20 →
`--z-dropdown` 40 → `--z-backdrop` 50 → `--z-modal` 60 → `--z-toast` 70 →
`--z-tooltip` 80.

## Rules

- **Contrast is not negotiable.** Body text ≥4.5:1, large text ≥3:1, and
  placeholders count as text. `--text-disabled` is ~2.2:1 on canvas and is for
  decoration only.
- **Primary action is ink, state is accent.** If a violet fill is not selection,
  focus or progress, it is wrong.
- **No card-in-card.** Panels sit on canvas; nesting one inside another means
  the hierarchy is wrong.
- **Every interactive element has** default, hover, focus-visible, active,
  disabled — and loading where it can be slow.
- **Empty states teach.** "Nothing here" wastes the one moment the operator is
  looking for guidance.
- **Failure states say what failed and what to do.** A control plane reports on
  systems in someone else's cloud; "something went wrong" is not reporting.
