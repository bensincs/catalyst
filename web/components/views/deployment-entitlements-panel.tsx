"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Rocket } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/form";
import { useToast } from "@/components/providers/toast-provider";
import { setDeploymentEntitlements } from "@/lib/actions";
import type { Application } from "@/lib/types";
import styles from "./entitlements-panel.module.css";

export function DeploymentEntitlementsPanel({
  slug,
  name,
  entitled,
  deployments,
}: {
  slug: string;
  name: string;
  entitled: string[];
  deployments: Application[];
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
      const res = await setDeploymentEntitlements(slug, [...selected]);
      if (res.ok) {
        toast({ title: `Updated deployment entitlements for ${name}`, tone: "success" });
        router.refresh();
      } else {
        toast({ title: "Couldn't update entitlements", description: res.error, tone: "danger" });
      }
    });

  return (
    <section className={styles.panel} aria-label="Deployment entitlements">
      <div className={styles.head}>
        <div className={styles.headText}>
          <h2 className={styles.title}>Deployments</h2>
          <p className={styles.sub}>
            Which platform deployments {name} may enable into its own cluster.
          </p>
        </div>
        <Button variant="primary" loading={pending} disabled={!dirty} onClick={save}>
          Save entitlements
        </Button>
      </div>

      {deployments.length === 0 ? (
        <div className={styles.none}>
          <Rocket size={18} strokeWidth={2} aria-hidden />
          <p>No platform deployments yet. Author deployments in Deployments first, then entitle this tenant.</p>
        </div>
      ) : (
        <div className={styles.list}>
          {deployments.map((d) => (
            <Checkbox
              key={d.id}
              checked={selected.has(d.id)}
              onChange={() => toggle(d.id)}
              label={d.name}
              description={d.description || `${d.chart}${d.targetRevision ? `@${d.targetRevision}` : ""}`}
            />
          ))}
        </div>
      )}
    </section>
  );
}
