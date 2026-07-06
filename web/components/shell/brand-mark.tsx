import { cn } from "@/lib/cn";
import styles from "./brand-mark.module.css";

/**
 * Cortex mark — a hub reconciling satellite nodes. The lime core is the control
 * plane; the ring nodes are tenants converging to desired state.
 */
export function BrandGlyph({ size = 22, className }: { size?: number; className?: string }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      className={cn(styles.glyph, className)}
      aria-hidden
    >
      <rect
        x="1.25"
        y="1.25"
        width="21.5"
        height="21.5"
        rx="6"
        className={styles.frame}
        strokeWidth="1.5"
      />
      <circle cx="12" cy="5.4" r="1.5" className={styles.node} />
      <circle cx="18.6" cy="12" r="1.5" className={styles.node} />
      <circle cx="12" cy="18.6" r="1.5" className={styles.node} />
      <circle cx="5.4" cy="12" r="1.5" className={styles.node} />
      <path
        d="M12 5.4 12 12 18.6 12M12 12 12 18.6M12 12 5.4 12"
        className={styles.spokes}
        strokeWidth="1.4"
      />
      <circle cx="12" cy="12" r="3.1" className={styles.core} />
    </svg>
  );
}

export function BrandMark({
  collapsed = false,
  className,
}: {
  collapsed?: boolean;
  className?: string;
}) {
  return (
    <span className={cn(styles.mark, className)} data-collapsed={collapsed || undefined}>
      <BrandGlyph />
      {!collapsed && (
        <span className={styles.wordmark}>
          Cortex
          <span className={styles.by}>by Inception</span>
        </span>
      )}
    </span>
  );
}
