import { parse, stringify } from "yaml";

export type Obj = Record<string, unknown>;

export const isObj = (v: unknown): v is Obj => typeof v === "object" && v !== null && !Array.isArray(v);

export function parseYaml(text: string): Obj {
  if (!text || text.trim() === "") return {};
  try {
    const v = parse(text);
    return isObj(v) ? v : {};
  } catch {
    return {};
  }
}

export function setAt(obj: Obj, path: string[], val: unknown): Obj {
  const [head, ...rest] = path;
  const next: Obj = { ...obj };
  next[head] = rest.length === 0 ? val : setAt(isObj(next[head]) ? (next[head] as Obj) : {}, rest, val);
  return next;
}

export function collectLeaves(obj: Obj, base: string[], out: { path: string[]; val: unknown }[]) {
  for (const key of Object.keys(obj)) {
    const path = [...base, key];
    const v = obj[key];
    if (isObj(v) && Object.keys(v).length > 0) collectLeaves(v, path, out);
    else out.push({ path, val: v });
  }
}

// Coerce free-typed text into the most natural YAML/JSON scalar or structure.
export function coerce(s: string): unknown {
  const t = s.trim();
  if (t === "") return "";
  if (t === "true") return true;
  if (t === "false") return false;
  if (t === "null") return null;
  if (/^-?\d+$/.test(t) || /^-?\d*\.\d+$/.test(t)) return Number(t);
  try {
    const v = parse(t);
    if (typeof v !== "string") return v;
  } catch {
    /* keep as string */
  }
  return t;
}

export function toText(v: unknown): string {
  if (v === null) return "null";
  if (v === undefined) return "";
  if (typeof v === "object") return stringify(v).trimEnd();
  return String(v);
}

/** A stored YAML document ⇆ a flat map of dotted path → raw text (leaf values). */
export function yamlToMap(yaml: string): Record<string, string> {
  const leaves: { path: string[]; val: unknown }[] = [];
  collectLeaves(parseYaml(yaml), [], leaves);
  const m: Record<string, string> = {};
  for (const l of leaves) m[l.path.join(".")] = toText(l.val);
  return m;
}

/** A flat map of dotted path → raw text → a nested YAML document (empty entries
 *  dropped, values coerced to natural scalars). */
export function mapToYaml(map: Record<string, string>): string {
  let obj: Obj = {};
  for (const [path, text] of Object.entries(map)) {
    if (text.trim() === "") continue;
    obj = setAt(obj, path.split(".").map((s) => s.trim()).filter(Boolean), coerce(text));
  }
  return Object.keys(obj).length ? stringify(obj) : "";
}
