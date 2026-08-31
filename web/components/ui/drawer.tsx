"use client";

import { useEffect, useRef, type ReactNode } from "react";
import { X } from "lucide-react";
import styles from "./drawer.module.css";

/** A side panel for secondary, occasional work — the same native <dialog>
 *  mechanics as Modal (focus trap, Esc, inert background) but anchored to the
 *  right edge and full height, so a long list has somewhere to go without
 *  taking over the page. */
export function Drawer({
  open,
  onClose,
  title,
  description,
  children,
  width = 520,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  description?: string;
  children: ReactNode;
  width?: number;
}) {
  const ref = useRef<HTMLDialogElement | null>(null);

  useEffect(() => {
    const dlg = ref.current;
    if (!dlg) return;
    if (open && !dlg.open) dlg.showModal();
    else if (!open && dlg.open) dlg.close();
  }, [open]);

  return (
    <dialog
      ref={ref}
      className={styles.dialog}
      style={{ width: `min(${width}px, calc(100vw - 2rem))` }}
      onClose={onClose}
      onCancel={onClose}
      onClick={(e) => {
        // Backdrop clicks land on the dialog itself, not the panel.
        if (e.target === ref.current) onClose();
      }}
      aria-label={title}
    >
      <div className={styles.panel}>
        <header className={styles.head}>
          <div className={styles.headText}>
            <h2 className={styles.title}>{title}</h2>
            {description && <p className={styles.description}>{description}</p>}
          </div>
          <button type="button" className={styles.close} onClick={onClose} aria-label="Close">
            <X size={18} strokeWidth={2.2} />
          </button>
        </header>
        <div className={styles.body}>{children}</div>
      </div>
    </dialog>
  );
}
