"use client";

import { forwardRef, type ButtonHTMLAttributes, type ComponentProps } from "react";
import Link from "next/link";
import { Loader2, type LucideIcon } from "lucide-react";
import { cn } from "@/lib/cn";
import styles from "./button.module.css";

type Variant = "primary" | "brand" | "secondary" | "ghost" | "danger" | "danger-ghost";
type Size = "sm" | "md";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  icon?: LucideIcon;
  iconRight?: LucideIcon;
  loading?: boolean;
  iconOnly?: boolean;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  function Button(
    {
      variant = "secondary",
      size = "md",
      icon: Icon,
      iconRight: IconRight,
      loading = false,
      iconOnly = false,
      className,
      children,
      disabled,
      type = "button",
      ...rest
    },
    ref,
  ) {
    const iconSize = size === "sm" ? 15 : 16;
    return (
      <button
        ref={ref}
        type={type}
        className={cn(styles.button, className)}
        data-variant={variant}
        data-size={size}
        data-icon-only={iconOnly || undefined}
        data-loading={loading || undefined}
        disabled={disabled || loading}
        {...rest}
      >
        {loading && (
          <span className={styles.spinner} aria-hidden>
            <Loader2 size={iconSize} strokeWidth={2.4} />
          </span>
        )}
        {!loading && Icon && (
          <Icon size={iconSize} strokeWidth={2.2} aria-hidden />
        )}
        {!iconOnly && children != null && (
          <span className={styles.label}>{children}</span>
        )}
        {!loading && IconRight && (
          <IconRight size={iconSize} strokeWidth={2.2} aria-hidden />
        )}
      </button>
    );
  },
);

/** A navigation control that looks exactly like a Button but renders a real
 *  <a> (next/link) — so it middle-clicks, opens in a new tab, and prefetches. */
export function ButtonLink({
  variant = "secondary",
  size = "md",
  icon: Icon,
  iconRight: IconRight,
  iconOnly = false,
  className,
  children,
  ...rest
}: {
  variant?: Variant;
  size?: Size;
  icon?: LucideIcon;
  iconRight?: LucideIcon;
  iconOnly?: boolean;
} & ComponentProps<typeof Link>) {
  const iconSize = size === "sm" ? 15 : 16;
  return (
    <Link
      className={cn(styles.button, className)}
      data-variant={variant}
      data-size={size}
      data-icon-only={iconOnly || undefined}
      {...rest}
    >
      {Icon && <Icon size={iconSize} strokeWidth={2.2} aria-hidden />}
      {!iconOnly && children != null && <span className={styles.label}>{children}</span>}
      {IconRight && <IconRight size={iconSize} strokeWidth={2.2} aria-hidden />}
    </Link>
  );
}
