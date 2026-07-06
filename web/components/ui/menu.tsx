"use client";

import {
  useCallback,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import styles from "./menu.module.css";

interface TriggerProps {
  ref: (el: HTMLButtonElement | null) => void;
  onClick: () => void;
  "aria-haspopup": "menu";
  "aria-expanded": boolean;
  "aria-controls": string;
  id: string;
  "data-open"?: boolean;
}

interface MenuProps {
  button: (props: TriggerProps) => ReactNode;
  children: (helpers: { close: () => void }) => ReactNode;
  align?: "start" | "end";
  width?: number;
  ariaLabel: string;
}

export function Menu({
  button,
  children,
  align = "start",
  width,
  ariaLabel,
}: MenuProps) {
  const [open, setOpen] = useState(false);
  const [coords, setCoords] = useState<{ top: number; left: number; minWidth: number } | null>(
    null,
  );
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const rawId = useId();
  const menuId = `menu-${rawId.replace(/[:]/g, "")}`;
  const triggerId = `${menuId}-trigger`;

  const place = useCallback(() => {
    const t = triggerRef.current;
    if (!t) return;
    const r = t.getBoundingClientRect();
    const w = width ?? Math.max(r.width, 200);
    const gap = 6;
    let left = align === "end" ? r.right - w : r.left;
    left = Math.min(Math.max(8, left), window.innerWidth - w - 8);
    let top = r.bottom + gap;
    // flip up if not enough room below
    const estH = panelRef.current?.offsetHeight ?? 280;
    if (top + estH > window.innerHeight - 8 && r.top - estH - gap > 8) {
      top = r.top - estH - gap;
    }
    setCoords({ top, left, minWidth: r.width });
  }, [align, width]);

  const close = useCallback(() => {
    setOpen(false);
    triggerRef.current?.focus();
  }, []);

  useLayoutEffect(() => {
    if (open) place();
  }, [open, place]);

  useEffect(() => {
    if (!open) return;
    const onScroll = () => place();
    const onResize = () => place();
    window.addEventListener("scroll", onScroll, true);
    window.addEventListener("resize", onResize);
    return () => {
      window.removeEventListener("scroll", onScroll, true);
      window.removeEventListener("resize", onResize);
    };
  }, [open, place]);

  useEffect(() => {
    if (!open) return;
    // focus first item
    const id = requestAnimationFrame(() => {
      const first = panelRef.current?.querySelector<HTMLElement>("[data-menu-item]");
      first?.focus();
    });

    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        close();
        return;
      }
      if (e.key === "Tab") {
        close();
        return;
      }
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const items = Array.from(
          panelRef.current?.querySelectorAll<HTMLElement>("[data-menu-item]:not([disabled])") ??
            [],
        );
        if (!items.length) return;
        const idx = items.indexOf(document.activeElement as HTMLElement);
        const next =
          e.key === "ArrowDown"
            ? items[(idx + 1) % items.length]
            : items[(idx - 1 + items.length) % items.length];
        next?.focus();
      }
    };

    const onPointer = (e: PointerEvent) => {
      const target = e.target as Node;
      if (panelRef.current?.contains(target) || triggerRef.current?.contains(target)) return;
      setOpen(false);
    };

    document.addEventListener("keydown", onKey);
    document.addEventListener("pointerdown", onPointer);
    return () => {
      cancelAnimationFrame(id);
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("pointerdown", onPointer);
    };
  }, [open, close]);

  const triggerProps: TriggerProps = {
    ref: (el) => {
      triggerRef.current = el;
    },
    onClick: () => setOpen((o) => !o),
    "aria-haspopup": "menu",
    "aria-expanded": open,
    "aria-controls": menuId,
    id: triggerId,
    "data-open": open || undefined,
  };

  return (
    <>
      {button(triggerProps)}
      {open &&
        typeof document !== "undefined" &&
        createPortal(
          <div
            ref={panelRef}
            id={menuId}
            role="menu"
            aria-label={ariaLabel}
            aria-labelledby={triggerId}
            className={styles.panel}
            style={{
              top: coords?.top ?? -9999,
              left: coords?.left ?? -9999,
              width: width ? `${width}px` : undefined,
              minWidth: width ? undefined : `${coords?.minWidth ?? 200}px`,
            }}
          >
            {children({ close })}
          </div>,
          document.body,
        )}
    </>
  );
}

export function MenuItem({
  children,
  onClick,
  icon,
  selected = false,
  danger = false,
  disabled = false,
  trailing,
  role = "menuitem",
}: {
  children: ReactNode;
  onClick?: () => void;
  icon?: ReactNode;
  selected?: boolean;
  danger?: boolean;
  disabled?: boolean;
  trailing?: ReactNode;
  role?: "menuitem" | "menuitemradio";
}) {
  return (
    <button
      type="button"
      role={role}
      data-menu-item
      data-danger={danger || undefined}
      aria-checked={role === "menuitemradio" ? selected : undefined}
      disabled={disabled}
      className={styles.item}
      onClick={onClick}
    >
      {icon && (
        <span className={styles.itemIcon} aria-hidden>
          {icon}
        </span>
      )}
      <span className={styles.itemLabel}>{children}</span>
      {trailing != null ? (
        <span className={styles.itemTrailing}>{trailing}</span>
      ) : selected ? (
        <span className={styles.check} aria-hidden />
      ) : null}
    </button>
  );
}

export function MenuSeparator() {
  return <div className={styles.separator} role="separator" />;
}

export function MenuLabel({ children }: { children: ReactNode }) {
  return <div className={styles.menuLabel}>{children}</div>;
}
