"use client";

import { useTransition } from "react";
import { useRouter } from "next/navigation";
import { ShieldCheck, ShieldOff } from "lucide-react";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import { setTenantEnabled } from "@/lib/actions";
import styles from "./entitlements-panel.module.css";

/** Platform control to enable (approve) or disable a tenant's access — signing
 * in to the console and running its reconciler. */
export function TenantAccessPanel({
  slug,
  name,
  enabled,
}: {
  slug: string;
  name: string;
  enabled: boolean;
}) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();

  const toggle = () =>
    start(async () => {
      const res = await setTenantEnabled(slug, !enabled);
      if (res.ok) {
        toast({ title: enabled ? `Disabled ${name}` : `Enabled ${name}`, tone: "success" });
        router.refresh();
      } else {
        toast({ title: "Couldn't update access", description: res.error, tone: "danger" });
      }
    });

  return (
    <section className={styles.panel} aria-label="Tenant access">
      <div className={styles.head}>
        <div className={styles.headText}>
          <h2 className={styles.title}>
            Access <StatusBadge tone={enabled ? "success" : "warning"} label={enabled ? "Enabled" : "Disabled"} variant="soft" />
          </h2>
          <p className={styles.sub}>
            Whether {name} can sign in to the console and run its reconciler. Disable to cut a tenant
            off; enable to approve a pending one.
          </p>
        </div>
        <Button
          variant={enabled ? "danger-ghost" : "primary"}
          icon={enabled ? ShieldOff : ShieldCheck}
          loading={pending}
          onClick={toggle}
        >
          {enabled ? "Disable access" : "Enable access"}
        </Button>
      </div>
    </section>
  );
}
