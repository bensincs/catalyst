"use client";

import { useMemo, useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Bot, Boxes, Brain, Layers, Lock, ShieldCheck, type LucideIcon } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/form";
import { useToast } from "@/components/providers/toast-provider";
import { setAllEntitlements } from "@/lib/actions";
import type { Application, CatalogAgent, Dependency, Infrastructure, MemoryStore } from "@/lib/types";
import styles from "./entitlements-panel.module.css";

type Kind = "infrastructure" | "application" | "agent" | "memory_store";
const kkey = (kind: Kind, id: string) => `${kind}:${id}`;
const splitKey = (k: string): [Kind, string] => {
  const i = k.indexOf(":");
  return [k.slice(0, i) as Kind, k.slice(i + 1)];
};

interface Row {
  kind: Kind;
  id: string;
  name: string;
  detail: string;
  deps: Dependency[];
}

const GROUPS: { kind: Kind; label: string; icon: LucideIcon }[] = [
  { kind: "infrastructure", label: "Infrastructure", icon: Boxes },
  { kind: "application", label: "Applications", icon: Layers },
  { kind: "agent", label: "Agents", icon: Bot },
  { kind: "memory_store", label: "Memory stores", icon: Brain },
];

// One panel for every kind a tenant can be entitled to. Entitlements cascade the
// dependency tree: ticking an application auto-ticks (and locks) everything it
// depends on — you can't un-entitle something another selection still needs.
export function EntitlementsPanel({
  slug,
  name,
  infrastructure,
  deployments,
  agents,
  stores,
  entitledInfrastructure,
  entitledDeployments,
  entitledAgents,
  entitledStores,
}: {
  slug: string;
  name: string;
  infrastructure: Infrastructure[];
  deployments: Application[];
  agents: CatalogAgent[];
  stores: MemoryStore[];
  entitledInfrastructure: string[];
  entitledDeployments: string[];
  entitledAgents: string[];
  entitledStores: string[];
}) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();

  const rows = useMemo<Row[]>(
    () => [
      ...infrastructure.map((i) => ({ kind: "infrastructure" as const, id: i.id, name: i.name, detail: "Bicep / Azure", deps: i.dependencies ?? [] })),
      ...deployments.map((a) => ({ kind: "application" as const, id: a.id, name: a.name, detail: a.chart || "Helm", deps: a.dependencies ?? [] })),
      ...agents.map((a) => ({
        kind: "agent" as const,
        id: a.id,
        name: a.name,
        detail: `${a.type === "hosted" ? "Hosted" : "Prompt"} · ${a.model}`,
        deps: a.definition.memoryStore ? [{ kind: "memory_store" as const, id: a.definition.memoryStore }] : [],
      })),
      ...stores.map((s) => ({ kind: "memory_store" as const, id: s.id, name: s.name, detail: "Memory", deps: [] as Dependency[] })),
    ],
    [infrastructure, deployments, agents, stores],
  );
  const depsByKey = useMemo(() => new Map(rows.map((r) => [kkey(r.kind, r.id), r.deps])), [rows]);

  const initial = useMemo(
    () =>
      new Set<string>([
        ...entitledInfrastructure.map((id) => kkey("infrastructure", id)),
        ...entitledDeployments.map((id) => kkey("application", id)),
        ...entitledAgents.map((id) => kkey("agent", id)),
        ...entitledStores.map((id) => kkey("memory_store", id)),
      ]),
    [entitledInfrastructure, entitledDeployments, entitledAgents, entitledStores],
  );
  // `picked` is what the admin explicitly chose; `required` is the transitive
  // dependency closure of those picks (auto-entitled + locked).
  const [picked, setPicked] = useState<Set<string>>(() => new Set(initial));

  const required = useMemo(() => {
    const req = new Set<string>();
    const visit = (kind: Kind, id: string) => {
      for (const d of depsByKey.get(kkey(kind, id)) ?? []) {
        const k = kkey(d.kind as Kind, d.id);
        if (!req.has(k)) {
          req.add(k);
          visit(d.kind as Kind, d.id);
        }
      }
    };
    for (const k of picked) {
      const [kind, id] = splitKey(k);
      visit(kind, id);
    }
    return req;
  }, [picked, depsByKey]);

  const checked = (k: string) => picked.has(k) || required.has(k);
  const locked = (k: string) => required.has(k); // needed by a selection — can't untick

  const toggle = (k: string) => {
    if (locked(k)) return;
    setPicked((prev) => {
      const next = new Set(prev);
      next.has(k) ? next.delete(k) : next.add(k);
      return next;
    });
  };

  // The effective entitlement set (picks + their required deps), by kind.
  const finalByKind = useMemo(() => {
    const all = new Set<string>([...picked, ...required]);
    const out: Record<Kind, string[]> = { infrastructure: [], application: [], agent: [], memory_store: [] };
    for (const k of all) {
      const [kind, id] = splitKey(k);
      out[kind].push(id);
    }
    return out;
  }, [picked, required]);

  const dirty = useMemo(() => {
    const all = new Set<string>([...picked, ...required]);
    return all.size !== initial.size || [...all].some((k) => !initial.has(k));
  }, [picked, required, initial]);

  const save = () =>
    start(async () => {
      const res = await setAllEntitlements(slug, finalByKind);
      if (!res.ok) {
        toast({ title: "Couldn't update entitlements", description: res.error, tone: "danger" });
      } else {
        toast({ title: `Updated entitlements for ${name}`, tone: "success" });
        router.refresh();
      }
    });

  const total = rows.length;

  return (
    <section className={styles.panel} aria-label="Entitlements">
      <div className={styles.head}>
        <div className={styles.headText}>
          <h2 className={styles.title}>Entitlements</h2>
          <p className={styles.sub}>
            What {name} may enable. Selecting one thing also entitles everything it depends on (shown
            locked); removing an entitlement doesn&rsquo;t disable what&rsquo;s already running.
          </p>
        </div>
        <Button variant="primary" loading={pending} disabled={!dirty} onClick={save}>
          Save entitlements
        </Button>
      </div>

      {total === 0 ? (
        <div className={styles.none}>
          <ShieldCheck size={18} strokeWidth={2} aria-hidden />
          <p>Nothing in the catalog yet. Author infrastructure, applications, agents, or stores first.</p>
        </div>
      ) : (
        <div className={styles.groups}>
          {GROUPS.map((g) => {
            const items = rows.filter((r) => r.kind === g.kind);
            if (items.length === 0) return null;
            return (
              <div key={g.kind} className={styles.group}>
                <div className={styles.groupHead}>
                  <g.icon size={13} strokeWidth={2} aria-hidden />
                  <span>{g.label}</span>
                  <span className={styles.groupCount}>{items.length}</span>
                </div>
                <div className={styles.list}>
                  {items.map((r) => {
                    const k = kkey(r.kind, r.id);
                    const isLocked = locked(k);
                    return (
                      <div key={k} className={styles.item} data-locked={isLocked || undefined}>
                        <Checkbox
                          checked={checked(k)}
                          disabled={isLocked}
                          onChange={() => toggle(k)}
                          label={r.name}
                          description={r.detail}
                        />
                        {isLocked && (
                          <span className={styles.reqTag} title="Required by a selection">
                            <Lock size={11} strokeWidth={2.4} aria-hidden /> required
                          </span>
                        )}
                      </div>
                    );
                  })}
                </div>
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}
