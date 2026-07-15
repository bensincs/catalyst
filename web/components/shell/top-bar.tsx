"use client";

import { Menu as MenuIcon } from "lucide-react";
import { useConsole } from "@/components/providers/console-provider";
import { EnvBadge } from "./env-badge";
import { ThemeToggle } from "./theme-toggle";
import { CommandTrigger } from "./command-trigger";
import styles from "./top-bar.module.css";

export function TopBar() {
  const { setMobileNavOpen } = useConsole();

  return (
    <header className={styles.topbar}>
      <div className={styles.left}>
        <button
          type="button"
          className={styles.hamburger}
          onClick={() => setMobileNavOpen(true)}
          aria-label="Open navigation"
        >
          <MenuIcon size={19} strokeWidth={2} />
        </button>
      </div>

      <div className={styles.right}>
        <CommandTrigger />
        <div className={styles.divider} aria-hidden />
        <EnvBadge />
        <ThemeToggle />
      </div>
    </header>
  );
}
