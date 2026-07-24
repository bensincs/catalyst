"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { UserPlus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useToast } from "@/components/providers/toast-provider";
import { addTenantMember, removeTenantMember } from "@/lib/actions";
import type { TenantMember } from "@/lib/api";
import styles from "./entitlements-panel.module.css";

/** Platform control to assign/revoke users on a platform-hosted tenant. Users are
 *  assigned by email; their Entra oid binds on first sign-in. */
export function TenantMembersPanel({
  slug,
  name,
  members,
}: {
  slug: string;
  name: string;
  members: TenantMember[];
}) {
  const router = useRouter();
  const { toast } = useToast();
  const [email, setEmail] = useState("");
  const [pending, start] = useTransition();

  const add = () =>
    start(async () => {
      const value = email.trim();
      if (!value) return;
      const res = await addTenantMember(slug, value);
      if (res.ok) {
        toast({ title: `Assigned ${value}`, description: `Added to ${name}.`, tone: "success" });
        setEmail("");
        router.refresh();
      } else {
        toast({ title: "Couldn't assign", description: res.error, tone: "danger" });
      }
    });

  const remove = (value: string) =>
    start(async () => {
      const res = await removeTenantMember(slug, value);
      if (res.ok) {
        toast({ title: `Removed ${value}`, tone: "success" });
        router.refresh();
      } else {
        toast({ title: "Couldn't remove", description: res.error, tone: "danger" });
      }
    });

  return (
    <section className={styles.panel} aria-label="Tenant members">
      <div className={styles.head}>
        <div className={styles.headText}>
          <h2 className={styles.title}>Members</h2>
          <p className={styles.sub}>
            Assign users to {name}. Access is granted by email; a user&apos;s directory identity binds on their
            first sign-in. Members can operate this tenant from the console.
          </p>
        </div>
      </div>

      <div style={{ display: "flex", gap: 8, margin: "12px 0", flexWrap: "wrap" }}>
        <input
          type="email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") add();
          }}
          placeholder="user@example.com"
          style={{
            flex: "1 1 240px",
            padding: "8px 10px",
            borderRadius: 8,
            border: "1px solid var(--border, #333)",
            background: "transparent",
            color: "inherit",
            font: "inherit",
          }}
        />
        <Button variant="secondary" icon={UserPlus} loading={pending} onClick={add}>
          Assign
        </Button>
      </div>

      {members.length === 0 ? (
        <p className={styles.sub}>No users assigned yet.</p>
      ) : (
        <ul style={{ listStyle: "none", margin: 0, padding: 0, display: "flex", flexDirection: "column", gap: 6 }}>
          {members.map((m) => (
            <li
              key={m.email}
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                gap: 8,
                padding: "6px 10px",
                borderRadius: 8,
                border: "1px solid var(--border, #333)",
              }}
            >
              <span>
                {m.email}
                {m.oid ? "" : "  ·  pending first sign-in"}
              </span>
              <Button variant="ghost" icon={X} loading={pending} onClick={() => remove(m.email)} aria-label={`Remove ${m.email}`}>
                Remove
              </Button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
