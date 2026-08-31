"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Trash2, AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useToast } from "@/components/providers/toast-provider";
import { deleteTenant } from "@/lib/actions";
import panel from "./entitlements-panel.module.css";
import styles from "./tenant-members-panel.module.css";

/** Deleting a tenant is irreversible and destroys live Azure resources, so it
 *  sits behind type-to-confirm rather than a single click. */
export function DangerZonePanel({
  slug,
  name,
  hostingMode,
  subscriptionId,
}: {
  slug: string;
  name: string;
  hostingMode: "delegated" | "platform";
  subscriptionId?: string;
}) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();
  const [confirm, setConfirm] = useState("");
  const [purgeAzure, setPurgeAzure] = useState(true);

  const armed = confirm.trim() === name.trim() && name.trim() !== "";

  const remove = () =>
    start(async () => {
      const res = await deleteTenant(slug, purgeAzure);
      if (res.ok) {
        toast({
          title: `Deleted ${name}`,
          description: purgeAzure
            ? "Azure resource groups are being reclaimed."
            : "Cortex records removed; Azure resources left in place.",
          tone: "success",
        });
        router.push("/");
      } else {
        toast({ title: "Couldn't delete", description: res.error, tone: "danger" });
      }
    });

  return (
    <section className={panel.panel} aria-label="Danger zone">
      <div className={panel.head}>
        <div className={panel.headText}>
          <h2 className={panel.title}>Danger zone</h2>
          <p className={panel.sub}>
            Deleting {name} removes its agents, deployments, memory stores and memberships, and
            tombstones it so{" "}
            {hostingMode === "delegated"
              ? "Lighthouse discovery won't recreate it"
              : "it can't be recreated"}
            . This cannot be undone.
          </p>
        </div>
      </div>

      <div style={{ marginTop: 14, display: "flex", flexDirection: "column", gap: 12 }}>
        <label style={{ display: "flex", gap: 8, alignItems: "flex-start", fontSize: 13 }}>
          <input
            type="checkbox"
            checked={purgeAzure}
            onChange={(e) => setPurgeAzure(e.target.checked)}
            style={{ marginTop: 3 }}
          />
          <span>
            <strong>Also delete its Azure resource groups</strong>
            <span style={{ display: "block", color: "var(--text-secondary)", fontSize: 12 }}>
              Destroys the AKS cluster, Foundry project and reconciler
              {subscriptionId ? ` in subscription ${subscriptionId}` : ""}. Uncheck to remove only
              the Cortex records and leave Azure untouched.
            </span>
          </span>
        </label>

        {purgeAzure ? (
          <p
            style={{
              display: "flex",
              gap: 8,
              alignItems: "flex-start",
              margin: 0,
              fontSize: 12,
              color: "var(--text-secondary)",
            }}
          >
            <AlertTriangle size={14} style={{ marginTop: 2, flexShrink: 0 }} aria-hidden />
            <span>
              Any data in the Foundry project or on the cluster goes with it. There is no backup.
            </span>
          </p>
        ) : null}

        <label style={{ display: "flex", flexDirection: "column", gap: 4, maxWidth: 420 }}>
          <span style={{ fontSize: 12, color: "var(--text-secondary)" }}>
            Type <strong>{name}</strong> to confirm
          </span>
          <input
            className={styles.input}
            style={{ paddingLeft: 12 }}
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            placeholder={name}
            autoComplete="off"
            spellCheck={false}
          />
        </label>

        <div>
          <Button variant="danger" icon={Trash2} loading={pending} disabled={!armed} onClick={remove}>
            Delete tenant
          </Button>
        </div>
      </div>
    </section>
  );
}
