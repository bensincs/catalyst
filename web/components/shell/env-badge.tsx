"use client";

import { useConsole } from "@/components/providers/console-provider";
import { ENV_META } from "@/lib/types";
import styles from "./env-badge.module.css";

export function EnvBadge() {
  const { env } = useConsole();
  const meta = ENV_META[env];
  return (
    <span
      className={styles.badge}
      data-tone={meta.tone}
      title={`Environment: ${meta.label}`}
    >
      <span className={styles.dot} aria-hidden />
      <span className={styles.short}>{meta.short}</span>
    </span>
  );
}
