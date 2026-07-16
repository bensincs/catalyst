"use client";

import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Cable, PenLine, Plus, Search, X } from "lucide-react";
import styles from "./wiring-canvas.module.css";

type Static = { id: string; value: string };
type Link = { source: string; path: string }; // source = `out:<name>` | `st:<id>`
type Line = { key: string; d: string; active: boolean };

let seq = 0;
const uid = () => `st${seq++}`;

// Stable empty-array default: a fresh `[]` default on each render would change
// identity every time and drive the layout effect into an infinite update loop.
const NONE: string[] = [];

function seed(initialStatic: Record<string, string>, initialWired: Record<string, string>, targets: string[]) {
  const statics: Static[] = [];
  const links: Link[] = [];
  const seen = new Set<string>();
  for (const [target, value] of Object.entries(initialStatic)) {
    const id = uid();
    statics.push({ id, value });
    links.push({ source: `st:${id}`, path: target });
    seen.add(target);
  }
  for (const [target, out] of Object.entries(initialWired)) {
    links.push({ source: `out:${out}`, path: target });
    seen.add(target);
  }
  return { statics, links, extraPaths: [...seen].filter((t) => !targets.includes(t)) };
}

function build(statics: Static[], links: Link[]) {
  const byId = new Map(statics.map((s) => [s.id, s]));
  const staticMap: Record<string, string> = {};
  const wiredMap: Record<string, string> = {};
  for (const l of links) {
    if (l.source.startsWith("out:")) wiredMap[l.path] = l.source.slice(4);
    else if (l.source.startsWith("st:")) {
      const s = byId.get(l.source.slice(3));
      if (s) staticMap[l.path] = s.value;
    }
  }
  return { staticMap, wiredMap };
}

/** A wiring board: the right column lists targets (auto-filled from a chart's Helm
 *  values, or a module's Bicep inputs); the left holds sources — static values you
 *  type, and (optionally) a module's Bicep outputs. Draw a line from a source to a
 *  target to set it. Emits a target→static-text map and a target→output-name map;
 *  the parent turns those into its own fields. */
export function WiringCanvas({
  outputs = NONE,
  targets,
  suggestions = NONE,
  allowAddTarget = true,
  sourceLabel = "Sources",
  targetLabel = "Values",
  addPlaceholder = "Add one not listed…",
  emptyHint = "Nothing resolved yet — add one below.",
  initialStatic,
  initialWired,
  onChange,
}: {
  outputs?: string[];
  targets: string[];
  suggestions?: string[];
  allowAddTarget?: boolean;
  sourceLabel?: string;
  targetLabel?: string;
  addPlaceholder?: string;
  emptyHint?: string;
  initialStatic: Record<string, string>;
  initialWired: Record<string, string>;
  onChange: (staticMap: Record<string, string>, wiredMap: Record<string, string>) => void;
}) {
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const seeded = useMemo(() => seed(initialStatic, initialWired, targets), []);
  const [statics, setStatics] = useState<Static[]>(seeded.statics);
  const [extraPaths, setExtraPaths] = useState<string[]>(seeded.extraPaths);
  const [links, setLinks] = useState<Link[]>(seeded.links);
  const [pending, setPending] = useState<string | null>(null);
  const [newPath, setNewPath] = useState("");
  const [query, setQuery] = useState("");

  const boardRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const srcRefs = useRef(new Map<string, HTMLElement>());
  const inRefs = useRef(new Map<string, HTMLElement>());
  const [lines, setLines] = useState<Line[]>([]);
  const [hoverPath, setHoverPath] = useState<string | null>(null);
  const listId = useRef(`wire-${Math.random().toString(36).slice(2, 7)}`).current;

  const chartSet = useMemo(() => new Set(targets), [targets]);
  const allTargets = useMemo(() => Array.from(new Set([...targets, ...extraPaths])), [targets, extraPaths]);

  // Auto-order the targets so each wired target sits at ~the same row as its
  // source, keeping the connector lines short and un-crossed: walk the sources
  // top-to-bottom, placing each source's wired target(s) at that row (and filling
  // an unwired source's row with an unwired target), then the rest below.
  const orderedTargets = useMemo(() => {
    const sourceOrder = [...outputs.map((o) => `out:${o}`), ...statics.map((s) => `st:${s.id}`)];
    const bySource = new Map<string, string[]>();
    for (const l of links) {
      const arr = bySource.get(l.source);
      if (arr) arr.push(l.path);
      else bySource.set(l.source, [l.path]);
    }
    const wired = new Set(links.map((l) => l.path));
    const unwired = allTargets.filter((p) => !wired.has(p));
    const placed = new Set<string>();
    const ordered: string[] = [];
    let u = 0;
    for (const src of sourceOrder) {
      const ts = bySource.get(src);
      if (ts) {
        for (const t of ts) {
          if (!placed.has(t)) {
            ordered.push(t);
            placed.add(t);
          }
        }
      } else if (u < unwired.length) {
        ordered.push(unwired[u]);
        placed.add(unwired[u]);
        u++;
      }
    }
    for (; u < unwired.length; u++) {
      if (!placed.has(unwired[u])) {
        ordered.push(unwired[u]);
        placed.add(unwired[u]);
      }
    }
    for (const p of allTargets) if (!placed.has(p)) ordered.push(p);
    return ordered;
  }, [outputs, statics, links, allTargets]);

  const q = query.trim().toLowerCase();
  const shownTargets = q ? orderedTargets.filter((p) => p.toLowerCase().includes(q)) : orderedTargets;

  const commit = (s: Static[], l: Link[]) => {
    setStatics(s);
    setLinks(l);
    const { staticMap, wiredMap } = build(s, l);
    onChange(staticMap, wiredMap);
  };

  const recompute = useCallback(() => {
    const board = boardRef.current;
    if (!board) return;
    const box = board.getBoundingClientRect();
    const next: Line[] = [];
    for (const w of links) {
      const a = srcRefs.current.get(w.source);
      const b = inRefs.current.get(w.path);
      if (!a || !b) continue;
      const ra = a.getBoundingClientRect();
      const rb = b.getBoundingClientRect();
      const x1 = ra.right - box.left;
      const y1 = ra.top + ra.height / 2 - box.top;
      const x2 = rb.left - box.left;
      const y2 = rb.top + rb.height / 2 - box.top;
      const dx = Math.max(36, (x2 - x1) / 2);
      next.push({
        key: `${w.source}→${w.path}`,
        d: `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`,
        active: pending === w.source || hoverPath === w.path,
      });
    }
    setLines(next);
  }, [links, pending, hoverPath]);

  useLayoutEffect(() => {
    recompute();
  }, [recompute, statics, shownTargets, outputs]);

  useLayoutEffect(() => {
    const board = boardRef.current;
    if (!board) return;
    const ro = new ResizeObserver(recompute);
    ro.observe(board);
    const sc = scrollRef.current;
    sc?.addEventListener("scroll", recompute, { passive: true });
    window.addEventListener("resize", recompute);
    return () => {
      ro.disconnect();
      sc?.removeEventListener("scroll", recompute);
      window.removeEventListener("resize", recompute);
    };
  }, [recompute]);

  const srcWired = (id: string) => links.some((l) => l.source === id);
  const pathWired = (p: string) => links.some((l) => l.path === p);

  const clickSource = (id: string) => setPending((cur) => (cur === id ? null : id));
  const clickPath = (p: string) => {
    if (!pending) return;
    const existing = links.find((l) => l.path === p);
    const next = existing && existing.source === pending ? links.filter((l) => l.path !== p) : [...links.filter((l) => l.path !== p), { source: pending, path: p }];
    commit(statics, next);
    setPending(null);
  };

  const addStatic = () => {
    const id = uid();
    commit([...statics, { id, value: "" }], links);
    setPending(`st:${id}`);
  };
  const setStaticValue = (id: string, value: string) => commit(statics.map((s) => (s.id === id ? { ...s, value } : s)), links);
  const removeStatic = (id: string) => {
    const src = `st:${id}`;
    commit(statics.filter((s) => s.id !== id), links.filter((l) => l.source !== src));
    if (pending === src) setPending(null);
  };

  const addPath = () => {
    const p = newPath.trim();
    if (p && !allTargets.includes(p)) setExtraPaths((prev) => [...prev, p]);
    setNewPath("");
  };
  const removePath = (p: string) => {
    setExtraPaths((prev) => prev.filter((x) => x !== p));
    commit(statics, links.filter((l) => l.path !== p));
  };

  const srcSetRef = (id: string) => (el: HTMLElement | null) => {
    if (el) srcRefs.current.set(id, el);
    else srcRefs.current.delete(id);
  };
  const inSetRef = (p: string) => (el: HTMLElement | null) => {
    if (el) inRefs.current.set(p, el);
    else inRefs.current.delete(p);
  };

  const pendingLabel = pending?.startsWith("out:") ? pending.slice(4) : pending ? "static value" : "";

  return (
    <div className={styles.wrap}>
      <div className={styles.legend} aria-live="polite">
        <Cable size={15} strokeWidth={2} />
        <span>
          {pending ? (
            <>
              Wiring <b className={styles.legendOut}>{pendingLabel}</b> — pick a {targetLabel.toLowerCase()} to connect it to.
            </>
          ) : outputs.length > 0 ? (
            <>Add a static value or pick a Bicep output on the left, then draw it into a {targetLabel.toLowerCase()} on the right.</>
          ) : (
            <>Add a static value on the left, then draw it into a {targetLabel.toLowerCase()} on the right.</>
          )}
        </span>
      </div>

      <div className={styles.boardScroll} ref={scrollRef}>
        <div className={styles.board} ref={boardRef}>
          <svg className={styles.svg} aria-hidden>
            {lines.map((l) => (
              <path key={l.key} d={l.d} pathLength={1} className={styles.wire} data-active={l.active || undefined} />
            ))}
          </svg>

          <div className={`${styles.col} ${styles.colSticky}`}>
            <div className={styles.colHead} data-side="out">
              {sourceLabel}
            </div>

            {outputs.map((o) => {
              const id = `out:${o}`;
              return (
                <button
                  type="button"
                  key={id}
                  ref={srcSetRef(id)}
                  className={styles.node}
                  data-side="out"
                  data-selected={pending === id || undefined}
                  data-wired={srcWired(id) || undefined}
                  onClick={() => clickSource(id)}
                >
                  <span className={styles.nodeTag}>output</span>
                  <span className={styles.nodeLabel}>{o}</span>
                  <span className={styles.port} aria-hidden />
                </button>
              );
            })}

            {statics.map((s) => {
              const id = `st:${s.id}`;
              return (
                <div
                  key={id}
                  ref={srcSetRef(id)}
                  className={styles.node}
                  data-side="out"
                  data-static
                  data-selected={pending === id || undefined}
                  data-wired={srcWired(id) || undefined}
                  onClick={() => clickSource(id)}
                  role="button"
                  tabIndex={0}
                >
                  <span className={styles.nodeTag} data-static>
                    <PenLine size={11} strokeWidth={2.4} />
                  </span>
                  <input
                    className={styles.staticInput}
                    value={s.value}
                    placeholder="static value"
                    spellCheck={false}
                    aria-label="Static value"
                    onClick={(e) => e.stopPropagation()}
                    onChange={(e) => setStaticValue(s.id, e.target.value)}
                  />
                  <button
                    type="button"
                    className={styles.nodeRemove}
                    aria-label="Remove static value"
                    onClick={(e) => {
                      e.stopPropagation();
                      removeStatic(s.id);
                    }}
                  >
                    <X size={13} strokeWidth={2.4} />
                  </button>
                  <span className={styles.port} aria-hidden />
                </div>
              );
            })}

            <button type="button" className={styles.addStatic} onClick={addStatic}>
              <Plus size={15} strokeWidth={2.4} /> Static value
            </button>
          </div>

          <div className={styles.col}>
            <div className={styles.colHeadRow}>
              <div className={styles.colHead} data-side="in">
                {targetLabel}
              </div>
              {allTargets.length > 8 && (
                <div className={styles.search}>
                  <Search size={14} strokeWidth={2.2} className={styles.searchIcon} aria-hidden />
                  <input
                    className={styles.searchInput}
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                    placeholder="Search…"
                    spellCheck={false}
                    aria-label={`Search ${targetLabel}`}
                  />
                </div>
              )}
            </div>

            {allTargets.length === 0 ? (
              <p className={styles.hint}>{emptyHint}</p>
            ) : (
              shownTargets.map((p) => {
                const custom = !chartSet.has(p);
                return (
                  <div
                    key={p}
                    ref={inSetRef(p)}
                    className={styles.node}
                    data-side="in"
                    data-armed={pending ? true : undefined}
                    data-wired={pathWired(p) || undefined}
                    onMouseEnter={() => setHoverPath(p)}
                    onMouseLeave={() => setHoverPath((cur) => (cur === p ? null : cur))}
                    onClick={() => clickPath(p)}
                    role="button"
                    tabIndex={0}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        clickPath(p);
                      }
                    }}
                  >
                    <span className={styles.port} aria-hidden />
                    <span className={`${styles.nodeLabel} mono`}>{p}</span>
                    {custom && (
                      <button
                        type="button"
                        className={styles.nodeRemove}
                        aria-label={`Remove ${p}`}
                        onClick={(e) => {
                          e.stopPropagation();
                          removePath(p);
                        }}
                      >
                        <X size={13} strokeWidth={2.4} />
                      </button>
                    )}
                  </div>
                );
              })
            )}
            {q && shownTargets.length === 0 && <p className={styles.hint}>No matches for “{query}”.</p>}

            {allowAddTarget && (
              <div className={styles.addRow}>
                <datalist id={listId}>
                  {suggestions.map((p) => (
                    <option key={p} value={p} />
                  ))}
                </datalist>
                <input
                  className={styles.addInput}
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
                  aria-label={`New ${targetLabel}`}
                />
                <button type="button" className={styles.addBtn} onClick={addPath} aria-label={`Add ${targetLabel}`}>
                  <Plus size={15} strokeWidth={2.4} />
                </button>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
