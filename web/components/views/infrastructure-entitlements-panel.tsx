"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Boxes } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/form";
import { useToast } from "@/components/providers/toast-provider";
import { setInfrastructureEntitlements } from "@/lib/actions";
import type { Infrastructure } from "@/lib/types";
import styles from "./entitlements-panel.module.css";

export function InfrastructureEntitlementsPanel({
  slug,
  name,
  entitled,
  infrastructure,
}: {
  slug: string;
  name: string;
  entitled: string[];
  infrastructure: Infrastructure[];
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
      const res = await setInfrastructureEntitlements(slug, [...selected]);
      if (res.ok) {
        toast({ title: `Updated infrastructure entitlements for ${name}`, tone: "success" });
        router.refresh();
      } else {
        toast({ title: "Couldn't update entitlements", description: res.error, tone: "danger" });
      }
    });

  return (
    <section className={styles.panel} aria-label="Infrastructure entitlements">
      <div className={styles.head}>
        <div className={styles.headText}>
          <h2 className={styles.title}>Infrastructure</h2>
          <p className={styles.sub}>
            Which platform infrastructure {name} may provision into its own subscription.
          </p>
        </div>
        <Button variant="primary" loading={pending} disabled={!dirty} onClick={save}>
          Save entitlements
        </Button>
      </div>

      {infrastructure.length === 0 ? (
        <div className={styles.none}>
          <Boxes size={18} strokeWidth={2} aria-hidden />
          <p>No platform infrastructure yet. Author infrastructure in Infrastructure first, then entitle this tenant.</p>
        </div>
      ) : (
        <div className={styles.list}>
          {infrastructure.map((i) => (
            <Checkbox
              key={i.id}
              checked={selected.has(i.id)}
              onChange={() => toggle(i.id)}
              label={i.name}
              description={i.description || (i.bicepModule ?? "") || "Platform infrastructure"}
            />
          ))}
        </div>
      )}
    </section>
  );
}
