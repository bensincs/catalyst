"use client";

import { useTransition } from "react";
import { Building2, ChevronsUpDown } from "lucide-react";
import { useConsole } from "@/components/providers/console-provider";
import { Menu, MenuItem, MenuLabel } from "@/components/ui/menu";
import { setActiveTenantSlug } from "@/lib/tenant-actions";
import styles from "./tenant-switcher.module.css";

/** Switches the active Cortex tenant for a caller assigned to several. Sends the
 *  selection as X-Cortex-Tenant on every API call. Only enabled tenants are
 *  offered; hidden when there's nothing to switch between. */
export function TenantSwitcher() {
  const { cortexTenants, activeTenantSlug, activeTenant } = useConsole();
  const [pending, start] = useTransition();

  const options = (cortexTenants ?? []).filter((t) => t.enabled);
  if (options.length < 2) return null;

  const currentId =
    (activeTenantSlug && options.some((t) => t.id === activeTenantSlug) && activeTenantSlug) ||
    (activeTenant && options.some((t) => t.id === activeTenant.id) && activeTenant.id) ||
    options[0]?.id ||
    "";
  const current = options.find((t) => t.id === currentId) ?? options[0];

  const select = (slug: string, close: () => void) => {
    close();
    if (slug !== currentId) start(async () => { await setActiveTenantSlug(slug); });
  };

  return (
    <Menu
      ariaLabel="Switch tenant"
      align="start"
      width={260}
      button={(props) => (
        <button {...props} type="button" className={styles.trigger} data-pending={pending || undefined}>
          <Building2 size={15} strokeWidth={2} aria-hidden className={styles.leadIcon} />
          <span className={styles.name}>{current?.name}</span>
          <ChevronsUpDown size={14} strokeWidth={2} aria-hidden className={styles.chevron} />
        </button>
      )}
    >
      {({ close }) => (
        <>
          <MenuLabel>Tenants</MenuLabel>
          {options.map((t) => (
            <MenuItem
              key={t.id}
              role="menuitemradio"
              selected={t.id === currentId}
              icon={<Building2 size={15} strokeWidth={2} />}
              onClick={() => select(t.id, close)}
            >
              {t.name}
            </MenuItem>
          ))}
        </>
      )}
    </Menu>
  );
}
