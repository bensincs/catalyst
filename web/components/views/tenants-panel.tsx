"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Building2, Check, Plus, RefreshCw, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useConsole } from "@/components/providers/console-provider";
import { switchTenant, discoverTenants } from "@/lib/tenant-actions";
import type { DiscoveryResult } from "@/lib/tenant-discovery";
import styles from "./tenants-panel.module.css";

const GUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

const NOTICES: Record<string, string> = {
  badid: "That doesn't look like a directory ID.",
  state: "That connection request expired or didn't match — please try again.",
  exchange: "Microsoft Entra couldn't complete the connection. Please try again.",
  mismatch: "That sign-in returned a different directory than the one requested.",
  denied: "The connection was cancelled.",
};

const DISCOVERY_HINTS: Record<string, string> = {
  "no-refresh-token": "Sign in again to refresh your session, then retry.",
  consent: "Automatic discovery needs the “Azure Service Management → user_impersonation” permission granted (admin consent) on the app registration. Until then, connect a directory by its ID below.",
  "arm-token": "Automatic discovery needs the “Azure Service Management” permission granted on the app registration. You can still connect a directory by its ID below.",
};

/** Start the tenant-scoped OAuth connect for a directory (full-page redirect). */
function beginConnect(tid: string, name = "") {
  const q = name ? `?name=${encodeURIComponent(name)}` : "";
  window.location.assign(`/api/tenants/${tid}/connect${q}`);
}

export function TenantsPanel({ notice }: { notice?: string }) {
  const { myTenants, activeTid, activeTenant } = useConsole();
  const router = useRouter();
  const [switching, startSwitch] = useTransition();
  const [discovering, startDiscover] = useTransition();
  const [result, setResult] = useState<DiscoveryResult | null>(null);
  const [manual, setManual] = useState("");

  const connectedTids = new Set(myTenants.map((t) => t.tid));
  const manualValid = GUID.test(manual.trim().toLowerCase());

  const nameFor = (tid: string, fallback: string) =>
    (tid === activeTid ? activeTenant?.name : undefined) || fallback || `${tid.slice(0, 8)}…`;

  const use = (tid: string) =>
    startSwitch(async () => {
      await switchTenant(tid);
      router.refresh();
    });

  const runDiscovery = () =>
    startDiscover(async () => {
      setResult(await discoverTenants());
    });

  const newlyFound = (result?.tenants ?? []).filter((t) => !connectedTids.has(t.tid));

  return (
    <div className={styles.wrap}>
      {notice && NOTICES[notice] && (
        <p className={styles.notice} role="alert">
          {NOTICES[notice]}
        </p>
      )}

      {/* Connected directories */}
      <ul className={styles.list} role="list">
        {myTenants.map((t) => {
          const active = t.tid === activeTid;
          return (
            <li key={t.tid} className={styles.row}>
              <span className={styles.rowIcon} aria-hidden>
                <Building2 size={16} strokeWidth={2} />
              </span>
              <span className={styles.rowText}>
                <span className={styles.rowName}>
                  {nameFor(t.tid, t.name)}
                  {t.needsReauth && <span className={styles.warn}> · needs reconnect</span>}
                </span>
                <span className={styles.rowMeta}>{t.tid}</span>
              </span>
              <span className={styles.rowAction}>
                {t.needsReauth ? (
                  <Button size="sm" variant="secondary" icon={RefreshCw} onClick={() => beginConnect(t.tid, t.name)}>
                    Reconnect
                  </Button>
                ) : active ? (
                  <span className={styles.activePill}>
                    <Check size={13} strokeWidth={2.8} aria-hidden />
                    Active
                  </span>
                ) : (
                  <Button size="sm" variant="secondary" onClick={() => use(t.tid)} loading={switching}>
                    Use
                  </Button>
                )}
              </span>
            </li>
          );
        })}
      </ul>

      {/* Connect another */}
      <div className={styles.connect}>
        <div className={styles.connectHead}>
          <span className={styles.connectTitle}>Connect another directory</span>
          <Button size="sm" variant="secondary" icon={Search} onClick={runDiscovery} loading={discovering}>
            Find directories
          </Button>
        </div>

        {result && (
          <>
            {newlyFound.length > 0 && (
              <ul className={styles.list} role="list">
                {newlyFound.map((t) => (
                  <li key={t.tid} className={styles.row}>
                    <span className={styles.rowIcon} aria-hidden>
                      <Building2 size={16} strokeWidth={2} />
                    </span>
                    <span className={styles.rowText}>
                      <span className={styles.rowName}>{t.displayName || t.defaultDomain || `${t.tid.slice(0, 8)}…`}</span>
                      <span className={styles.rowMeta}>{t.defaultDomain || t.tid}</span>
                    </span>
                    <span className={styles.rowAction}>
                      <Button size="sm" variant="secondary" icon={Plus} onClick={() => beginConnect(t.tid, t.displayName)}>
                        Connect
                      </Button>
                    </span>
                  </li>
                ))}
              </ul>
            )}
            {newlyFound.length === 0 && !result.error && (
              <p className={styles.hint}>No other directories found for your account.</p>
            )}
            {result.error && (
              <p className={styles.hint}>{DISCOVERY_HINTS[result.error] ?? "Couldn't reach Azure to list directories right now."}</p>
            )}
          </>
        )}

        <div className={styles.manual}>
          <input
            className={styles.input}
            value={manual}
            onChange={(e) => setManual(e.target.value)}
            placeholder="Or connect by directory (tenant) ID"
            spellCheck={false}
            autoComplete="off"
            aria-label="Directory (tenant) ID"
          />
          <Button
            variant="primary"
            disabled={!manualValid}
            onClick={() => beginConnect(manual.trim().toLowerCase())}
          >
            Connect
          </Button>
        </div>
        <p className={styles.hint}>
          A directory ID is the Entra tenant GUID you’re a guest in. Connecting redirects you to Microsoft to
          sign in for that directory.
        </p>
      </div>
    </div>
  );
}
