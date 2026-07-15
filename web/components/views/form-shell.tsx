"use client";

import { type ReactNode } from "react";
import Link from "next/link";
import { ArrowLeft, type LucideIcon } from "lucide-react";
import styles from "./form-shell.module.css";

/** The full-page create/edit scaffold: a back link + titled header, a body that
 *  fills the width, and a sticky action bar. Shared by the agent + memory-store
 *  forms so they match the deployment form. */
export function FormShell({
  backHref,
  backLabel,
  icon: Icon,
  title,
  subtitle,
  footer,
  children,
}: {
  backHref: string;
  backLabel: string;
  icon: LucideIcon;
  title: string;
  subtitle?: string;
  footer: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className={styles.page}>
      <div className={styles.head}>
        <Link href={backHref} className={styles.back}>
          <ArrowLeft size={15} strokeWidth={2.4} /> {backLabel}
        </Link>
        <div className={styles.titleRow}>
          <span className={styles.titleIcon} aria-hidden>
            <Icon size={20} strokeWidth={2} />
          </span>
          <div>
            <h1 className={styles.title}>{title}</h1>
            {subtitle && <p className={styles.subtitle}>{subtitle}</p>}
          </div>
        </div>
      </div>

      <div className={styles.body}>{children}</div>

      <div className={styles.footer}>{footer}</div>
    </div>
  );
}

export function FormSection({
  icon: Icon,
  title,
  desc,
  status,
  accent,
  children,
}: {
  icon: LucideIcon;
  title: string;
  desc?: string;
  status?: ReactNode;
  accent?: boolean;
  children: ReactNode;
}) {
  return (
    <section className={styles.section} data-accent={accent || undefined}>
      <div className={styles.sectionHead}>
        <span className={styles.sectionIcon} aria-hidden>
          <Icon size={16} strokeWidth={2.1} />
        </span>
        <div className={styles.sectionMeta}>
          <h2 className={styles.sectionTitle}>{title}</h2>
          {desc && <p className={styles.sectionDesc}>{desc}</p>}
        </div>
        {status && <div className={styles.sectionStatus}>{status}</div>}
      </div>
      <div className={styles.sectionBody}>{children}</div>
    </section>
  );
}
