"use client";

import { Bot, Boxes, Database, Rocket, type LucideIcon } from "lucide-react";
import type { Dependency, DepKind, DepOption } from "@/lib/types";
import styles from "./dependency-picker.module.css";

const KIND_META: Record<DepKind, { icon: LucideIcon; label: string }> = {
  infrastructure: { icon: Boxes, label: "Infrastructure" },
  application: { icon: Rocket, label: "Applications" },
  agent: { icon: Bot, label: "Agents" },
  memory_store: { icon: Database, label: "Memory stores" },
};

const GROUP_ORDER: DepKind[] = ["infrastructure", "application", "agent", "memory_store"];

/** A typed dependency picker: toggleable pills grouped by kind. Emits the full
 *  {kind,id} edge set. The parent pre-filters `options` to the allowed edges +
 *  entitled/owned candidates, so this only renders and toggles. */
export function DependencyPicker({
  options,
  value,
  onChange,
}: {
  options: DepOption[];
  value: Dependency[];
  onChange: (deps: Dependency[]) => void;
}) {
  const has = (o: DepOption) => value.some((d) => d.kind === o.kind && d.id === o.id);
  const toggle = (o: DepOption) =>
    onChange(
      has(o)
        ? value.filter((d) => !(d.kind === o.kind && d.id === o.id))
        : [...value, { kind: o.kind, id: o.id }],
    );

  const groups = GROUP_ORDER.map((kind) => ({
    kind,
    items: options.filter((o) => o.kind === kind),
  })).filter((g) => g.items.length > 0);

  return (
    <div className={styles.groups}>
      {groups.map((g) => {
        const Icon = KIND_META[g.kind].icon;
        return (
          <div key={g.kind} className={styles.group}>
            {groups.length > 1 && <div className={styles.groupLabel}>{KIND_META[g.kind].label}</div>}
            <div className={styles.grid}>
              {g.items.map((o) => {
                const on = has(o);
                return (
                  <button
                    type="button"
                    key={`${o.kind}:${o.id}`}
                    className={styles.chip}
                    data-on={on || undefined}
                    onClick={() => toggle(o)}
                  >
                    <Icon size={14} strokeWidth={2.2} />
                    <span>{o.name}</span>
                  </button>
                );
              })}
            </div>
          </div>
        );
      })}
    </div>
  );
}
