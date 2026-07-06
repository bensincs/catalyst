"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { PanelLeftClose, PanelLeftOpen } from "lucide-react";
import { useConsole } from "@/components/providers/console-provider";
import { navForRole, homeForRole } from "@/lib/nav";
import { BrandMark } from "./brand-mark";
import styles from "./side-rail.module.css";

export function RailNav({
  collapsed,
  onNavigate,
}: {
  collapsed: boolean;
  onNavigate?: () => void;
}) {
  const { role } = useConsole();
  const pathname = usePathname();
  const groups = navForRole(role);
  const main = groups.filter((g) => g.id !== "system");
  const system = groups.filter((g) => g.id === "system");

  const renderGroup = (
    group: (typeof groups)[number],
    key: string,
  ) => (
    <div className={styles.group} key={key}>
      {group.label && !collapsed && (
        <div className={styles.groupLabel}>{group.label}</div>
      )}
      <ul role="list" className={styles.list}>
        {group.items.map((item) => {
          const active =
            pathname === item.href || pathname.startsWith(item.href + "/");
          const Icon = item.icon;
          return (
            <li key={item.href}>
              <Link
                href={item.href}
                className={styles.link}
                data-active={active || undefined}
                aria-current={active ? "page" : undefined}
                aria-label={collapsed ? item.label : undefined}
                onClick={onNavigate}
              >
                <span className={styles.linkIcon} aria-hidden>
                  <Icon size={18} strokeWidth={2} />
                </span>
                <span className={styles.linkLabel}>{item.label}</span>
                {collapsed && <span className={styles.tooltip}>{item.label}</span>}
              </Link>
            </li>
          );
        })}
      </ul>
    </div>
  );

  return (
    <>
      <div className={styles.mainNav}>
        {main.map((g) => renderGroup(g, g.id))}
      </div>
      <div className={styles.systemNav}>
        {system.map((g) => renderGroup(g, g.id))}
      </div>
    </>
  );
}

export function SideRail() {
  const { railCollapsed, toggleRail, role } = useConsole();

  return (
    <aside className={styles.rail} aria-label="Primary">
      <div className={styles.head}>
        <Link
          href={homeForRole(role)}
          className={styles.brandLink}
          aria-label="Cortex home"
        >
          <BrandMark collapsed={railCollapsed} />
        </Link>
      </div>

      <nav className={styles.nav} aria-label="Primary navigation">
        <RailNav collapsed={railCollapsed} />
      </nav>

      <div className={styles.foot}>
        <button
          type="button"
          className={styles.collapseBtn}
          onClick={toggleRail}
          aria-label={railCollapsed ? "Expand sidebar" : "Collapse sidebar"}
          aria-pressed={railCollapsed}
        >
          <span className={styles.collapseIcon} aria-hidden>
            {railCollapsed ? (
              <PanelLeftOpen size={18} strokeWidth={2} />
            ) : (
              <PanelLeftClose size={18} strokeWidth={2} />
            )}
          </span>
          <span className={styles.collapseLabel}>Collapse</span>
        </button>
      </div>
    </aside>
  );
}
