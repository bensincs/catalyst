"use client";

import { useState } from "react";
import { ArrowUpRight, RefreshCw, ShieldAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/ui/page-header";
import { appIcon } from "@/lib/app-icons";
import type { PinnedApp } from "@/lib/types";
import styles from "./embedded-app.module.css";

/** Renders an installed application inside the console.
 *
 *  The frame is deliberately not invisible. What is on screen is the tenant's
 *  own software running in their cluster, on their domain, under their
 *  identity — not part of Cortex. A chrome-less iframe would imply the console
 *  vouches for what is inside it, and would leave no way to tell a page served
 *  by the app from a page served by us. The header names the app and shows
 *  where it actually lives.
 *
 *  Two properties this relies on rather than enforces:
 *
 *  - The app is on a different origin, so it cannot read the console's session.
 *    That is browser-enforced and holds regardless of what the app does.
 *  - The app must permit framing. Many do not, by default or by policy, and
 *    there is no way to detect that from here — a blocked frame looks exactly
 *    like a slow one. Rather than guess, the escape hatch is always present.
 */
export function EmbeddedApp({ app }: { app: PinnedApp }) {
  const [nonce, setNonce] = useState(0);
  const Icon = appIcon(app.icon);
  let host = app.url;
  try {
    host = new URL(app.url).host;
  } catch {
    /* keep the raw value — it is only shown, never followed */
  }

  return (
    <div className={styles.wrap}>
      <PageHeader
        title={app.name}
        description={`Running in your tenant at ${host}`}
        actions={
          <div className={styles.actions}>
            <Button
              size="sm"
              variant="ghost"
              icon={RefreshCw}
              onClick={() => setNonce((n) => n + 1)}
            >
              Reload
            </Button>
            <Button
              size="sm"
              variant="secondary"
              icon={ArrowUpRight}
              onClick={() => window.open(app.url, "_blank", "noopener,noreferrer")}
            >
              Open in a new tab
            </Button>
          </div>
        }
      />

      <div className={styles.frameWrap}>
        <div className={styles.frameBar}>
          <span className={styles.frameIcon} aria-hidden>
            <Icon size={14} strokeWidth={2} />
          </span>
          <span className={`${styles.frameHost} mono`}>{host}</span>
          <span className={styles.frameNote}>your tenant&rsquo;s application</span>
        </div>

        <iframe
          key={nonce}
          src={app.url}
          title={app.name}
          className={styles.frame}
          // allow-same-origin is required or the app cannot use its own cookies
          // or storage and most apps simply break. It does NOT grant access to
          // the console: the app is a different origin, so the browser isolates
          // it either way. Everything not needed to render an app is withheld.
          sandbox="allow-same-origin allow-scripts allow-forms allow-popups allow-downloads"
          referrerPolicy="no-referrer"
          loading="lazy"
        />
      </div>

      <p className={styles.footnote}>
        <ShieldAlert size={14} strokeWidth={2} aria-hidden />
        <span>
          This is your own application, not part of Cortex. If it doesn&rsquo;t appear, it may
          not allow being embedded — open it in a new tab instead.
        </span>
      </p>
    </div>
  );
}
