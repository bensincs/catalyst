"use client";

import { useRouter } from "next/navigation";
import {
  Building2,
  Check,
  ChevronsUpDown,
  Globe,
  Radar,
  ShieldCheck,
} from "lucide-react";
import { useConsole } from "@/components/providers/console-provider";
import { Menu, MenuItem, MenuLabel, MenuSeparator } from "@/components/ui/menu";
import { StatusDot } from "@/components/ui/status";
import { LIFECYCLE_META } from "@/lib/types";
import styles from "./tenant-switcher.module.css";

export function TenantSwitcher() {
  const { role, tenants, activeTenant } = useConsole();
  const router = useRouter();

  if (role === "tenant") {
    if (!activeTenant) return null;
    const t = activeTenant;
    return (
      <Menu
        ariaLabel="Tenant identity"
        align="start"
        width={320}
        button={(props) => (
          <button {...props} type="button" className={styles.trigger}>
            <span className={styles.badge} data-sovereign aria-hidden>
              <ShieldCheck size={16} strokeWidth={2.2} />
            </span>
            <span className={styles.text}>
              <span className={styles.primary}>{t.name}</span>
              <span className={styles.secondary}>
                <span className={styles.sovLabel}>{t.plan}</span>
                <span className={styles.sep} aria-hidden>
                  ·
                </span>
                {t.region}
              </span>
            </span>
            <ChevronsUpDown size={15} strokeWidth={2} className={styles.chevron} aria-hidden />
          </button>
        )}
      >
        {() => (
          <div className={styles.identity}>
            <MenuLabel>Operating context</MenuLabel>
            <p className={styles.identityLede}>
              Agents run in <strong>{t.name}&rsquo;s</strong> own Azure tenant, under its
              own identity. Cortex never holds your data.
            </p>
            <dl className={styles.facts}>
              <Fact label="Tenant ID" value={t.tenantId} mono />
              <Fact label="Subscription" value={t.subscriptionId || "—"} mono />
              <Fact label="Region" value={t.region} />
              <Fact label="Reconciler identity" value={t.reconcilerIdentity || "—"} mono />
              <Fact label="Foundry project" value={t.foundryProject || "—"} mono />
            </dl>
          </div>
        )}
      </Menu>
    );
  }

  // Platform: launch into any tenant.
  return (
    <Menu
      ariaLabel="Fleet"
      align="start"
      width={288}
      button={(props) => (
        <button {...props} type="button" className={styles.trigger}>
          <span className={styles.badge} aria-hidden>
            <Globe size={16} strokeWidth={2.2} />
          </span>
          <span className={styles.text}>
            <span className={styles.primary}>All tenants</span>
            <span className={styles.secondary}>{tenants.length} tenants · fleet</span>
          </span>
          <ChevronsUpDown size={15} strokeWidth={2} className={styles.chevron} aria-hidden />
        </button>
      )}
    >
      {({ close }) => (
        <>
          <MenuItem
            icon={<Globe size={16} strokeWidth={2} />}
            trailing={<Check size={14} strokeWidth={2.6} />}
            onClick={() => {
              router.push("/");
              close();
            }}
          >
            All tenants
          </MenuItem>
          {tenants.length > 0 && (
            <>
              <MenuSeparator />
              <MenuLabel>Open a tenant</MenuLabel>
              {tenants.slice(0, 7).map((t) => (
                <MenuItem
                  key={t.id}
                  icon={<StatusDot tone={LIFECYCLE_META[t.lifecycle].tone} />}
                  onClick={() => {
                    router.push(`/tenants/${t.id}`);
                    close();
                  }}
                >
                  {t.name}
                </MenuItem>
              ))}
            </>
          )}
          <MenuSeparator />
          <MenuItem
            icon={<Radar size={16} strokeWidth={2} />}
            onClick={() => {
              router.push("/");
              close();
            }}
          >
            View all in Fleet
          </MenuItem>
        </>
      )}
    </Menu>
  );
}

function Fact({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className={styles.fact}>
      <dt>{label}</dt>
      <dd className={mono ? "mono" : undefined}>{value}</dd>
    </div>
  );
}
