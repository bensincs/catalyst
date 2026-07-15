"use client";

import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Cable, Plus, X } from "lucide-react";
import type { WireLink } from "@/lib/types";
import styles from "./wiring-editor.module.css";

/** Parse output names from either Bicep source (`output <name> ...`) or a
 *  compiled ARM template (the `outputs` object). */
export function parseBicepOutputs(bicep: string): string[] {
  const s = bicep.trim();
  if (s.startsWith("{")) {
    try {
      const t = JSON.parse(s) as { outputs?: Record<string, unknown> };
      if (t && t.outputs && typeof t.outputs === "object") return Object.keys(t.outputs);
    } catch {
      /* not valid JSON yet */
    }
    return [];
  }
  const out: string[] = [];
  const re = /(?:^|\n)\s*output\s+([A-Za-z_][A-Za-z0-9_]*)\b/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(bicep))) out.push(m[1]);
  return Array.from(new Set(out));
}

type Line = { key: string; d: string; mx: number; my: number; active: boolean };

/** An interactive board that wires Bicep outputs (left) to Helm value paths
 *  (right). Click an output, then a Helm path, to connect them; the connectors
 *  are live bezier curves that track the nodes. */
export function WiringEditor({
  bicep,
  wiring,
  onChange,
}: {
  bicep: string;
  wiring: WireLink[];
  onChange: (w: WireLink[]) => void;
}) {
  const outputs = useMemo(() => parseBicepOutputs(bicep), [bicep]);
  const [paths, setPaths] = useState<string[]>(() =>
    Array.from(new Set(wiring.map((w) => w.helmPath).filter(Boolean))),
  );
  const [newPath, setNewPath] = useState("");
  const [pending, setPending] = useState<string | null>(null);

  const boardRef = useRef<HTMLDivElement>(null);
  const outRefs = useRef(new Map<string, HTMLElement>());
  const inRefs = useRef(new Map<string, HTMLElement>());
  const [lines, setLines] = useState<Line[]>([]);
  const [hoverPath, setHoverPath] = useState<string | null>(null);

  const recompute = useCallback(() => {
    const board = boardRef.current;
    if (!board) return;
    const box = board.getBoundingClientRect();
    const next: Line[] = [];
    for (const w of wiring) {
      const a = outRefs.current.get(w.bicepOutput);
      const b = inRefs.current.get(w.helmPath);
      if (!a || !b) continue;
      const ra = a.getBoundingClientRect();
      const rb = b.getBoundingClientRect();
      const x1 = ra.right - box.left;
      const y1 = ra.top + ra.height / 2 - box.top;
      const x2 = rb.left - box.left;
      const y2 = rb.top + rb.height / 2 - box.top;
      const dx = Math.max(36, (x2 - x1) / 2);
      next.push({
        key: `${w.bicepOutput}→${w.helmPath}`,
        d: `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`,
        mx: (x1 + x2) / 2,
        my: (y1 + y2) / 2,
        active:
          pending === w.bicepOutput || hoverPath === w.helmPath,
      });
    }
    setLines(next);
  }, [wiring, pending, hoverPath]);

  useLayoutEffect(() => {
    recompute();
  }, [recompute, paths, outputs]);

  useLayoutEffect(() => {
    const board = boardRef.current;
    if (!board) return;
    const ro = new ResizeObserver(recompute);
    ro.observe(board);
    window.addEventListener("resize", recompute);
    return () => {
      ro.disconnect();
      window.removeEventListener("resize", recompute);
    };
  }, [recompute]);

  const connected = (o: string, p: string) => wiring.some((w) => w.bicepOutput === o && w.helmPath === p);
  const outWired = (o: string) => wiring.some((w) => w.bicepOutput === o);
  const pathWired = (p: string) => wiring.some((w) => w.helmPath === p);

  const clickOut = (o: string) => setPending((cur) => (cur === o ? null : o));
  const clickIn = (p: string) => {
    if (!pending) return;
    onChange(
      connected(pending, p)
        ? wiring.filter((w) => !(w.bicepOutput === pending && w.helmPath === p))
        : [...wiring, { bicepOutput: pending, helmPath: p }],
    );
    setPending(null);
  };
  const addPath = () => {
    const p = newPath.trim();
    if (p && !paths.includes(p)) setPaths((prev) => [...prev, p]);
    setNewPath("");
  };
  const removePath = (p: string) => {
    setPaths((prev) => prev.filter((x) => x !== p));
    onChange(wiring.filter((w) => w.helmPath !== p));
  };

  const outSetRef = (o: string) => (el: HTMLElement | null) => {
    if (el) outRefs.current.set(o, el);
    else outRefs.current.delete(o);
  };
  const inSetRef = (p: string) => (el: HTMLElement | null) => {
    if (el) inRefs.current.set(p, el);
    else inRefs.current.delete(p);
  };

  return (
    <div className={styles.wrap}>
      <div className={styles.legend}>
        <Cable size={15} strokeWidth={2} />
        <span>
          {pending ? (
            <>
              Wiring <b className={styles.legendOut}>{pending}</b> — pick a Helm value to connect it to.
            </>
          ) : (
            <>Click a Bicep output, then a Helm value, to wire the infra into the chart.</>
          )}
        </span>
      </div>

      <div className={styles.board} ref={boardRef}>
        <svg className={styles.svg} aria-hidden>
          {lines.map((l) => (
            <path key={l.key} d={l.d} className={styles.wire} data-active={l.active || undefined} />
          ))}
        </svg>

        <div className={styles.col}>
          <div className={styles.colHead} data-side="out">
            Bicep outputs
          </div>
          {outputs.length === 0 ? (
            <p className={styles.hint}>
              Declare <code>output</code> values in the Azure infra module to wire them.
            </p>
          ) : (
            outputs.map((o) => (
              <button
                type="button"
                key={o}
                ref={outSetRef(o)}
                className={styles.node}
                data-side="out"
                data-selected={pending === o || undefined}
                data-wired={outWired(o) || undefined}
                onClick={() => clickOut(o)}
              >
                <span className={styles.nodeLabel}>{o}</span>
                <span className={styles.port} aria-hidden />
              </button>
            ))
          )}
        </div>

        <div className={styles.col}>
          <div className={styles.colHead} data-side="in">
            Helm values
          </div>
          {paths.map((p) => (
            <div
              key={p}
              ref={inSetRef(p)}
              className={styles.node}
              data-side="in"
              data-armed={pending ? true : undefined}
              data-wired={pathWired(p) || undefined}
              onMouseEnter={() => setHoverPath(p)}
              onMouseLeave={() => setHoverPath((cur) => (cur === p ? null : cur))}
              onClick={() => clickIn(p)}
              role="button"
              tabIndex={0}
            >
              <span className={styles.port} aria-hidden />
              <span className={`${styles.nodeLabel} mono`}>{p}</span>
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
            </div>
          ))}
          <div className={styles.addRow}>
            <input
              className={styles.addInput}
              value={newPath}
              onChange={(e) => setNewPath(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  addPath();
                }
              }}
              placeholder="database.host"
              spellCheck={false}
              aria-label="New Helm value path"
            />
            <button type="button" className={styles.addBtn} onClick={addPath} aria-label="Add Helm value">
              <Plus size={15} strokeWidth={2.4} />
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
