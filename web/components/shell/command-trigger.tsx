"use client";

import { Search } from "lucide-react";
import { useConsole } from "@/components/providers/console-provider";
import { Kbd } from "@/components/ui/kbd";
import styles from "./command-trigger.module.css";

export function CommandTrigger() {
  const { setPaletteOpen } = useConsole();
  return (
    <button
      type="button"
      className={styles.trigger}
      onClick={() => setPaletteOpen(true)}
      aria-label="Open command palette"
    >
      <Search size={15} strokeWidth={2} className={styles.icon} aria-hidden />
      <span className={styles.label}>Search or jump to…</span>
      <Kbd keys={["⌘", "K"]} className={styles.kbd} />
    </button>
  );
}
