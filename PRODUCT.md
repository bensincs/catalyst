# Product

## Register

product

## Users

Cortex is operated from **one console with two operators**, gated by role — a single
vocabulary, role-driven views, never two disconnected apps.

- **Platform Admins (the publisher — our staff).** Home tenant is ours. They author the
  agent catalog, cut and canary versions (`rolloutPercent` as an availability gate),
  entitle tenants by plan, and watch the whole fleet — which tenants run which versions,
  enrollment status, drift, health. Their context is high-stakes, whole-fleet operations:
  one action can change what hundreds of customer tenants are allowed to install.
- **Tenant Admins (the customer — the buyer).** Home tenant is theirs; they see only their
  slice. They browse entitled agents, enable and configure them (forms driven by each
  agent's config schema), choose publish targets (API / Teams / M365), install the in-tenant
  Cortex app, and monitor health, usage, and cost showback. This is the buyable,
  self-serve product face — it has to earn trust from an enterprise or government buyer.

The job to be done is the same at both altitudes: **express intent, see reality, and
understand the gap between them** across a fleet of AI agents that run in customers' own
Azure tenants. End users chat with agents elsewhere (Teams / M365 / API); the console is
where the fleet is governed, not where agents are used.

## Product Purpose

Cortex is the **control-plane console** for a production, multi-tenant AI-agent platform
built on Microsoft Foundry. The platform lets an organization run a fleet of AI agents
without building or operating agent infrastructure: the publisher curates a versioned
catalog, each customer tenant installs a lightweight in-tenant reconciler + Foundry project,
and agents run under the customer's own identity, in the customer's own subscription.

The console is the differentiated product surface on top of the Azure managed spine — the
**builder, admin, and publisher UX** plus the **fleet view** that make multi-tenant agent
management transactable. It exists so a non-developer can log in, install, enable, configure,
and publish an agent, and so a platform team can manage the whole customer base from one
place.

Success looks like: a Tenant Admin completes purchase-less-to-live onboarding (sign in →
install → enable → agent answers over its endpoint) without touching a terminal; a Platform
Admin releases a canary version, entitles a tenant, and reads fleet health without leaving
the console; and at every step the operator trusts what the screen is telling them about a
system running in someone else's cloud.

## Brand Personality

Cortex inherits the **Inception42** identity (the intelligence layer of the G42 Intelligence
Grid): AI-native, sovereign, and precise. The through-line is *certainty* — "operate with
certainty, act decisively, deliver outcomes at national and enterprise scale," "intelligence
you own and control," "trusted and proven, uncompromising reliability."

- **Three words:** sovereign, precise, trusted.
- **Voice & tone:** calm authority. Declarative, exact, unhurried. It states what is and what
  will happen; it never hedges, hypes, or chirps. Confidence expressed through clarity, not
  volume.
- **Emotional goal:** enterprise-serious confidence. Operators should feel *in command* of a
  consequential system; buyers should feel this is safe to trust with mission-critical,
  sovereign workloads. The visual anchor is the Inception42 system — a disciplined
  near-monochrome surface carrying a single, ownable signature accent — translated from a
  marketing surface into a dense operator console.

## Anti-references

- **Consumer-playful SaaS** (the explicit no): pastel gradients, rounded pillowy cards,
  mascots, big friendly illustrations, exclamatory copy. It undercuts a sovereign-enterprise
  buyer's trust instantly.
- **Generic enterprise cloud-console chrome:** crowded toolbars, blue-link soup, gray-on-gray
  density, tab overload. Familiar but joyless; the chosen precision-monochrome lineage
  (Stripe / Vercel restraint) is the deliberate opposite.
- **"Serious = decorated" corporate cliché:** navy-and-gold, marble, stock handshakes.
  Seriousness here comes from precision and legibility, not ornament.
- **Motion or color as decoration.** In a control plane, every accent and every animation
  must mean something (state, selection, feedback). Flourish for its own sake reads as
  untrustworthy.

## Design Principles

1. **Sovereignty, made legible.** The product's promise is isolation and ownership — agents
   run in the customer's tenant, under the customer's identity. The UI must always make
   *what runs where, and as whom* obvious. Never obscure the tenant/identity boundary; it is
   the value proposition on screen.
2. **Desired vs. actual, never ambiguous.** Cortex is a reconciler at heart. The console's
   core job is to show intent, reality, and the drift between them with zero guesswork.
   Trust is earned through operational transparency, not reassurance.
3. **Calm under high stakes.** A single action can ripple across a fleet of tenants. Make
   consequential actions deliberate, previewable, and reversible; surface blast radius before
   commit. Nothing changes silently underneath a customer.
4. **Earned familiarity over novelty.** Power operators must trust every control on first
   sight. Standard affordances, one consistent vocabulary across both admin roles; invention
   is reserved for the few moments that genuinely improve the work, never for flavor.
5. **One shell, two operators.** Platform and Tenant Admins share a single console language;
   role decides what is visible and permitted, never how the interface behaves. Learn it
   once, trust it everywhere.

## Accessibility & Inclusion

- **Target: WCAG 2.2 AA across the console**, maintained to be VPAT / conformance-statement
  ready for enterprise and government procurement.
- **Keyboard-complete:** every workflow operable without a mouse; visible focus states
  (`:focus-visible`); logical tab order; a command surface for power operators.
- **Contrast:** body text ≥ 4.5:1, large/bold text and meaningful UI boundaries ≥ 3:1,
  including placeholder and secondary text — no light-gray-for-elegance.
- **Color is never the only signal.** The brand's state palette runs lime / violet / red;
  lime-green and red together are a red-green color-vision risk, so status must always carry
  a second cue (icon, label, shape, or text), never hue alone.
- **Reduced motion is a first-class path:** every transition has a
  `prefers-reduced-motion: reduce` alternative (crossfade or instant). Motion conveys state;
  it is never required to understand it.
