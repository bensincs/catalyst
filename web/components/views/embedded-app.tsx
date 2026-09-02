"use client";

import { useEffect, useRef, useState } from "react";
import { ArrowUpRight, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useConsole } from "@/components/providers/console-provider";
import { PageHeader } from "@/components/ui/page-header";
import type { PinnedApp } from "@/lib/types";
import styles from "./embedded-app.module.css";

/** Renders an installed application as a page of the console.
 *
 *  Presented first-party on purpose. This is not arbitrary third-party content:
 *  it comes from a catalog the platform publishes, the tenant chose to install
 *  it, and it runs on their own domain under their own identity. Wrapping it in
 *  a browser-chrome frame made the console look like it was hosting something it
 *  distrusted, which is the wrong signal for software we ship.
 *
 *  The isolation that matters is not visual and does not depend on any of this:
 *  the app is a different origin, so the browser will not let it reach the
 *  console's session whatever it is dressed as. The sandbox still withholds
 *  everything an application does not need to render.
 *
 *  One thing genuinely cannot be detected from here: an app that refuses to be
 *  framed produces a blank area that is indistinguishable from one still
 *  loading. Rather than pre-empt that with a permanent warning, the fallback
 *  appears only once it has taken long enough to be worth mentioning.
 */
export function EmbeddedApp({ app }: { app: PinnedApp }) {
  const { theme } = useConsole();
  const [nonce, setNonce] = useState(0);
  const [loaded, setLoaded] = useState(false);
  const [slow, setSlow] = useState(false);
  const frameRef = useRef<HTMLIFrameElement | null>(null);

  useEffect(() => {
    setLoaded(false);
    setSlow(false);
    const t = setTimeout(() => setSlow(true), 6000);
    return () => clearTimeout(t);
  }, [nonce]);

  // Tell the app it is embedded, and which theme to match. Sent as a message
  // rather than a query parameter so a theme toggle does not reload the frame
  // and throw away whatever the person was in the middle of. An app that does
  // not listen simply keeps its own appearance.
  useEffect(() => {
    const w = frameRef.current?.contentWindow;
    if (!w || !loaded) return;
    let origin = "*";
    try {
      origin = new URL(app.url).origin;
    } catch {
      return; // never broadcast to an origin we could not parse
    }
    w.postMessage({ source: "cortex", embedded: true, theme }, origin);
  }, [loaded, theme, app.url, nonce]);

  const open = () => window.open(app.url, "_blank", "noopener,noreferrer");

  return (
    <div className={styles.wrap}>
      <PageHeader
        title={app.name}
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
            <Button size="sm" variant="ghost" icon={ArrowUpRight} onClick={open}>
              Open in a new tab
            </Button>
          </div>
        }
      />

      <div className={styles.surface}>
        <iframe
          key={nonce}
          ref={frameRef}
          src={app.url}
          title={app.name}
          className={styles.frame}
          onLoad={() => setLoaded(true)}
          // allow-same-origin is required or the app cannot use its own cookies
          // or storage and most apps simply break. It grants nothing across
          // origins: the browser isolates it from the console either way.
          sandbox="allow-same-origin allow-scripts allow-forms allow-popups allow-downloads"
          referrerPolicy="no-referrer"
        />

        {!loaded && slow && (
          <div className={styles.fallback} role="status">
            <p className={styles.fallbackTitle}>This is taking a while</p>
            <p className={styles.fallbackBody}>
              Some applications don&rsquo;t allow being shown inside another page. If nothing
              appears, open it directly.
            </p>
            <Button size="sm" variant="secondary" icon={ArrowUpRight} onClick={open}>
              Open {app.name}
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}
