"use client";

import type { ReactNode } from "react";
import { SideRail } from "./side-rail";
import { TopBar } from "./top-bar";
import { MobileNav } from "./mobile-nav";
import { CommandPalette } from "./command-palette";
import styles from "./app-shell.module.css";

export function AppShell({ children }: { children: ReactNode }) {
  return (
    <>
      <a href="#main-content" className="skip-link">
        Skip to content
      </a>
      <div className={styles.shell}>
        <SideRail />
        <TopBar />
        <main id="main-content" className={styles.main} tabIndex={-1}>
          <div className={styles.content}>{children}</div>
        </main>
      </div>
      <MobileNav />
      <CommandPalette />
    </>
  );
}
