import { dependenciesFor, outputLabel, secretSetOutputs } from "./wiring";

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

// A secret store offers names, never values — and offers BOTH halves a chart
// needs: which Secret, and which key inside it.
describe("secretSetOutputs", () => {
  it("offers the Secret name and one output per key", () => {
    expect(secretSetOutputs(["password", "apiKey"])).toEqual([
      "secretName",
      "key:password",
      "key:apiKey",
    ]);
  });

  it("offers the Secret name even for a store with no keys", () => {
    expect(secretSetOutputs([])).toEqual(["secretName"]);
  });

  it("never offers anything that could be a value", () => {
    // The outputs are merged into spec.source.helm.values verbatim, so anything
    // here is published to anyone with cluster access. Every entry must be a
    // name: the Secret's, or a key's.
    for (const o of secretSetOutputs(["password"])) {
      expect(o === "secretName" || o.startsWith("key:")).toBe(true);
    }
  });
});

describe("outputLabel", () => {
  it("never shows the wire format", () => {
    expect(outputLabel("key:password")).not.toContain("key:");
  });

  it("distinguishes a key NAME from the secret itself", () => {
    // The danger this guards against: an entry reading just "password", next to
    // a Secret name, reads as "this binds the password". It binds the string
    // "password". An author who misreads it thinks they wired a credential when
    // they wired a label.
    const label = outputLabel("key:password");
    expect(label).toContain("password");
    expect(label.toLowerCase()).toContain("key name");
  });

  it("leaves a source's own identifiers alone", () => {
    // Bicep outputs and derived app/agent outputs are names the author already
    // recognises; rewriting them would make them harder to match up, not easier.
    for (const o of ["host", "port", "administratorLogin", "serviceHost", "agentId"]) {
      expect(outputLabel(o)).toBe(o);
    }
  });

  it("shows the Secret name it resolves to", () => {
    // Symmetry with the key output: both entries name a concrete thing, so an
    // author can read what a binding produces without leaving the picker.
    expect(outputLabel("secretName", "todo-database")).toBe(
      "Secret name: cortex-secret-todo-database",
    );
  });

  it("falls back to the bare label when the store is unknown", () => {
    expect(outputLabel("secretName")).toBe("Secret name");
  });
});
