import type { Application, Dependency } from "@/lib/types";

/** An app's dependency edges, repaired from its wiring.
 *
 *  Wiring implies a dependency: an app that binds a source's output into its
 *  Helm values necessarily depends on that source. Records exist with wiring but
 *  no matching edge, and the two are read very differently — the picker showed
 *  nothing selected, the bound values lost their labels and their outputs could
 *  not be re-picked, and because submit prunes wiring to the selected edges,
 *  merely opening such an app and saving it silently discarded every binding.
 *
 *  Treat the wiring as authoritative and union it in, so opening the form
 *  presents the true dependency set and saving writes the missing edges back.
 *
 *  Lives here rather than in the form so it can be tested without dragging a
 *  client component (and its server-only imports) into the test. */
export function dependenciesFor(
  app?: Pick<Application, "dependencies" | "wiring">,
): Dependency[] {
  const deps: Dependency[] = [...(app?.dependencies ?? [])];
  const seen = new Set(deps.map((d) => `${d.kind}:${d.id}`));
  for (const w of app?.wiring ?? []) {
    const key = `${w.sourceKind}:${w.sourceId}`;
    if (w.sourceId === "" || seen.has(key)) continue;
    seen.add(key);
    deps.push({ kind: w.sourceKind, id: w.sourceId });
  }
  return deps;
}

/* ── Wireable outputs, by dependency kind ─────────────────────────────────────
 *
 * These live here rather than in the deployment form because the form is a
 * "use client" module: every export of one becomes a client reference, so a
 * Server Component that CALLS one gets "Attempted to call ... from the server"
 * at request time. The pages that build the dependency list are server
 * components, so this vocabulary has to be plain shared code.
 */

/** Derived outputs a dependency application exposes for wiring. */
export const APP_OUTPUTS = ["name", "namespace", "serviceHost"];

/** Derived outputs a dependency agent exposes for wiring. */
export const AGENT_OUTPUTS = ["agentId", "name"];

/** A secret store's wireable outputs are NAMES, never values.
 *
 *  A chart needs two things to read a secret, not one: which Secret to look in
 *  and which key inside it — e.g. `existingSecret` and
 *  `existingSecretPasswordKey`. So a store offers `secretName` plus one output
 *  per declared key.
 *
 *  What is deliberately absent is the value. Wiring is merged into
 *  spec.source.helm.values verbatim and copied into the Argo Application, so
 *  anything offered here is readable by anyone with cluster access.
 */
export const secretSetOutputs = (keys: string[]): string[] => [
  "secretName",
  ...keys.map((k) => `${SECRET_KEY_PREFIX}${k}`),
];

/** Wire-format prefix marking an output as "the NAME of this key". */
const SECRET_KEY_PREFIX = "key:";

/** How a wireable output should read in the picker.
 *
 *  Most outputs are the source's own identifiers (`host`, `agentId`) and are
 *  shown as they are — an author recognises them from the module or chart.
 *
 *  A secret store's key outputs need translating twice over. `key:password` is
 *  wire format and should never have been on screen, but simply stripping the
 *  prefix would be worse than ugly: an entry reading `password`, sitting next to
 *  a Secret name, invites the reading "this binds the password" when what it
 *  binds is the literal string "password". Charts want exactly that (it is what
 *  goes in `existingSecretPasswordKey`), but an author who misreads it would
 *  believe they had wired a credential when they had wired a label. So the label
 *  says which of the two it is.
 */
export function outputLabel(output: string): string {
  if (output === "secretName") return "Secret name";
  if (output.startsWith(SECRET_KEY_PREFIX)) {
    return `Key name: ${output.slice(SECRET_KEY_PREFIX.length)}`;
  }
  return output;
}
