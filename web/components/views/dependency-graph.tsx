"use client";

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { Bot, Boxes, Brain, Layers, type LucideIcon } from "lucide-react";
import type { Application, EnabledAgent, Infrastructure, MemoryStore } from "@/lib/types";
import { agentStatus, applicationStatus, infraStatus, storeStatus } from "@/lib/status";
import styles from "./dependency-graph.module.css";

type Kind = "infrastructure" | "application" | "agent" | "memory_store";
type Tone = "success" | "info" | "warning" | "danger" | "neutral";

interface GNode {
  key: string;
  id: string;
  kind: Kind;
  name: string;
  tone: Tone;
  state: string;
  pulse: boolean;
  href?: string;
}
interface GEdge {
  from: string; // dependency (upstream / provider) key
  to: string; // dependent (downstream) key
}

const COLUMNS: { kind: Kind; label: string; icon: LucideIcon }[] = [
  { kind: "infrastructure", label: "Infrastructure", icon: Boxes },
  { kind: "application", label: "Applications", icon: Layers },
  { kind: "agent", label: "Agents", icon: Bot },
  { kind: "memory_store", label: "Memory stores", icon: Brain },
];
const ICON: Record<Kind, LucideIcon> = { infrastructure: Boxes, application: Layers, agent: Bot, memory_store: Brain };

const nkey = (kind: Kind, id: string) => `${kind}:${id}`;

export function DependencyGraph({
  infrastructure,
  applications,
  agents,
  stores,
}: {
  infrastructure: Infrastructure[];
  applications: Application[];
  agents: EnabledAgent[];
  stores: MemoryStore[];
}) {
  const { nodes, edges, byColumn } = useMemo(() => {
    const nodes: GNode[] = [];
    const present = new Set<string>();
    const add = (n: GNode) => {
      if (present.has(n.key)) return;
      present.add(n.key);
      nodes.push(n);
    };

    for (const i of infrastructure) {
      const t = infraStatus(i.infraState);
      add({ key: nkey("infrastructure", i.id), id: i.id, kind: "infrastructure", name: i.name, tone: t.tone, state: t.label, pulse: t.pulse, href: "/infrastructure" });
    }
    for (const a of applications) {
      const t = applicationStatus(a);
      add({ key: nkey("application", a.id), id: a.id, kind: "application", name: a.name, tone: t.tone, state: t.label, pulse: t.pulse, href: "/deployments" });
    }
    for (const a of agents) {
      const t = agentStatus(a);
      add({ key: nkey("agent", a.id), id: a.id, kind: "agent", name: a.name, tone: t.tone, state: t.label, pulse: t.pulse, href: "/agents" });
    }
    for (const s of stores) {
      const t = storeStatus(s);
      add({ key: nkey("memory_store", s.id), id: s.id, kind: "memory_store", name: s.name, tone: t.tone, state: t.label, pulse: t.pulse, href: "/memory-stores" });
    }

    const edges: GEdge[] = [];
    const edge = (fromKind: Kind, fromId: string, toKey: string) => {
      const from = nkey(fromKind, fromId);
      if (present.has(from) && present.has(toKey) && from !== toKey) edges.push({ from, to: toKey });
    };
    for (const i of infrastructure) {
      for (const d of i.dependencies ?? []) edge(d.kind as Kind, d.id, nkey("infrastructure", i.id));
    }
    for (const a of applications) {
      for (const d of a.dependencies ?? []) edge(d.kind as Kind, d.id, nkey("application", a.id));
    }
    for (const a of agents) {
      if (a.memoryStore) edge("memory_store", a.memoryStore, nkey("agent", a.id));
    }

    const byColumn = COLUMNS.map((c) => ({ ...c, nodes: nodes.filter((n) => n.kind === c.kind) }));
    return { nodes, edges, byColumn };
  }, [infrastructure, applications, agents, stores]);

  const total = nodes.length;
  const live = nodes.filter((n) => n.tone === "success").length;
  const working = nodes.filter((n) => n.tone === "info").length;
  const attention = nodes.filter((n) => n.tone === "warning" || n.tone === "danger").length;

  // ── Edge geometry (measured SVG overlay) ───────────────────────────────────
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const nodeRefs = useRef(new Map<string, HTMLElement>());
  const setNodeRef = useCallback((key: string) => (el: HTMLElement | null) => {
    if (el) nodeRefs.current.set(key, el);
    else nodeRefs.current.delete(key);
  }, []);
  const [paths, setPaths] = useState<{ from: string; to: string; d: string }[]>([]);
  const [size, setSize] = useState({ w: 0, h: 0 });

  const measure = useCallback(() => {
    const wrap = wrapRef.current;
    if (!wrap) return;
    const box = wrap.getBoundingClientRect();
    setSize({ w: box.width, h: box.height });
    const next: { from: string; to: string; d: string }[] = [];
    for (const e of edges) {
      const a = nodeRefs.current.get(e.from);
      const b = nodeRefs.current.get(e.to);
      if (!a || !b) continue;
      const ra = a.getBoundingClientRect();
      const rb = b.getBoundingClientRect();
      // dependency (from) sits in a left column → exit its right edge into the
      // dependent's (to) left edge.
      const x1 = ra.right - box.left;
      const y1 = ra.top - box.top + ra.height / 2;
      const x2 = rb.left - box.left;
      const y2 = rb.top - box.top + rb.height / 2;
      const dx = Math.max(28, (x2 - x1) * 0.5);
      next.push({ from: e.from, to: e.to, d: `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}` });
    }
    setPaths(next);
  }, [edges]);

  useLayoutEffect(() => {
    measure();
  }, [measure, byColumn]);
  useEffect(() => {
    const wrap = wrapRef.current;
    if (!wrap) return;
    const ro = new ResizeObserver(() => measure());
    ro.observe(wrap);
    window.addEventListener("resize", measure);
    return () => {
      ro.disconnect();
      window.removeEventListener("resize", measure);
    };
  }, [measure]);

  // ── Hover / focus highlight ────────────────────────────────────────────────
  const [active, setActive] = useState<string | null>(null);
  const connected = useMemo(() => {
    if (!active) return null;
    const nodeSet = new Set<string>([active]);
    const edgeSet = new Set<string>();
    edges.forEach((e, i) => {
      if (e.from === active || e.to === active) {
        edgeSet.add(String(i));
        nodeSet.add(e.from);
        nodeSet.add(e.to);
      }
    });
    return { nodeSet, edgeSet };
  }, [active, edges]);

  if (total === 0) {
    return (
      <div className={styles.empty}>
        <div className={styles.emptyGlyph} aria-hidden>
          <Layers size={22} strokeWidth={1.75} />
        </div>
        <p className={styles.emptyTitle}>Nothing enabled yet</p>
        <p className={styles.emptyBody}>
          Enable an application and Cortex installs its infrastructure, agents, and memory stores together as one
          dependency graph — provisioned into your own subscription.
        </p>
        <Link className={styles.emptyLink} href="/deployments">
          Browse applications →
        </Link>
      </div>
    );
  }

  return (
    <div className={styles.root}>
      <div className={styles.summary}>
        <span className={styles.summaryTotal}>
          {total} resource{total === 1 ? "" : "s"}
        </span>
        <span className={styles.summaryDivider} aria-hidden />
        {live > 0 && <span className={styles.summaryStat} data-tone="success">{live} live</span>}
        {working > 0 && <span className={styles.summaryStat} data-tone="info">{working} deploying</span>}
        {attention > 0 && <span className={styles.summaryStat} data-tone="warning">{attention} need attention</span>}
        {live === total && <span className={styles.summaryStat} data-tone="success">all live</span>}
      </div>

      <div className={styles.board} ref={wrapRef} data-dim={active ? true : undefined}>
        <svg className={styles.edges} width={size.w} height={size.h} aria-hidden focusable="false">
          {paths.map((p, i) => {
            const on = connected?.edgeSet.has(String(edges.findIndex((e) => e.from === p.from && e.to === p.to)));
            const isActive = connected ? Boolean(on) : false;
            return (
              <path
                key={`${p.from}-${p.to}`}
                d={p.d}
                className={styles.edge}
                data-active={isActive || undefined}
                data-muted={connected && !isActive ? true : undefined}
              />
            );
          })}
        </svg>

        <div className={styles.columns}>
          {byColumn.map((col) => (
            <div className={styles.column} key={col.kind}>
              <div className={styles.colHead}>
                <col.icon size={13} strokeWidth={2} aria-hidden />
                <span>{col.label}</span>
                <span className={styles.colCount}>{col.nodes.length}</span>
              </div>
              <div className={styles.colNodes}>
                {col.nodes.length === 0 && <div className={styles.colEmpty} aria-hidden>—</div>}
                {col.nodes.map((n) => {
                  const Icon = ICON[n.kind];
                  const dim = connected ? !connected.nodeSet.has(n.key) : false;
                  const NodeInner = (
                    <>
                      <span className={styles.nodeDot} data-tone={n.tone} data-pulse={n.pulse || undefined} aria-hidden />
                      <span className={styles.nodeIcon} aria-hidden>
                        <Icon size={15} strokeWidth={2} />
                      </span>
                      <span className={styles.nodeText}>
                        <span className={styles.nodeName}>{n.name}</span>
                        <span className={styles.nodeState}>{n.state}</span>
                      </span>
                    </>
                  );
                  const common = {
                    className: styles.node,
                    "data-tone": n.tone,
                    "data-dim": dim || undefined,
                    ref: setNodeRef(n.key) as never,
                    onMouseEnter: () => setActive(n.key),
                    onMouseLeave: () => setActive((a) => (a === n.key ? null : a)),
                    onFocus: () => setActive(n.key),
                    onBlur: () => setActive((a) => (a === n.key ? null : a)),
                    "aria-label": `${col.label.replace(/s$/, "")}: ${n.name} — ${n.state}`,
                  };
                  return n.href ? (
                    <Link key={n.key} href={n.href} {...common}>
                      {NodeInner}
                    </Link>
                  ) : (
                    <div key={n.key} {...common} tabIndex={0}>
                      {NodeInner}
                    </div>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      </div>

      <div className={styles.legend}>
        <span className={styles.legendItem}><span className={styles.legendDot} data-tone="success" />Live</span>
        <span className={styles.legendItem}><span className={styles.legendDot} data-tone="info" />Deploying</span>
        <span className={styles.legendItem}><span className={styles.legendDot} data-tone="warning" />Waiting / drift</span>
        <span className={styles.legendItem}><span className={styles.legendDot} data-tone="danger" />Failed</span>
      </div>
    </div>
  );
}
