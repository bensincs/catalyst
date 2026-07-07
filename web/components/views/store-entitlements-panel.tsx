"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Database } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/form";
import { useToast } from "@/components/providers/toast-provider";
import { setStoreEntitlements } from "@/lib/actions";
import type { MemoryStore } from "@/lib/types";
import styles from "./entitlements-panel.module.css";

export function StoreEntitlementsPanel({
  slug,
  name,
  entitled,
  stores,
}: {
  slug: string;
  name: string;
  entitled: string[];
  stores: MemoryStore[];
}) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();
  const [selected, setSelected] = useState<Set<string>>(new Set(entitled));
  const dirty = selected.size !== entitled.length || !entitled.every((id) => selected.has(id));

  const toggle = (id: string) =>
    setSelected((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });

  const save = () =>
    start(async () => {
      const res = await setStoreEntitlements(slug, [...selected]);
      if (res.ok) {
        toast({ title: `Updated memory-store entitlements for ${name}`, tone: "success" });
        router.refresh();
      } else {
        toast({ title: "Couldn't update entitlements", description: res.error, tone: "danger" });
      }
    });

  return (
    <section className={styles.panel} aria-label="Memory store entitlements">
      <div className={styles.head}>
        <div className={styles.headText}>
          <h2 className={styles.title}>Memory stores</h2>
          <p className={styles.sub}>
            Which platform memory stores {name} may connect agents to. Stores an entitled agent
            requires are granted automatically.
          </p>
        </div>
        <Button variant="primary" loading={pending} disabled={!dirty} onClick={save}>
          Save entitlements
        </Button>
      </div>

      {stores.length === 0 ? (
        <div className={styles.none}>
          <Database size={18} strokeWidth={2} aria-hidden />
          <p>No platform memory stores yet. Author stores in Memory stores first, then entitle this tenant.</p>
        </div>
      ) : (
        <div className={styles.list}>
          {stores.map((s) => (
            <Checkbox
              key={s.id}
              checked={selected.has(s.id)}
              onChange={() => toggle(s.id)}
              label={s.name}
              description={s.description || "Platform memory store"}
            />
          ))}
        </div>
      )}
    </section>
  );
}
