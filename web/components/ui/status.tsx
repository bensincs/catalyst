import type { LucideIcon } from "lucide-react";
import { cn } from "@/lib/cn";
import type { HealthMeta } from "@/lib/types";
import styles from "./status.module.css";

type Tone = HealthMeta["tone"];

export function StatusDot({
  tone,
  pulse = false,
  className,
}: {
  tone: Tone;
  pulse?: boolean;
  className?: string;
}) {
  return (
    <span
      className={cn(styles.dot, className)}
      data-tone={tone}
      data-pulse={pulse || undefined}
      aria-hidden
    />
  );
}

export function StatusBadge({
  tone,
  label,
  icon: Icon,
  pulse = false,
  variant = "soft",
  className,
}: {
  tone: Tone;
  label: string;
  icon?: LucideIcon;
  pulse?: boolean;
  /** soft = tinted pill, plain = dot + text on transparent */
  variant?: "soft" | "plain";
  className?: string;
}) {
  return (
    <span className={cn(styles.badge, className)} data-tone={tone} data-variant={variant}>
      {Icon ? (
        <Icon size={12} strokeWidth={2.4} aria-hidden className={styles.badgeIcon} />
      ) : (
        <StatusDot tone={tone} pulse={pulse} />
      )}
      <span>{label}</span>
    </span>
  );
}
