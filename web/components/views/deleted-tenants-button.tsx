"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Undo2, Trash2, History, AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Drawer } from "@/components/ui/drawer";
import { useToast } from "@/components/providers/toast-provider";
import { restoreTenant } from "@/lib/actions";
import { formatRelative } from "@/lib/format";
import type { Tombstone } from "@/lib/types";
import styles from "./deleted-tenants.module.css";

/** Deleted tenants, behind a button rather than on the page — this is occasional
 *  recovery work, not something the fleet view should carry every day.
 *
 *  Deleting tombstones the slug. Without that a delegated tenant returns within
 *  ~30s, because the discovery sweep re-registers every Lighthouse-delegated
 *  subscription and the slug is derived from the Entra directory id. Removing
 *  the tombstone is therefore the ONLY thing that lets a tenant come back — which
 *  is why Restore and Purge are the same operation underneath, and why Purge on a
 *  delegated tenant is called out as also un-blocking it. */
export function DeletedTenantsButton({ tombstones }: { tombstones: Tombstone[] }) {
  const [open, setOpen] = useState(false);
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();
  const [busy, setBusy] = useState<string | null>(null);

  if (tombstones.length === 0) return null;

  const act = (t: Tombstone, mode: "restore" | "purge") =>
    start(async () => {
      setBusy(t.slug + mode);
      const res = await restoreTenant(t.slug);
      setBusy(null);
      if (!res.ok) {
        toast({ title: "Couldn't update", description: res.error, tone: "danger" });
        return;
      }
      const label = t.name || t.slug;
      toast({
        title: mode === "restore" ? `Restored ${label}` : `Purged ${label}`,
        description:
          mode === "restore"
            ? t.tenantId
              ? "It will reappear, disabled, on the next discovery sweep or sign-in."
              : "Platform-hosted — nothing recreates it automatically; create it again to use it."
            : t.tenantId
              ? "Record removed. Its subscription is still delegated, so discovery will re-register it."
              : "Record removed.",
        tone: "success",
      });
      router.refresh();
    });

  return (
    <>
      <Button variant="ghost" icon={History} onClick={() => setOpen(true)}>
        Deleted tenants ({tombstones.length})
      </Button>

      <Drawer
        open={open}
        onClose={() => setOpen(false)}
        title="Deleted tenants"
        description="Blocked from being recreated. Users signing in from a deleted directory are refused rather than given a fresh tenant."
      >
        <ul className={styles.list}>
          {tombstones.map((t) => (
            <li key={t.slug} className={styles.row}>
              <div className={styles.rowHead}>
                <Trash2 size={15} className={styles.icon} aria-hidden />
                <div className={styles.meta}>
                  <div className={styles.name}>{t.name || t.slug}</div>
                  <div className={styles.sub}>
                    {t.slug}
                    {t.tenantId ? ` · directory ${t.tenantId}` : " · platform-hosted"}
                  </div>
                  {t.deletedAt ? (
                    <div className={styles.sub}>
                      deleted {formatRelative(new Date(t.deletedAt).getTime(), Date.now())}
                      {t.deletedBy ? ` by ${t.deletedBy}` : ""}
                    </div>
                  ) : null}
                </div>
              </div>

              {t.tenantId ? (
                <p className={styles.note}>
                  <AlertTriangle size={13} aria-hidden />
                  <span>
                    Its subscription is still delegated, so either action lets discovery re-register
                    it. To keep it gone, remove the Lighthouse delegation in Azure.
                  </span>
                </p>
              ) : null}

              <div className={styles.actions}>
                <Button
                  variant="secondary"
                  icon={Undo2}
                  loading={pending && busy === t.slug + "restore"}
                  onClick={() => act(t, "restore")}
                >
                  Restore
                </Button>
                <Button
                  variant="danger-ghost"
                  icon={Trash2}
                  loading={pending && busy === t.slug + "purge"}
                  onClick={() => act(t, "purge")}
                >
                  Purge
                </Button>
              </div>
            </li>
          ))}
        </ul>
      </Drawer>
    </>
  );
}
