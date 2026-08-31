"use client";

import { useTransition, useState } from "react";
import { useRouter } from "next/navigation";
import { Undo2, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useToast } from "@/components/providers/toast-provider";
import { restoreTenant } from "@/lib/actions";
import { formatRelative } from "@/lib/format";
import type { Tombstone } from "@/lib/types";
import panel from "./entitlements-panel.module.css";

/** Deleted tenants, with a way to undo the deletion.
 *
 *  Deleting tombstones the slug — without that a delegated tenant returns within
 *  ~30s, because the discovery sweep re-registers every Lighthouse-delegated
 *  subscription and the slug is derived from the Entra directory id. Restoring
 *  lifts the tombstone; a delegated tenant then reappears (disabled) on the next
 *  sweep or sign-in.
 *
 *  Restoring does NOT bring back the tenant's Azure resources or its agents,
 *  deployments and memberships — those were destroyed at delete time. */
export function DeletedTenantsPanel({ tombstones }: { tombstones: Tombstone[] }) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();
  const [busy, setBusy] = useState<string | null>(null);

  if (tombstones.length === 0) return null;

  const restore = (t: Tombstone) =>
    start(async () => {
      setBusy(t.slug);
      const res = await restoreTenant(t.slug);
      setBusy(null);
      if (res.ok) {
        toast({
          title: `Restored ${t.name || t.slug}`,
          description: t.tenantId
            ? "It will reappear, disabled, on the next discovery sweep or sign-in."
            : "Platform-hosted — nothing will recreate it automatically; create it again to use it.",
          tone: "success",
        });
        router.refresh();
      } else {
        toast({ title: "Couldn't restore", description: res.error, tone: "danger" });
      }
    });

  return (
    <section className={panel.panel} aria-label="Deleted tenants" style={{ marginTop: 16 }}>
      <div className={panel.head}>
        <div className={panel.headText}>
          <h2 className={panel.title}>
            Deleted tenants{" "}
            <span style={{ fontWeight: 400, color: "var(--text-secondary)" }}>
              ({tombstones.length})
            </span>
          </h2>
          <p className={panel.sub}>
            These are blocked from being recreated. Users signing in from a deleted directory are
            refused rather than provisioned a fresh tenant. Restoring lifts that block — it does not
            bring back Azure resources or entitlements.
          </p>
        </div>
      </div>

      <ul style={{ listStyle: "none", margin: "12px 0 0", padding: 0, display: "flex", flexDirection: "column", gap: 6 }}>
        {tombstones.map((t) => (
          <li
            key={t.slug}
            style={{
              display: "flex",
              alignItems: "center",
              gap: 12,
              padding: "10px 12px",
              border: "1px solid var(--border)",
              borderRadius: "var(--radius-md)",
            }}
          >
            <Trash2 size={15} style={{ opacity: 0.5, flexShrink: 0 }} aria-hidden />
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ fontWeight: 600, fontSize: "var(--text-body-sm)" }}>
                {t.name || t.slug}
              </div>
              <div
                style={{
                  fontSize: 12,
                  color: "var(--text-secondary)",
                  overflow: "hidden",
                  textOverflow: "ellipsis",
                  whiteSpace: "nowrap",
                }}
              >
                {t.slug}
                {t.tenantId ? ` · directory ${t.tenantId}` : " · platform-hosted"}
                {t.deletedAt ? ` · deleted ${formatRelative(new Date(t.deletedAt).getTime(), Date.now())}` : ""}
              </div>
            </div>
            <Button
              variant="ghost"
              icon={Undo2}
              loading={pending && busy === t.slug}
              onClick={() => restore(t)}
            >
              Restore
            </Button>
          </li>
        ))}
      </ul>
    </section>
  );
}
