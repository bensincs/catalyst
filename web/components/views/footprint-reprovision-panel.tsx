"use client";

import { useTransition } from "react";
import { useRouter } from "next/navigation";
import { RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import { reprovisionFootprint } from "@/lib/actions";
import styles from "./entitlements-panel.module.css";

const TONE = { ready: "success", provisioning: "info", failed: "danger" } as const;
const LABEL = { ready: "Provisioned", provisioning: "Provisioning", failed: "Failed" } as const;

/** Platform control to re-submit a tenant's footprint template (reconciler,
 * Foundry, AKS) so config fixes and new platform features reach an
 * already-provisioned tenant. Idempotent — safe to run anytime. */
export function FootprintReprovisionPanel({
  slug,
  name,
  footprintState,
}: {
  slug: string;
  name: string;
  footprintState?: string;
}) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();
  const state = (footprintState ?? "") as keyof typeof LABEL;
  const provisioning = footprintState === "provisioning";

  const run = () =>
    start(async () => {
      const res = await reprovisionFootprint(slug);
      if (res.ok) {
        toast({
          title: `Re-provisioning ${name}`,
          description: "Re-submitting the footprint into the tenant's subscription.",
          tone: "success",
        });
        router.refresh();
      } else {
        toast({ title: "Couldn't re-provision", description: res.error, tone: "danger" });
      }
    });

  return (
    <section className={styles.panel} aria-label="Tenant footprint">
      <div className={styles.head}>
        <div className={styles.headText}>
          <h2 className={styles.title}>
            Footprint{" "}
            {footprintState ? (
              <StatusBadge tone={TONE[state] ?? "neutral"} label={LABEL[state] ?? footprintState} variant="soft" />
            ) : null}
          </h2>
          <p className={styles.sub}>
            Re-submit the footprint template (reconciler, Foundry, AKS) into {name}&apos;s subscription so
            configuration fixes and new platform features reach this tenant. Idempotent — it updates the
            existing resources in place.
          </p>
        </div>
        <Button variant="secondary" icon={RefreshCw} loading={pending || provisioning} onClick={run}>
          Re-provision
        </Button>
      </div>
    </section>
  );
}
