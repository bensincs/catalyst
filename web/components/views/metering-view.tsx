"use client";

import { ArrowUpRight, Gauge, Radar } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button } from "@/components/ui/button";
import { StatusDot } from "@/components/ui/status";
import { EmptyState } from "@/components/ui/empty-state";
import { formatCount, formatInt } from "@/lib/format";
import { LIFECYCLE_META, type FleetStats, type TenantSummary } from "@/lib/types";
import styles from "./metering-view.module.css";

const COST_DOCS_URL =
  "https://learn.microsoft.com/azure/cost-management-billing/costs/quick-acm-cost-analysis";

export function MeteringView({ stats, tenants }: { stats: FleetStats; tenants: TenantSummary[] }) {
  const total = stats.callsMonth;
  const ranked = [...tenants].sort((a, b) => b.monthlyCalls - a.monthlyCalls);
  const live = tenants.filter((t) => t.lifecycle === "live").length;
  const avg = tenants.length ? Math.round(total / tenants.length) : 0;

  return (
    <div>
      <PageHeader
        title="Metering"
        description="Fleet-wide call volume, rolled up from reconciler heartbeats. Consumption stays on each customer's own Azure bill; Cortex surfaces per-tenant showback."
      />

      {tenants.length === 0 ? (
        <div className={styles.emptyWrap}>
          <EmptyState
            icon={Gauge}
            title="No metered tenants yet"
            description="As tenants enroll and their agents start answering, per-tenant call volume rolls up here from heartbeat data — the fleet-wide view of who's using what."
            action={
              <Button variant="primary" icon={Radar} onClick={() => (window.location.href = "/")}>
                Open Fleet
              </Button>
            }
          />
        </div>
      ) : (
        <>
          <section className={styles.stats} aria-label="Fleet metering summary">
            <Stat label="Calls · 30d" value={formatCount(total)} sub="across the fleet" />
            <Stat label="Tenants live" value={`${live}/${tenants.length}`} sub="heartbeating now" />
            <Stat label="Agents running" value={formatInt(stats.agents)} sub="fleet-wide" />
            <Stat label="Avg · tenant" value={formatCount(avg)} sub="calls · 30d" />
          </section>

          <section aria-label="Calls by tenant">
            <h2 className={styles.sectionTitle}>Calls by tenant</h2>
            <div className={styles.table}>
              {ranked.map((t) => {
                const lc = LIFECYCLE_META[t.lifecycle];
                const share = total > 0 ? t.monthlyCalls / total : 0;
                return (
                  <div key={t.id} className={styles.row}>
                    <div className={styles.rowHead}>
                      <StatusDot tone={lc.tone} pulse={t.lifecycle === "live"} />
                      <span className={styles.rowName}>{t.name}</span>
                      <span className={styles.plan} data-plan={t.plan}>
                        {t.plan}
                      </span>
                      <span className={styles.rowRegion}>{t.region}</span>
                    </div>
                    <div className={styles.bar} aria-hidden>
                      <span className={styles.barFill} style={{ transform: `scaleX(${Math.max(share, 0.015)})` }} />
                    </div>
                    <div className={styles.rowFigs}>
                      <span className={"tnum " + styles.rowCalls}>{formatInt(t.monthlyCalls)}</span>
                      <span className={styles.rowShare}>{(share * 100).toFixed(share < 0.1 ? 1 : 0)}%</span>
                    </div>
                  </div>
                );
              })}
            </div>
          </section>
        </>
      )}

      <section className={styles.cost} aria-label="Cost showback">
        <div className={styles.costText}>
          <h3 className={styles.costTitle}>Showback &amp; entitlements</h3>
          <p className={styles.costBody}>
            Model and compute spend stays on each tenant&rsquo;s own Azure invoice. Connect Azure
            Cost Management to attribute cost per tenant and reconcile it against plan entitlements.
          </p>
        </div>
        <Button
          variant="secondary"
          icon={ArrowUpRight}
          onClick={() => window.open(COST_DOCS_URL, "_blank", "noopener,noreferrer")}
        >
          Configure showback
        </Button>
      </section>
    </div>
  );
}

function Stat({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className={styles.stat}>
      <span className={styles.statLabel}>{label}</span>
      <span className={styles.statValue}>{value}</span>
      {sub && <span className={styles.statSub}>{sub}</span>}
    </div>
  );
}
