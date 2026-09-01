"use client";

import { useEffect, useState } from "react";
import { AlertTriangle, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/modal";
import { TextInput } from "@/components/ui/form";
import { getResourceUsage } from "@/lib/actions";
import type { ResourceKind } from "@/lib/actions";
import styles from "./confirm-delete.module.css";

/** Confirms deleting a catalog entity, and says what it costs first.
 *
 *  Deleting one is not a local edit: it strips the entity from every tenant's
 *  entitlements and drops every per-tenant enablement row, and the reconciler
 *  then prunes the workloads. A single click could uninstall a running
 *  application from every tenant that had it, which the operator has to be told
 *  before rather than discover after.
 *
 *  The confirmation is proportionate to the blast radius: nothing in use is a
 *  plain confirm, while anything a tenant is actually running has to be typed
 *  out — the same bar tenant deletion already uses. */
export function ConfirmDelete({
  kind,
  id,
  name,
  noun,
  onConfirm,
  pending = false,
}: {
  kind: ResourceKind;
  id: string;
  name: string;
  /** What this is, for the copy — "deployment", "agent", "memory store". */
  noun: string;
  onConfirm: () => void;
  pending?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const [usage, setUsage] = useState<{ entitled: number; enabled: number } | null>(null);
  const [typed, setTyped] = useState("");

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setUsage(null);
    getResourceUsage(kind, id).then((u) => {
      if (!cancelled) setUsage(u);
    });
    return () => {
      cancelled = true;
    };
  }, [open, kind, id]);

  const inUse = (usage?.enabled ?? 0) > 0;
  // Unknown usage is treated as in-use: failing to load the count must not
  // quietly downgrade the confirmation on something that may be running.
  const strict = usage === null || inUse;
  const armed = strict ? typed.trim() === name.trim() : true;

  const close = () => {
    setOpen(false);
    setTyped("");
  };

  return (
    <>
      <Button size="sm" variant="danger-ghost" icon={Trash2} onClick={() => setOpen(true)}>
        Delete
      </Button>
      <Modal
        open={open}
        onClose={close}
        title={`Delete ${name}?`}
        description={`This removes the ${noun} from the catalog for every tenant. It cannot be undone.`}
        footer={
          <>
            <Button variant="secondary" onClick={close} disabled={pending}>
              Cancel
            </Button>
            <Button
              variant="danger"
              icon={Trash2}
              disabled={!armed || pending}
              loading={pending}
              onClick={() => {
                close();
                onConfirm();
              }}
            >
              Delete {noun}
            </Button>
          </>
        }
      >
        {usage === null ? (
          <p className={styles.body}>Checking where this is in use…</p>
        ) : inUse ? (
          <p className={styles.warn}>
            <AlertTriangle size={15} strokeWidth={2.2} aria-hidden />
            <span>
              <strong>
                {usage.enabled} tenant{usage.enabled === 1 ? "" : "s"}
              </strong>{" "}
              {usage.enabled === 1 ? "is" : "are"} running this now
              {usage.entitled > usage.enabled
                ? `, and ${usage.entitled} ${usage.entitled === 1 ? "is" : "are"} entitled to it`
                : ""}
              . Deleting it stops it there.
            </span>
          </p>
        ) : usage.entitled > 0 ? (
          <p className={styles.body}>
            {usage.entitled} tenant{usage.entitled === 1 ? " is" : "s are"} entitled to this, but
            none are running it.
          </p>
        ) : (
          <p className={styles.body}>No tenant is entitled to this, so nothing is running it.</p>
        )}

        {strict && (
          <label className={styles.confirm}>
            <span>
              Type <strong>{name}</strong> to confirm
            </span>
            <TextInput
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              placeholder={name}
              spellCheck={false}
              autoComplete="off"
              aria-label={`Type ${name} to confirm deletion`}
            />
          </label>
        )}
      </Modal>
    </>
  );
}
