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
