"use client";

import Link from "next/link";
import { useEffect, useRef } from "react";
import { X } from "lucide-react";
import { useConsole } from "@/components/providers/console-provider";
import { homeForRole } from "@/lib/nav";
import { BrandMark } from "./brand-mark";
import { RailNav } from "./side-rail";
import styles from "./mobile-nav.module.css";

export function MobileNav() {
  const { role, mobileNavOpen, setMobileNavOpen } = useConsole();
  const dialogRef = useRef<HTMLDialogElement | null>(null);

  useEffect(() => {
    const dlg = dialogRef.current;
    if (!dlg) return;
    if (mobileNavOpen && !dlg.open) dlg.showModal();
    else if (!mobileNavOpen && dlg.open) dlg.close();
  }, [mobileNavOpen]);

  const close = () => setMobileNavOpen(false);

  return (
    <dialog
      ref={dialogRef}
      className={styles.drawer}
      aria-label="Navigation"
      onClose={close}
      onCancel={close}
      onClick={(e) => {
        if (e.target === dialogRef.current) close();
      }}
    >
      <div className={styles.sheet}>
        <div className={styles.head}>
          <Link href={homeForRole(role)} onClick={close} aria-label="Cortex home">
            <BrandMark />
          </Link>
          <button
            type="button"
            className={styles.close}
            onClick={close}
            aria-label="Close navigation"
          >
            <X size={19} strokeWidth={2} />
          </button>
        </div>
        <nav className={styles.nav} aria-label="Primary navigation">
          <RailNav collapsed={false} onNavigate={close} />
        </nav>
      </div>
    </dialog>
  );
}
