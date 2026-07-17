"use client";

import { Compass } from "lucide-react";
import { BrandGlyph } from "@/components/shell/brand-mark";
import { ButtonLink } from "@/components/ui/button";
import styles from "./not-found.module.css";

// Custom 404 — rendered by the root layout, so it stands alone (no shell) and
// echoes the sign-in surface: an engineered grid, a restrained accent glow, and
// one calm way back.
export default function NotFound() {
  return (
    <main className={styles.page}>
      <div className={styles.grid} aria-hidden />
      <div className={styles.glow} aria-hidden />
      <div className={styles.card}>
        <span className={styles.brand}>
          <BrandGlyph size={22} />
          Cortex
        </span>
        <span className={styles.code}>404</span>
        <h1 className={styles.title}>This page isn&rsquo;t on the map</h1>
        <p className={styles.body}>
          The page you&rsquo;re looking for doesn&rsquo;t exist, moved, or was never provisioned. Head back to
          the Fleet and pick up from there.
        </p>
        <div className={styles.actions}>
          <ButtonLink variant="primary" icon={Compass} href="/">
            Back to Fleet
          </ButtonLink>
        </div>
      </div>
    </main>
  );
}
