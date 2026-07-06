"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/form";
import { useToast } from "@/components/providers/toast-provider";
import { setEntitlements } from "@/lib/actions";
import type { CatalogAgent } from "@/lib/types";
import styles from "./entitlements-panel.module.css";

export function EntitlementsPanel({
  slug,
  name,
  entitled,
  catalog,
}: {
  slug: string;
  name: string;
  entitled: string[];
  catalog: CatalogAgent[];
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
      const res = await setEntitlements(slug, [...selected]);
      if (res.ok) {
        toast({ title: `Updated entitlements for ${name}`, tone: "success" });
        router.refresh();
      } else {
        toast({ title: "Couldn't update entitlements", description: res.error, tone: "danger" });
      }
    });

  return (
    <section className={styles.panel} aria-label="Entitlements">
      <div className={styles.head}>
        <div className={styles.headText}>
          <h2 className={styles.title}>Entitlements</h2>
          <p className={styles.sub}>
            Which catalog agents {name} may enable. Removing one doesn&rsquo;t disable what&rsquo;s
            already running.
          </p>
        </div>
        <Button variant="primary" loading={pending} disabled={!dirty} onClick={save}>
          Save entitlements
        </Button>
      </div>

      {catalog.length === 0 ? (
        <div className={styles.none}>
          <ShieldCheck size={18} strokeWidth={2} aria-hidden />
          <p>No catalog agents yet. Author agents in the Catalog first, then entitle this tenant.</p>
        </div>
      ) : (
        <div className={styles.list}>
          {catalog.map((a) => (
            <Checkbox
              key={a.id}
              checked={selected.has(a.id)}
              onChange={() => toggle(a.id)}
              label={a.name}
              description={`${a.type === "hosted" ? "Hosted" : "Prompt"} · v${a.latestVersion} · ${a.model}`}
            />
          ))}
        </div>
      )}
    </section>
  );
}
