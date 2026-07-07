import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { EmptyState } from "./empty-state";
import styles from "./error-state.module.css";

/**
 * Error surface, built on EmptyState so failures read in the same visual
 * language as empties. `page` fills the viewport (no shell — e.g. the control
 * plane is unreachable); `panel` sits in the content area with the shell intact.
 */
export function ErrorState({
  variant = "panel",
  icon,
  title,
  description,
  action,
}: {
  variant?: "page" | "panel";
  icon: LucideIcon;
  title: string;
  description: string;
  action?: ReactNode;
}) {
  return (
    <div className={styles.wrap} data-variant={variant} role="alert">
      <EmptyState icon={icon} title={title} description={description} action={action} />
    </div>
  );
}
