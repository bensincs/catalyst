---
version: alpha
name: Cortex Console — Inception42
description: >-
  Product-register design system for the Cortex control-plane console: a
  multi-tenant AI-agent fleet manager built on Microsoft Foundry. Near-monochrome
  precision (Stripe/Vercel lineage) carrying the Inception42 signature lime, with a
  light default surface and a first-class dark mode. Restrained color strategy —
  the lime is action and "live", never decoration.
colors:
  # ── Brand primitives (from inception42.ai) ──
  brand-ink: "#1e1f1e"      # near-black — dominant brand neutral
  brand-paper: "#fbf8ff"    # cool off-white (slight violet cast)
  brand-lime: "#91ff01"     # signature accent — "live / go / primary"
  brand-violet: "#ae74ff"   # secondary accent — in-progress / data series 2
  brand-red: "#f90019"      # alert red
  brand-slate: "#c2c8d6"    # cool blue-gray
  brand-gray: "#6b7280"     # mid gray

  # ── Semantic roles — LIGHT (default theme) ──
  canvas: "#fbf8ff"         # app background / chrome
  surface: "#ffffff"        # content panels, cards, tables
  surface-sunken: "#f3f1f7" # side rail, table headers, wells, code blocks
  surface-hover: "#f4f2fa"  # row/control hover on light
  text: "#1e1f1e"           # primary text — AA on all light surfaces
  text-secondary: "#3d3f43" # subheads, secondary emphasis
  text-muted: "#565961"     # metadata, help text — verified ≥4.5:1
  text-disabled: "#9a9da3"  # non-essential only
  border: "#e6e3ef"         # hairline dividers, input borders
  border-strong: "#c2c8d6"  # emphasized separators, focus wells
  on-accent: "#1e1f1e"      # ink on lime fills (brand CTA pairing)
  accent: "#91ff01"         # primary action / selection / "live" fill
  accent-hover: "#84e800"   # lime pressed/hover
  accent-ink: "#356b00"     # accessible lime-family TEXT on light (rare)
  accent-bg: "#edfbd2"      # pale lime badge/selection tint
  focus: "#1e1f1e"          # focus ring core (lime glow layered, not relied on)

  # ── Status — control-plane health (LIGHT) ──
  success: "#91ff01"        # healthy / synced / enabled  (dot+label; lime = live)
  success-ink: "#356b00"
  success-bg: "#edfbd2"
  info: "#ae74ff"           # reconciling / pending / in-progress
  info-ink: "#7c3aed"
  info-bg: "#f2ebff"
  warning: "#f5a623"        # attention / quota / drift
  warning-ink: "#9a5b00"
  warning-bg: "#fdf3e0"
  danger: "#f90019"         # error / blocked / failed — dots, emphasis, borders
  danger-strong: "#e60017"  # danger button fill (AA with white text: 4.8:1)
  danger-ink: "#cc0016"     # danger TEXT on light
  danger-bg: "#ffeef0"
  neutral: "#6b7280"        # disabled / unknown / offline
  neutral-bg: "#eef0f4"

  # ── Semantic roles — DARK ──
  canvas-dark: "#151615"
  surface-dark: "#1e1f1e"     # brand near-black is the content surface here
  surface-sunken-dark: "#121312"
  surface-hover-dark: "#282a29"
  text-dark: "#fbf8ff"
  text-secondary-dark: "#c8cad0"
  text-muted-dark: "#989ca4"  # verified ≥4.5:1 on surface-dark
  text-disabled-dark: "#63666c"
  border-dark: "#313235"
  border-strong-dark: "#484a4e"
  accent-dark: "#91ff01"      # lime sings on near-black — great focus ring here
  accent-ink-dark: "#b6ff5c"
  accent-bg-dark: "#243100"
  success-dark: "#91ff01"
  info-dark: "#c29bff"
  warning-dark: "#f5b642"
  danger-dark: "#ff5a6a"
typography:
  display:
    fontFamily: f37Lineca
    fontSize: 32px
    fontWeight: 500
    lineHeight: 1.05
    letterSpacing: -0.02em
  heading-page:
    fontFamily: abcDiatype
    fontSize: 24px
    fontWeight: 600
    lineHeight: 1.2
    letterSpacing: -0.01em
  heading-section:
    fontFamily: abcDiatype
    fontSize: 18px
    fontWeight: 600
    lineHeight: 1.25
    letterSpacing: -0.005em
  heading-card:
    fontFamily: abcDiatype
    fontSize: 15px
    fontWeight: 600
    lineHeight: 1.3
  body-lg:
    fontFamily: abcDiatype
    fontSize: 16px
    fontWeight: 400
    lineHeight: 1.6
  body-md:
    fontFamily: abcDiatype
    fontSize: 14px
    fontWeight: 400
    lineHeight: 1.55
  body-sm:
    fontFamily: abcDiatype
    fontSize: 13px
    fontWeight: 400
    lineHeight: 1.5
  label-md:
    fontFamily: abcDiatype
    fontSize: 13px
    fontWeight: 500
    lineHeight: 1.2
  label-sm:
    fontFamily: abcDiatype
    fontSize: 12px
    fontWeight: 500
    lineHeight: 1.2
  caption:
    fontFamily: abcDiatype
    fontSize: 12px
    fontWeight: 400
    lineHeight: 1.4
  overline:
    fontFamily: abcDiatype
    fontSize: 11px
    fontWeight: 600
    lineHeight: 1
    letterSpacing: 0.06em
  data-md:
    fontFamily: JetBrains Mono
    fontSize: 13px
    fontWeight: 450
    lineHeight: 1.5
    fontFeature: '"ss01", "zero"'
  data-sm:
    fontFamily: JetBrains Mono
    fontSize: 12px
    fontWeight: 450
    lineHeight: 1.45
    fontFeature: '"zero"'
spacing:
  px: 1px
  xs: 4px
  sm: 8px
  md: 12px
  base: 16px
  lg: 24px
  xl: 32px
  2xl: 48px
  3xl: 64px
  rail: 248px       # collapsed side-nav rail width
  content-max: 1360px
rounded:
  none: 0
  sm: 4px
  md: 6px
  lg: 8px
  xl: 12px
  full: 9999px
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.on-accent}"
    typography: "{typography.label-md}"
    rounded: "{rounded.md}"
    padding: 8px 14px
  button-primary-hover:
    backgroundColor: "{colors.accent-hover}"
  button-secondary:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.md}"
    padding: 8px 14px
  button-secondary-hover:
    backgroundColor: "{colors.surface-hover}"
  button-ghost:
    backgroundColor: transparent
    textColor: "{colors.text-secondary}"
    rounded: "{rounded.md}"
    padding: 8px 12px
  button-danger:
    backgroundColor: "{colors.danger-strong}"
    textColor: "#ffffff"
    rounded: "{rounded.md}"
    padding: 8px 14px
  input:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.text}"
    rounded: "{rounded.md}"
    padding: 8px 10px
  input-focus:
    backgroundColor: "{colors.surface}"
  badge-status:
    typography: "{typography.label-sm}"
    rounded: "{rounded.full}"
    padding: 2px 8px
  card:
    backgroundColor: "{colors.surface}"
    rounded: "{rounded.lg}"
    padding: 20px
  nav-item-active:
    backgroundColor: "{colors.surface-hover}"
    textColor: "{colors.text}"
    rounded: "{rounded.md}"
---

# Cortex Console — Design System

## Overview

Cortex is the control-plane console for a multi-tenant AI-agent platform on Microsoft
Foundry. It is a **product** surface: design serves the operator's task, not the other way
around. Two operators share one shell — Platform Admins (publisher: catalog, versioning,
canary, entitlements, whole-fleet view) and Tenant Admins (customer: browse, enable,
configure, install, monitor) — with role deciding what is visible, never how it behaves.

The feel is **enterprise-serious, precise, sovereign**. It inherits the Inception42
identity (the intelligence layer of the G42 Intelligence Grid) and translates a bold
marketing palette into a dense, trustworthy operator console in the Stripe / Vercel
precision lineage: near-monochrome, high-contrast, restrained, carrying a single ownable
signature — the lime `#91ff01`.

Three ideas govern every screen:

- **Desired vs. actual, never ambiguous.** Cortex is a reconciler. The UI's core job is to
  show intent, reality, and the drift between them without guesswork.
- **Sovereignty, made legible.** Agents run in the customer's own tenant, as the customer's
  own identity. Always make *what runs where, and as whom* obvious.
- **Calm under high stakes.** One action can ripple across a fleet of tenants. Consequential
  actions are deliberate, previewable, and reversible; nothing changes silently.

The tool should disappear into the task. Familiarity is a feature here; surprise is a bug.

## Colors

Restrained by default: tinted-cool neutrals carry the surface, and **one accent does the
work**. The signature lime is reserved for a single meaning — *live*: the primary action on
a screen, the current selection, and the "healthy / synced / enabled" state. It is never
decoration and never a background wash.

**Neutrals (the 95% of the surface).**
- **Ink (`#1e1f1e`):** the brand near-black. Primary text and, in dark mode, the content
  surface. Permanent, exact, high-contrast.
- **Paper (`#fbf8ff`):** cool off-white with a faint violet cast. The light-mode app
  canvas. Content panels sit one step brighter on pure white; the side rail and table
  headers sink one step to `#f3f1f7` — a three-tone tonal ladder, not shadows.
- **Slate (`#c2c8d6`) / Gray (`#6b7280`):** cool grays for strong separators, muted
  metadata, and disabled/unknown status.

**Accent — lime `#91ff01` ("live").** Always paired with ink text/icons (`on-accent`), never
white. Primary buttons, the active nav indicator, the selected-row marker, and the
healthy/enabled status all speak lime, so the palette teaches one lesson: *lime means go,
lime means healthy, lime means this is the thing.* Context (button vs. pill vs. 8px dot)
disambiguates the roles. Because pure lime has low luminance-contrast against light
surfaces, never use it as an outline or as small colored text on light — use `accent-ink`
(`#356b00`) for the rare lime-colored word, and a dark focus ring (below) for outlines.

**Secondary accent — violet `#ae74ff`.** The "in motion" hue: reconciling, pending,
in-progress, and the second series in any chart. Text form `info-ink` (`#7c3aed`).

**Status vocabulary (control-plane health).** Every status carries a **second cue** — icon,
shape, or label — so it never depends on hue (lime-green and red together are a red-green
color-vision risk):

| State | Meaning | Dot / fill | Text | Tint |
|---|---|---|---|---|
| Healthy / synced / enabled | steady-state, no drift | `success` lime | `success-ink` | `success-bg` |
| Reconciling / pending | desired ≠ actual, converging | `info` violet | `info-ink` | `info-bg` |
| Attention / drift / quota | needs a human soon | `warning` amber | `warning-ink` | `warning-bg` |
| Error / blocked / failed | not running; action required | `danger` red | `danger-ink` | `danger-bg` |
| Disabled / unknown / offline | inert or unreported | `neutral` gray | `text-muted` | `neutral-bg` |

**Dark mode** is first-class, not an afterthought — it's where operators watch reconciles
land. The brand near-black `#1e1f1e` becomes the content surface (`canvas-dark` sits below
it); lime gains contrast on dark and doubles as the focus ring. All `-dark` role tokens are
tuned to hold WCAG AA. Toggle by swapping the semantic set; component logic is unchanged.

Contrast is verified: body text ≥ 4.5:1 on every surface it lands on; `text-muted`,
placeholders, and meaningful borders included — no light-gray-for-elegance.

## Typography

Two brand faces plus a supporting mono:

- **abcDiatype** carries the console — headings, body, labels, buttons, data. A precise
  neo-grotesque (the Stripe/Vercel register); one family tuned across weights keeps a dense
  UI calm. This is the default for ~95% of text.
- **f37Lineca** (`display`) is the brand's display face, used **sparingly** for moments that
  deserve weight: onboarding/first-run headlines, empty-state hero lines, the auth screen,
  marketing-adjacent surfaces. Never for UI labels, table cells, or dense controls.
- **JetBrains Mono** (`data-*`) sets the identifiers this product is full of — agent IDs,
  image SHAs, cert thumbprints, tenant IDs, versions, JSON config — with slashed zero and
  tabular figures so hashes align and never read ambiguously.

The scale is **fixed rem, not fluid** (product users view at consistent DPI) on a tight
1.125–1.2 ratio: `heading-page` 24 → `heading-section` 18 → `heading-card` 15 → `body-md`
14 → `body-sm`/`label` 13 → `caption`/`overline` 12–11. Prose caps at 65–75ch; data and
tables may run denser. `overline` (11px, 0.06em, uppercase) is for **functional** grouping
only — table group headers, definition labels — never a decorative section eyebrow.

> **Fonts are licensed.** abcDiatype (Dinamo) and f37Lineca (F37) require licenses for the
> product build, matching the marketing site's `"abcDiatype","abcDiatype Fallback"` setup.
> Fallback stacks: sans → `abcDiatype, Inter, system-ui, sans-serif`; display →
> `f37Lineca, abcDiatype, system-ui, sans-serif`; mono →
> `"JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, monospace`.

## Layout

A persistent **side rail** (248px, collapsible to icons) holds primary navigation; a top bar
carries the tenant/context switcher, environment indicator, command palette trigger, and
account. Content sits in a single column capped at `content-max` (1360px) for reading and
forms, and goes edge-to-edge for wide fleet tables. Responsiveness is **structural** — the
rail collapses, tables switch to a stacked/priority-column layout, toolbars wrap — not fluid
typography.

Spacing rides a **4px base / 8px rhythm** (`xs` 4 → `sm` 8 → `md` 12 → `base` 16 → `lg` 24 →
`xl` 32 → `2xl` 48). Vary it deliberately: tight within a control group, generous between
regions. Prefer Flexbox for 1-D toolbars/rows and Grid only for genuine 2-D layouts; for
card/tile groups use `repeat(auto-fit, minmax(280px, 1fr))`. A semantic z-index scale
applies: rail/sticky < dropdown < modal-backdrop < modal < toast < tooltip — never arbitrary
`9999`. Popovers, menus, and the command palette use the native `<dialog>` / popover API or a
portal so they escape any `overflow` clip.

## Elevation & Depth

Flat and engineered. Hierarchy comes from **tonal layers + hairline borders**, not drop
shadows. Light mode stacks paper `#fbf8ff` (canvas) → white (surface) → `#f3f1f7` (sunken);
dark mode stacks `#151615` → `#1e1f1e` → raised `#282a29`. Borders are `1px` and low-contrast
(`border`), stepping to `border-strong` only where separation must read.

Shadows are used **only for genuinely floating layers** (menus, popovers, modals, toasts) and
stay soft and cool: e.g. `0 1px 2px rgba(30,31,30,.06)` resting, `0 8px 24px
rgba(30,31,30,.12)` for overlays. No shadows on cards, inputs, or table rows. In dark mode,
lift via a lighter surface + subtle border rather than shadow.

## Shapes

Architectural, not pillowy — the register is precision. A tight radius ladder: inputs and
buttons `md` (6px), cards/panels/menus `lg` (8px), large modals/wells `xl` (12px), chips and
small controls `sm` (4px), pills/avatars/status badges `full`. Never mix a rounded family
with sharp corners in the same view. Consumer-playful roundness is out of register.

## Components

Every interactive component ships the full state set — **default, hover, focus, active,
disabled, loading, error** — or it isn't done. Vocabulary is consistent screen to screen:
the same button is the same button everywhere.

- **Buttons.** *Primary* = lime fill + ink text, one per screen for the main action.
  *Secondary* = white/surface + `border` + ink text. *Ghost* = transparent, muted text, for
  low-emphasis/toolbar actions. *Danger* = red fill + white for destructive commits. Sizes
  share the 8px rhythm; icon buttons are square. Disabled drops to `neutral`/`text-disabled`
  with no accent.
- **Focus.** A visible `2px` ring on `:focus-visible` — dark core (`focus` / lime on dark)
  with a `2px` surface-colored gap so it reads on any background. A subtle lime outer glow may
  layer on for brand flavor but contrast never depends on it.
- **Status badge & dot.** Pill (`badge-status`) or 8px dot, always **icon or label + color**,
  drawn from the status vocabulary. The canonical fleet primitive.
- **Inputs / forms.** `md` radius, `border` at rest → `border-strong` on hover → dark ring on
  focus → `danger` border + `danger-ink` helper text on error. Labels in `label-md`, helper
  in `caption`. The agent's substance lives in its **definition** (see AGENT-MODEL.md) —
  a `prompt` (model/instructions/tools/knowledge) or `hosted` (image/runtime) payload the
  publisher authors and versions; tenant enable is light (publish targets + optional
  knowledge binding), so forms stay small and native.
- **Tables** are the heart of the fleet views: dense rows, sticky header on `surface-sunken`,
  tabular/mono figures for IDs and versions, row hover `surface-hover`, selection marked with
  a lime leading indicator. Provide sort, filter, and column density controls.
- **Diff / drift view.** A first-class pattern: desired vs. actual side-by-side or inline,
  additions in lime, removals in red, unchanged in muted — the reconciler made visible.
- **Loading** uses **skeletons** shaped like the content (rows, cards, form), not centered
  spinners. **Empty states** teach the next action ("No agents enabled yet — browse your
  entitled catalog") rather than saying "nothing here".
- **Overlays.** Prefer inline and progressive disclosure; reach for a modal only for a
  focused, blocking decision (destructive confirm, install handoff). Never a modal as the
  first thought. A **command palette** (⌘K) gives power operators keyboard-complete reach.

## Motion

Motion conveys **state**, never decoration. Most transitions are **150–250 ms** with an
ease-out curve (ease-out-quart/expo) — no bounce, no elastic. Use it for state changes,
feedback, selection, reveal, and value transitions (a fleet count ticking as heartbeats land).
No orchestrated page-load choreography: the console loads into a task. Staggering the rows of
a single list on first render is fine; a uniform entrance bolted onto every region is not.
Reveals enhance already-visible defaults — never gate content on a class-triggered transition.
Every animation has a `prefers-reduced-motion: reduce` alternative (crossfade or instant), and
nothing requires motion to be understood.

## Do's and Don'ts

- **Do** reserve lime for a single meaning — *live*: the one primary action, current
  selection, and healthy/enabled state. **Don't** use it as a background, border wash, or
  decoration.
- **Do** pair every status color with an icon, shape, or label. **Don't** signal state by hue
  alone (lime + red is a red-green risk).
- **Do** convey depth with tonal layers and 1px borders. **Don't** add drop shadows to cards,
  rows, or inputs.
- **Do** keep abcDiatype across UI, labels, and data; reserve f37Lineca for onboarding/empty/
  hero moments. **Don't** set UI labels or table cells in the display face.
- **Do** set every ID, SHA, version, and thumbprint in the mono with tabular figures. **Don't**
  let identifiers wrap or misalign.
- **Do** show desired, actual, and drift explicitly. **Don't** hide the tenant/identity
  boundary — it's the product's promise.
- **Do** ship default/hover/focus/active/disabled/loading/error for every control. **Don't**
  reinvent standard affordances (custom scrollbars, novel form controls, non-standard modals).
- **Do** preview blast radius before fleet-wide commits and keep them reversible. **Don't** let
  anything change silently under a tenant.
- **Do** hold WCAG 2.2 AA (4.5:1 body, 3:1 large/UI), keyboard-complete, focus-visible.
  **Don't** use light-gray body text on tinted white for "elegance".
