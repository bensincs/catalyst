"use client";

import { useConsole } from "@/components/providers/console-provider";
import { ENV_META } from "@/lib/types";
import styles from "./env-badge.module.css";

export function EnvBadge() {
  const { env } = useConsole();
  // Never let a misconfigured NEXT_PUBLIC_CORTEX_ENV crash the whole console —
  // fall back to a neutral badge showing the raw value.
  const meta =
    ENV_META[env] ?? {
      label: env,
      short: (env || "?").toUpperCase().slice(0, 4),
      tone: "neutral" as const,
    };
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
