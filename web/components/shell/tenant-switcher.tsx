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

  // Only enabled tenants are selectable — a disabled tenant is cut off, so it
  // shouldn't appear as an option to operate.
  const options = (cortexTenants ?? []).filter((t) => t.enabled);
  if (options.length < 2) return null;

  const current =
    (activeTenantSlug && options.some((t) => t.id === activeTenantSlug) ? activeTenantSlug : "") ||
    (activeTenant && options.some((t) => t.id === activeTenant.id) ? activeTenant.id : "") ||
    options[0]?.id ||
    "";

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
        {options.map((t) => (
          <option key={t.id} value={t.id}>
            {t.name}
          </option>
        ))}
      </select>
    </label>
  );
}
