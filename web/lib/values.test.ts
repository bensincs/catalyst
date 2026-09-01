import { coerce, mapToYaml, toText, yamlToMap } from "./values";

// The YAML <-> flat-map bridge the values editor is built on. A round trip that
// loses or mangles a value silently rewrites a deployment's configuration, so
// the edges matter more than the happy path.
describe("coerce", () => {
  it("keeps YAML scalars as their natural type", () => {
    expect(coerce("true")).toBe(true);
    expect(coerce("false")).toBe(false);
    expect(coerce("null")).toBe(null);
    expect(coerce("8080")).toBe(8080);
    expect(coerce("-3")).toBe(-3);
    expect(coerce("1.5")).toBe(1.5);
  });
  it("keeps version-like and id-like strings as strings", () => {
    // The classic trap: 1.2.3 is not a number, and a leading zero is
    // significant in an account id.
    expect(coerce("1.2.3")).toBe("1.2.3");
    expect(coerce("v1.4.2")).toBe("v1.4.2");
    expect(coerce("0042")).toBe("0042");
  });
  it("trims but preserves the empty string", () => {
    expect(coerce("  x  ")).toBe("x");
    expect(coerce("")).toBe("");
    expect(coerce("   ")).toBe("");
  });
});

describe("yamlToMap / mapToYaml", () => {
  it("round trips a nested document through dotted paths", () => {
    const yaml = "database:\n  host: db.example.com\n  port: 5432\nreplicas: 3\n";
    const m = yamlToMap(yaml);
    expect(m["database.host"]).toBe("db.example.com");
    expect(m["database.port"]).toBe("5432");
    expect(m["replicas"]).toBe("3");
    expect(yamlToMap(mapToYaml(m))).toEqual(m);
  });
  it("drops empty entries rather than writing empty keys", () => {
    // The editor treats an empty input as unset; emitting `key: ""` would set
    // the value to an empty string instead, which is a different thing.
    expect(mapToYaml({ a: "1", b: "", c: "   " })).toBe("a: 1\n");
  });
  it("returns an empty document when nothing is set", () => {
    expect(mapToYaml({})).toBe("");
    expect(mapToYaml({ a: "" })).toBe("");
  });
  it("survives invalid YAML instead of throwing", () => {
    // Values arrive from a stored record that a human may have edited.
    expect(yamlToMap("key: [unclosed")).toEqual({});
    expect(yamlToMap("")).toEqual({});
  });
  it("does not collapse a false or zero leaf", () => {
    const m = yamlToMap("flags:\n  on: false\ncount: 0\n");
    expect(m["flags.on"]).toBe("false");
    expect(m["count"]).toBe("0");
    // ...and they survive the trip back, rather than being read as empty.
    const back = yamlToMap(mapToYaml(m));
    expect(back["flags.on"]).toBe("false");
    expect(back["count"]).toBe("0");
  });
});

describe("toText", () => {
  it("renders scalars and structures without throwing", () => {
    expect(toText(null)).toBe("null");
    expect(toText(undefined)).toBe("");
    expect(toText(5)).toBe("5");
    expect(toText(false)).toBe("false");
    expect(toText({ a: 1 })).toContain("a: 1");
  });
});
