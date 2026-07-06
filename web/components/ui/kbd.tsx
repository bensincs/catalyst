import { cn } from "@/lib/cn";
import styles from "./kbd.module.css";

export function Kbd({
  keys,
  className,
}: {
  keys: string[];
  className?: string;
}) {
  return (
    <span className={cn(styles.kbd, className)} aria-hidden>
      {keys.map((k, i) => (
        <kbd key={i} className={styles.key}>
          {k}
        </kbd>
      ))}
    </span>
  );
}
