import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import styles from "./empty-state.module.css";

export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
  compact = false,
}: {
  icon: LucideIcon;
  title: string;
  description: string;
  action?: ReactNode;
  compact?: boolean;
}) {
  return (
    <div className={styles.empty} data-compact={compact || undefined}>
      <span className={styles.iconWrap} aria-hidden>
        <span className={styles.iconGrid} />
        <Icon size={22} strokeWidth={1.8} className={styles.icon} />
      </span>
      <div className={styles.copy}>
        <h3 className={styles.title}>{title}</h3>
        <p className={styles.description}>{description}</p>
      </div>
      {action && <div className={styles.action}>{action}</div>}
    </div>
  );
}
