import { dependenciesFor } from "./wiring";

// Wiring implies a dependency: an app that binds a source's output into its Helm
// values necessarily depends on that source. Records exist carrying wiring with
// no matching edge, and because submit prunes wiring to the selected edges,
// merely opening such an app and saving it discarded every binding it had.

const w = (kind: string, id: string, output = "host", helmPath = "database.host") =>
  ({ sourceKind: kind, sourceId: id, output, helmPath }) as never;

describe("dependenciesFor", () => {
  it("returns the stored edges when nothing is missing", () => {
    const deps = dependenciesFor({
      dependencies: [{ kind: "infrastructure", id: "db" }],
      wiring: [w("infrastructure", "db")],
    } as never);
    expect(deps).toEqual([{ kind: "infrastructure", id: "db" }]);
  });

  it("recovers an edge that only the wiring knows about", () => {
    // The shape found in real data: five wiring entries, no dependencies.
    const deps = dependenciesFor({
      dependencies: [],
      wiring: [w("infrastructure", "todo-app", "host"), w("infrastructure", "todo-app", "port")],
    } as never);
    expect(deps).toEqual([{ kind: "infrastructure", id: "todo-app" }]);
  });

  it("does not duplicate a source referenced by several wires", () => {
    const deps = dependenciesFor({
      dependencies: [],
      wiring: [w("infrastructure", "db", "host"), w("infrastructure", "db", "port"), w("agent", "triage", "agentId")],
    } as never);
    expect(deps).toHaveLength(2);
    expect(deps).toContainEqual({ kind: "infrastructure", id: "db" });
    expect(deps).toContainEqual({ kind: "agent", id: "triage" });
  });

  it("keeps a stored edge that has no wiring", () => {
    // Depending on something without binding any of its outputs is legitimate:
    // it orders the deploy.
    const deps = dependenciesFor({
      dependencies: [{ kind: "application", id: "billing" }],
      wiring: [],
    } as never);
    expect(deps).toEqual([{ kind: "application", id: "billing" }]);
  });

  it("ignores a wire with no source rather than inventing an empty edge", () => {
    const deps = dependenciesFor({
      dependencies: [],
      wiring: [w("infrastructure", ""), w("infrastructure", "db")],
    } as never);
    expect(deps).toEqual([{ kind: "infrastructure", id: "db" }]);
  });

  it("treats kind and id together, so the same id under two kinds is two edges", () => {
    const deps = dependenciesFor({
      dependencies: [],
      wiring: [w("infrastructure", "shared"), w("application", "shared")],
    } as never);
    expect(deps).toHaveLength(2);
  });

  it("handles an absent app, so a create form starts empty", () => {
    expect(dependenciesFor(undefined)).toEqual([]);
    expect(dependenciesFor({ dependencies: [], wiring: [] } as never)).toEqual([]);
  });
});
