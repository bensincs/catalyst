"use client";

import { Fragment, useId, useMemo, useState } from "react";
import { Link2, Plus, Search, X } from "lucide-react";
import { Menu, MenuItem, MenuLabel } from "@/components/ui/menu";
import styles from "./values-editor.module.css";

/** What fills one value. Absent from the map entirely means "not set" — an
 *  empty literal is the same thing, since empty entries are dropped downstream. */
type Assignment = { kind: "literal"; value: string } | { kind: "output"; token: string };

// Stable empty-array default: a fresh `[]` on each render changes identity and
// would invalidate every memo that depends on it.
const NONE: string[] = [];

// A source is labelled by where it came from and what it is. Callers with plain
// tokens get the token as the label and no origin.
const defaultOutputLabel = (token: string) => ({ tag: "", label: token });

function seed(
  initialStatic: Record<string, string>,
  initialWired: Record<string, string>,
  targets: string[],
) {
  const assignments: Record<string, Assignment> = {};
  for (const [path, value] of Object.entries(initialStatic)) {
    assignments[path] = { kind: "literal", value };
  }
  for (const [path, token] of Object.entries(initialWired)) {
    assignments[path] = { kind: "output", token };
  }
  const extraPaths = Object.keys(assignments).filter((p) => !targets.includes(p));
  return { assignments, extraPaths };
}

/** Sets a list of values — a chart's Helm values, or a Bicep module's inputs.
 *
 *  One row per value: type a literal, or bind it to an output of one of the
 *  deployment's dependencies. Each value takes exactly one source, so the row
 *  itself expresses the mapping and no connector drawing is needed.
 *
 *  Emits a path→literal-text map and a path→output-token map; the parent turns
 *  those into its own fields. */
export function ValuesEditor({
  outputs = NONE,
  outputLabel = defaultOutputLabel,
  targets,
  suggestions = NONE,
  requiredTargets = NONE,
  allowAddTarget = true,
  targetLabel = "Values",
  addPlaceholder = "Add one not listed…",
  emptyHint = "Nothing resolved yet — add one below.",
  initialStatic,
  initialWired,
  onChange,
}: {
  outputs?: string[];
  outputLabel?: (token: string) => { tag: string; label: string };
  targets: string[];
  suggestions?: string[];
  requiredTargets?: string[];
  allowAddTarget?: boolean;
  targetLabel?: string;
  addPlaceholder?: string;
  emptyHint?: string;
  initialStatic: Record<string, string>;
  initialWired: Record<string, string>;
  onChange: (staticMap: Record<string, string>, wiredMap: Record<string, string>) => void;
}) {
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const seeded = useMemo(() => seed(initialStatic, initialWired, targets), []);
  const [assignments, setAssignments] = useState<Record<string, Assignment>>(seeded.assignments);
  const [extraPaths, setExtraPaths] = useState<string[]>(seeded.extraPaths);
  const [newPath, setNewPath] = useState("");
  const [query, setQuery] = useState("");
  // useId, not Math.random: a random id differs between the server render and
  // the client, which fails hydration (the previous canvas had the same bug).
  // Colons are stripped because the id is used in CSS/querySelector contexts.
  const listId = `values-${useId().replace(/:/g, "")}`;

  const knownSet = useMemo(() => new Set(targets), [targets]);
  const requiredSet = useMemo(() => new Set(requiredTargets), [requiredTargets]);
  const allTargets = useMemo(
    () => Array.from(new Set([...targets, ...extraPaths])),
    [targets, extraPaths],
  );

  // Outputs grouped by the dependency they came from, so the picker reads as
  // "which dependency, then which of its outputs". Computed inline rather than
  // memoised: `outputLabel` is typically an inline closure, so memoising on it
  // would rebuild every render anyway while risking a stale one.
  const groups: { tag: string; items: { token: string; label: string }[] }[] = [];
  for (const token of outputs) {
    const { tag, label } = outputLabel(token);
    const existing = groups.find((g) => g.tag === tag);
    if (existing) existing.items.push({ token, label });
    else groups.push({ tag, items: [{ token, label }] });
  }

  const isSet = (p: string) => {
    const a = assignments[p];
    return a ? (a.kind === "output" ? true : a.value.trim() !== "") : false;
  };

  const q = query.trim().toLowerCase();
  const shown = q ? allTargets.filter((p) => p.toLowerCase().includes(q)) : allTargets;
  const setCount = allTargets.filter(isSet).length;

  const commit = (next: Record<string, Assignment>) => {
    setAssignments(next);
    const staticMap: Record<string, string> = {};
    const wiredMap: Record<string, string> = {};
    for (const [path, a] of Object.entries(next)) {
      if (a.kind === "literal") staticMap[path] = a.value;
      else wiredMap[path] = a.token;
    }
    onChange(staticMap, wiredMap);
  };

  const setLiteral = (path: string, value: string) =>
    commit({ ...assignments, [path]: { kind: "literal", value } });
  const bind = (path: string, token: string) =>
    commit({ ...assignments, [path]: { kind: "output", token } });
  const clear = (path: string) => {
    const next = { ...assignments };
    delete next[path];
    commit(next);
  };

  const addPath = () => {
    const p = newPath.trim();
    if (p && !allTargets.includes(p)) setExtraPaths((prev) => [...prev, p]);
    setNewPath("");
  };
  const removePath = (p: string) => {
    setExtraPaths((prev) => prev.filter((x) => x !== p));
    clear(p);
  };

  const singular = targetLabel.replace(/s$/, "").toLowerCase();

  // The picker is identical whether a value is currently bound or not, so build
  // it once. Fragments (not divs) keep the panel's role="menu" children valid.
  const picker = (path: string, current: string | null, close: () => void) =>
    groups.map((g) => (
      <Fragment key={g.tag}>
        {g.tag && <MenuLabel>{g.tag}</MenuLabel>}
        {g.items.map((it) => (
          <MenuItem
            key={it.token}
            role={current ? "menuitemradio" : "menuitem"}
            selected={current === it.token}
            onClick={() => {
              bind(path, it.token);
              close();
            }}
          >
            <span className="mono">{it.label}</span>
          </MenuItem>
        ))}
      </Fragment>
    ));

  return (
    <div className={styles.wrap}>
      {allTargets.length > 0 && (
        <div className={styles.head}>
          <p className={styles.count} aria-live="polite">
            <b>{setCount}</b> of {allTargets.length} set
          </p>
          {allTargets.length > 8 && (
            <div className={styles.search}>
              <Search size={14} strokeWidth={2.2} className={styles.searchIcon} aria-hidden />
              <input
                className={styles.searchInput}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Filter…"
                spellCheck={false}
                aria-label={`Filter ${targetLabel}`}
              />
            </div>
          )}
        </div>
      )}

      {allTargets.length === 0 ? (
        <p className={styles.hint}>{emptyHint}</p>
      ) : (
        <ul className={styles.rows}>
          {shown.map((path) => {
            const a = assignments[path];
            const bound = a?.kind === "output" ? a : null;
            const literal = a?.kind === "literal" ? a.value : "";
            const custom = !knownSet.has(path);
            const needed = requiredSet.has(path) && !isSet(path);
            const inputId = `${listId}-${path.replace(/[^\w-]/g, "_")}`;

            return (
              <li key={path} className={styles.row} data-set={isSet(path) || undefined}>
                <label className={styles.path} htmlFor={bound ? undefined : inputId}>
                  <span className="mono">{path}</span>
                  {needed && <span className={styles.required}>required</span>}
                </label>

                <div className={styles.control}>
                  {bound ? (
                    <>
                      <Menu
                        ariaLabel={`Change the source bound to ${path}`}
                        button={(props) => (
                          <button {...props} type="button" className={styles.bound}>
                            <Link2 size={13} strokeWidth={2.2} aria-hidden />
                            {outputLabel(bound.token).tag && (
                              <span className={styles.boundTag}>
                                {outputLabel(bound.token).tag}
                              </span>
                            )}
                            <span className={`${styles.boundLabel} mono`}>
                              {outputLabel(bound.token).label}
                            </span>
                          </button>
                        )}
                      >
                        {({ close }) => picker(path, bound.token, close)}
                      </Menu>
                      <button
                        type="button"
                        className={styles.clear}
                        aria-label={`Unbind ${path}`}
                        onClick={() => clear(path)}
                      >
                        <X size={14} strokeWidth={2.4} />
                      </button>
                    </>
                  ) : (
                    <>
                      <input
                        id={inputId}
                        className={`${styles.input} mono`}
                        value={literal}
                        placeholder="Set a value…"
                        spellCheck={false}
                        onChange={(e) => setLiteral(path, e.target.value)}
                      />
                      {groups.length > 0 && (
                        <Menu
                          ariaLabel={`Bind ${path} to a dependency output`}
                          align="end"
                          button={(props) => (
                            <button
                              {...props}
                              type="button"
                              className={styles.bindBtn}
                              title={`Bind ${singular} to a dependency output`}
                            >
                              <Link2 size={14} strokeWidth={2.2} aria-hidden />
                              <span className={styles.srOnly}>
                                Bind {path} to a dependency output
                              </span>
                            </button>
                          )}
                        >
                          {({ close }) => picker(path, null, close)}
                        </Menu>
                      )}
                    </>
                  )}

                  {custom && (
                    <button
                      type="button"
                      className={styles.clear}
                      aria-label={`Remove ${path}`}
                      onClick={() => removePath(path)}
                    >
                      <X size={14} strokeWidth={2.4} />
                    </button>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      )}

      {q && shown.length === 0 && <p className={styles.hint}>No matches for “{query}”.</p>}

      {allowAddTarget && (
        <div className={styles.addRow}>
          <datalist id={listId}>
            {suggestions.map((p) => (
              <option key={p} value={p} />
            ))}
          </datalist>
          <input
            className={`${styles.addInput} mono`}
            value={newPath}
            list={listId}
            onChange={(e) => setNewPath(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addPath();
              }
            }}
            placeholder={addPlaceholder}
            spellCheck={false}
            aria-label={`New ${singular}`}
          />
          <button
            type="button"
            className={styles.addBtn}
            onClick={addPath}
            aria-label={`Add ${singular}`}
          >
            <Plus size={15} strokeWidth={2.4} />
          </button>
        </div>
      )}
    </div>
  );
}
