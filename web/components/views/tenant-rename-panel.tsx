"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Check, Pencil, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useToast } from "@/components/providers/toast-provider";
import { renameTenant } from "@/lib/actions";
import styles from "./tenant-members-panel.module.css";
import panel from "./entitlements-panel.module.css";

/** Platform control to rename a tenant's display name. */
export function TenantRenamePanel({ slug, name }: { slug: string; name: string }) {
  const router = useRouter();
  const { toast } = useToast();
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(name);
  const [pending, start] = useTransition();

  const save = () =>
    start(async () => {
      const v = value.trim();
      if (!v || v === name) {
        setEditing(false);
        return;
      }
      const res = await renameTenant(slug, v);
      if (res.ok) {
        toast({ title: "Renamed", description: `“${name}” → “${v}”.`, tone: "success" });
        setEditing(false);
        router.refresh();
      } else {
        toast({ title: "Couldn't rename", description: res.error, tone: "danger" });
      }
    });

  return (
    <section className={panel.panel} aria-label="Tenant name">
      <div className={panel.head}>
        <div className={panel.headText}>
          <h2 className={panel.title}>Name</h2>
          <p className={panel.sub}>The tenant&apos;s display name across the console.</p>
        </div>
        {!editing ? (
          <Button variant="secondary" icon={Pencil} onClick={() => { setValue(name); setEditing(true); }}>
            Rename
          </Button>
        ) : null}
      </div>

      {editing ? (
        <div className={styles.field} style={{ marginTop: 12 }}>
          <input
            className={styles.input}
            style={{ paddingLeft: 12 }}
            autoFocus
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") save();
              if (e.key === "Escape") setEditing(false);
            }}
            aria-label="Tenant name"
          />
          <Button icon={Check} loading={pending} onClick={save}>
            Save
          </Button>
          <Button variant="ghost" icon={X} onClick={() => setEditing(false)}>
            Cancel
          </Button>
        </div>
      ) : (
        <p style={{ marginTop: 10, fontSize: "var(--text-body)", fontWeight: 600 }}>{name}</p>
      )}
    </section>
  );
}
