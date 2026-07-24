"use client";

import { useEffect, useRef, useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { UserPlus, X, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useToast } from "@/components/providers/toast-provider";
import { addTenantMember, removeTenantMember } from "@/lib/actions";
import { searchUsers } from "@/lib/tenant-actions";
import type { TenantMember, UserOption } from "@/lib/api";
import styles from "./tenant-members-panel.module.css";
import panel from "./entitlements-panel.module.css";

const GUID = /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

/** Platform control to assign/revoke users on a tenant. Assigning is a type-ahead
 *  over previously-signed-in users; a full email or Entra object id can also be
 *  entered directly for someone who hasn't signed in yet (oid binds on first
 *  sign-in). */
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
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<UserOption[]>([]);
  const [open, setOpen] = useState(false);
  const [pending, start] = useTransition();
  const boxRef = useRef<HTMLDivElement | null>(null);

  const assigned = new Set(members.map((m) => m.principal.toLowerCase()));
  const value = query.trim();
  const freeTextValid = value.includes("@") || GUID.test(value);

  // Debounced live search over signed-in users.
  useEffect(() => {
    const q = query.trim();
    if (q.length < 1) {
      setResults([]);
      return;
    }
    let alive = true;
    const t = setTimeout(async () => {
      const r = await searchUsers(q);
      if (alive) setResults(r);
    }, 180);
    return () => {
      alive = false;
      clearTimeout(t);
    };
  }, [query]);

  // Close on outside click.
  useEffect(() => {
    const onDown = (e: PointerEvent) => {
      if (boxRef.current && !boxRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", onDown);
    return () => document.removeEventListener("pointerdown", onDown);
  }, []);

  const assign = (identifier: string) =>
    start(async () => {
      const v = identifier.trim();
      if (!v) return;
      const res = await addTenantMember(slug, v);
      if (res.ok) {
        toast({ title: `Assigned ${v}`, description: `Added to ${name}.`, tone: "success" });
        setQuery("");
        setResults([]);
        setOpen(false);
        router.refresh();
      } else {
        toast({ title: "Couldn't assign", description: res.error, tone: "danger" });
      }
    });

  const remove = (principal: string) =>
    start(async () => {
      const res = await removeTenantMember(slug, principal);
      if (res.ok) {
        toast({ title: `Removed ${principal}`, tone: "success" });
        router.refresh();
      } else {
        toast({ title: "Couldn't remove", description: res.error, tone: "danger" });
      }
    });

  const suggestions = results.filter(
    (u) => !assigned.has((u.email || u.oid).toLowerCase()),
  );

  return (
    <section className={panel.panel} aria-label="Tenant members">
      <div className={panel.head}>
        <div className={panel.headText}>
          <h2 className={panel.title}>Members</h2>
          <p className={panel.sub}>
            Assign users to {name}. Search people who&apos;ve signed in, or enter an email / Entra
            object id directly — access binds to their identity on first sign-in.
          </p>
        </div>
      </div>

      <div className={styles.combo} ref={boxRef}>
        <div className={styles.field}>
          <Search size={15} strokeWidth={2} aria-hidden className={styles.fieldIcon} />
          <input
            className={styles.input}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setOpen(true);
            }}
            onFocus={() => setOpen(true)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && freeTextValid) assign(value);
              if (e.key === "Escape") setOpen(false);
            }}
            placeholder="Search name/email, or paste an email or object id"
            aria-label="Assign a user"
            autoComplete="off"
          />
          <Button variant="secondary" icon={UserPlus} loading={pending} disabled={!freeTextValid} onClick={() => assign(value)}>
            Assign
          </Button>
        </div>

        {open && (suggestions.length > 0 || (value && freeTextValid)) && (
          <ul className={styles.menu} role="listbox">
            {suggestions.map((u) => (
              <li key={u.oid}>
                <button type="button" className={styles.option} onClick={() => assign(u.email || u.oid)}>
                  <span className={styles.optName}>{u.name || u.email || u.oid}</span>
                  {u.email && u.email !== u.name ? <span className={styles.optMeta}>{u.email}</span> : null}
                </button>
              </li>
            ))}
            {value && freeTextValid && !suggestions.some((u) => (u.email || u.oid).toLowerCase() === value.toLowerCase()) ? (
              <li>
                <button type="button" className={styles.option} onClick={() => assign(value)}>
                  <span className={styles.optName}>Assign “{value}”</span>
                  <span className={styles.optMeta}>{value.includes("@") ? "email" : "object id"}</span>
                </button>
              </li>
            ) : null}
          </ul>
        )}
      </div>

      {members.length === 0 ? (
        <p className={panel.sub}>No users assigned yet.</p>
      ) : (
        <ul className={styles.list}>
          {members.map((m) => {
            const isEmail = m.principal.includes("@");
            return (
              <li key={m.principal} className={styles.row}>
                <span className={isEmail ? undefined : styles.mono}>
                  {m.principal}
                  {isEmail ? (m.oid ? "" : <span className={styles.tag}>pending sign-in</span>) : <span className={styles.tag}>object id</span>}
                </span>
                <Button variant="ghost" icon={X} loading={pending} onClick={() => remove(m.principal)} aria-label={`Remove ${m.principal}`}>
                  Remove
                </Button>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}
