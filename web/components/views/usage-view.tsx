"use client";

import { Activity, ArrowUpRight, Bot } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button } from "@/components/ui/button";
import { StatusDot } from "@/components/ui/status";
import { EmptyState } from "@/components/ui/empty-state";
import { formatCount, formatInt } from "@/lib/format";
import { HEALTH_META, type EnabledAgent } from "@/lib/types";
import styles from "./usage-view.module.css";

const COST_DOCS_URL =
  "https://learn.microsoft.com/azure/cost-management-billing/costs/quick-acm-cost-analysis";

export function UsageView({ agents }: { agents: EnabledAgent[] }) {
  const total = agents.reduce((n, a) => n + a.calls30d, 0);
  const ranked = [...agents].sort((a, b) => b.calls30d - a.calls30d);
  const models = modelBreakdown(agents);
  const top = ranked[0];

  return (
    <div>
      <PageHeader
        title="Usage"
        description="Per-agent call volume over the last 30 days, reported by your reconciler. Model and compute run on your own Azure subscription."
      />

      {agents.length === 0 ? (
        <div className={styles.emptyWrap}>
          <EmptyState
            icon={Activity}
            title="No usage yet"
            description="Once you enable an agent and it starts answering over its endpoint, call volume and model mix appear here — reported from your own tenant on every reconciler heartbeat."
            action={
              <Button variant="primary" icon={Bot} onClick={() => (window.location.href = "/catalog")}>
                Browse catalog
              </Button>
            }
          />
        </div>
      ) : (
        <>
          <section className={styles.stats} aria-label="Usage summary">
            <Stat label="Calls · 30d" value={formatCount(total)} sub="across all agents" />
            <Stat label="Enabled agents" value={formatInt(agents.length)} sub={`${models.length} models`} />
            <Stat label="Busiest agent" value={top?.name ?? "—"} sub={top ? `${formatCount(top.calls30d)} calls` : ""} wide />
          </section>

          <section aria-label="Calls by agent">
            <h2 className={styles.sectionTitle}>Calls by agent</h2>
            <div className={styles.table}>
              {ranked.map((a) => {
                const h = HEALTH_META[a.health];
                const share = total > 0 ? a.calls30d / total : 0;
                return (
                  <div key={a.id} className={styles.row}>
                    <div className={styles.rowHead}>
                      <StatusDot tone={h.tone} pulse={a.health === "reconciling"} />
                      <span className={styles.rowName}>{a.name}</span>
                      <span className={"mono " + styles.rowModel}>{a.model}</span>
                    </div>
                    <div className={styles.bar} aria-hidden>
                      <span className={styles.barFill} style={{ width: `${Math.max(share * 100, 1.5)}%` }} />
                    </div>
                    <div className={styles.rowFigs}>
                      <span className={"tnum " + styles.rowCalls}>{formatInt(a.calls30d)}</span>
                      <span className={styles.rowShare}>{(share * 100).toFixed(share < 0.1 ? 1 : 0)}%</span>
                    </div>
                  </div>
                );
              })}
            </div>
          </section>

          {models.length > 1 && (
            <section aria-label="Calls by model" className={styles.modelsSection}>
              <h2 className={styles.sectionTitle}>Calls by model</h2>
              <div className={styles.models}>
                {models.map((m) => (
                  <div key={m.model} className={styles.model}>
                    <span className={"mono " + styles.modelName}>{m.model}</span>
                    <div className={styles.bar} aria-hidden>
                      <span
                        className={styles.barFill}
                        data-series="model"
                        style={{ width: `${Math.max((total > 0 ? m.calls / total : 0) * 100, 1.5)}%` }}
                      />
                    </div>
                    <span className={"tnum " + styles.modelCalls}>{formatCount(m.calls)}</span>
                  </div>
                ))}
              </div>
            </section>
          )}
        </>
      )}

      {/* Honest cost state — Cortex never sees the bill */}
      <section className={styles.cost} aria-label="Cost showback">
        <div className={styles.costText}>
          <h3 className={styles.costTitle}>Cost showback</h3>
          <p className={styles.costBody}>
            Spend lands on your own Azure invoice — Cortex never touches your billing data. Connect
            Azure Cost Management to attribute model and compute cost back to each agent here.
          </p>
        </div>
        <Button
          variant="secondary"
          icon={ArrowUpRight}
          onClick={() => window.open(COST_DOCS_URL, "_blank", "noopener,noreferrer")}
        >
          Connect cost data
        </Button>
      </section>
    </div>
  );
}

function modelBreakdown(agents: EnabledAgent[]): { model: string; calls: number }[] {
  const map = new Map<string, number>();
  for (const a of agents) map.set(a.model, (map.get(a.model) ?? 0) + a.calls30d);
  return [...map.entries()].map(([model, calls]) => ({ model, calls })).sort((a, b) => b.calls - a.calls);
}

function Stat({
  label,
  value,
  sub,
  wide = false,
}: {
  label: string;
  value: string;
  sub?: string;
  wide?: boolean;
}) {
  return (
    <div className={styles.stat} data-wide={wide || undefined}>
      <span className={styles.statLabel}>{label}</span>
      <span className={styles.statValue}>{value}</span>
      {sub && <span className={styles.statSub}>{sub}</span>}
    </div>
  );
}
