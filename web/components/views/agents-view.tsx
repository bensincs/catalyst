"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { AppWindow, ArrowUpRight, Bot, ChevronRight, Globe, MessageSquare, RefreshCw } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button } from "@/components/ui/button";
import { StatusBadge, StatusDot } from "@/components/ui/status";
import { EmptyState } from "@/components/ui/empty-state";
import { HEALTH_META, type EnabledAgent, type PublishTarget } from "@/lib/types";
import styles from "./agents-view.module.css";

const PUBLISH: Record<PublishTarget, { label: string; icon: typeof Globe }> = {
  api: { label: "API", icon: Globe },
  teams: { label: "Teams", icon: MessageSquare },
  m365: { label: "M365", icon: AppWindow },
};

export function AgentsView({ agents }: { agents: EnabledAgent[] }) {
  const router = useRouter();
  const drifting = agents.filter((a) => a.drift).length;

  return (
    <div>
      <PageHeader
        title="Agents"
        description="The agents enabled in your tenant — desired vs. actual, health, and where they publish. Reconciled into your own Foundry project."
        meta={
          drifting > 0 ? (
            <span className={styles.driftMeta}>
              <RefreshCw size={12} strokeWidth={2.4} aria-hidden />
              {drifting} converging
            </span>
          ) : undefined
        }
        actions={
          <Button variant="primary" icon={Bot} onClick={() => router.push("/catalog")}>
            Browse catalog
          </Button>
        }
      />

      {agents.length === 0 ? (
        <div className={styles.panelEmpty}>
          <EmptyState
            icon={Bot}
            title="No agents enabled yet"
            description="Browse the agents your platform team entitled you to and enable one — the reconciler brings it live in your own Foundry project and keeps it converged to the latest version."
            action={
              <Button variant="primary" onClick={() => router.push("/catalog")}>
                Browse catalog
              </Button>
            }
          />
        </div>
      ) : (
        <ul className={styles.list} role="list">
          {agents.map((a) => {
            const h = HEALTH_META[a.health];
            return (
              <li key={a.id}>
                <Link href={`/agents/${encodeURIComponent(a.id)}`} className={styles.row}>
                  <span className={styles.icon} aria-hidden>
                    <StatusDot tone={h.tone} pulse={a.health === "reconciling"} />
                  </span>
                  <div className={styles.main}>
                    <div className={styles.nameRow}>
                      <span className={styles.name}>{a.name}</span>
                      {a.channel === "beta" && <span className={styles.beta}>beta</span>}
                    </div>
                    <div className={styles.meta}>
                      <span className="mono">v{a.version}</span>
                      {a.drift && (
                        <span className={styles.drift} title={`Converging to v${a.desiredVersion}`}>
                          <RefreshCw size={11} strokeWidth={2.6} aria-hidden />
                          <span className="mono">v{a.desiredVersion}</span>
                        </span>
                      )}
                      <span className={styles.sep} aria-hidden>·</span>
                      <span className="mono">{a.model}</span>
                    </div>
                  </div>
                  <div className={styles.publish}>
                    {a.publishTo.map((p) => {
                      const Icon = PUBLISH[p].icon;
                      return (
                        <span key={p} className={styles.chip} title={`Published to ${PUBLISH[p].label}`}>
                          <Icon size={12} strokeWidth={2.2} aria-hidden />
                          {PUBLISH[p].label}
                        </span>
                      );
                    })}
                  </div>
                  <div className={styles.status}>
                    <StatusBadge tone={h.tone} label={h.label} pulse={a.health === "reconciling"} variant={a.health === "healthy" ? "plain" : "soft"} />
                  </div>
                  <ChevronRight size={16} strokeWidth={2} className={styles.chevron} aria-hidden />
                </Link>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}
