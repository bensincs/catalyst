"use client";

import { useTransition } from "react";
import { useConsole } from "@/components/providers/console-provider";
import { setActiveTenantSlug } from "@/lib/tenant-actions";
import styles from "./top-bar.module.css";

/** Switches the active Cortex tenant for a caller assigned to several (e.g. a
 *  platform-directory member of multiple platform-hosted tenants). Sends the
 *  selection as X-Cortex-Tenant on every API call. Hidden when there's nothing to
 *  switch between. */
export function TenantSwitcher() {
  const { cortexTenants, activeTenantSlug, activeTenant } = useConsole();
  const [pending, start] = useTransition();

  if (!cortexTenants || cortexTenants.length < 2) return null;

  const current = activeTenantSlug || activeTenant?.id || cortexTenants[0]?.id || "";

  return (
    <label className={styles.tenantSwitch}>
      <select
        aria-label="Active tenant"
        value={current}
        disabled={pending}
        onChange={(e) => {
          const slug = e.target.value;
          start(async () => {
            await setActiveTenantSlug(slug);
          });
        }}
      >
        {cortexTenants.map((t) => (
          <option key={t.id} value={t.id}>
            {t.name}
          </option>
        ))}
      </select>
    </label>
  );
}
