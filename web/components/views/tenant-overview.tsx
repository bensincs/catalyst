"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { ArrowLeft, ArrowUpRight, Bot, ShieldCheck } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button } from "@/components/ui/button";
import { StatusBadge, StatusDot } from "@/components/ui/status";
import { formatRelative } from "@/lib/format";
import {
  LIFECYCLE_META,
  type Application,
  type EnabledAgent,
  type Infrastructure,
  type Lifecycle,
  type MemoryStore,
  type TenantContextInfo,
} from "@/lib/types";
import { useToast } from "@/components/providers/toast-provider";
import { DependencyGraph } from "./dependency-graph";
import styles from "./tenant-overview.module.css";
import installStyles from "./install-view.module.css";

export function TenantOverview({
  tenant,
  agents,
  now,
  platformView = false,
  infrastructure,
  applications,
  stores,
}: {
  tenant: TenantContextInfo;
  agents: EnabledAgent[];
  now: number;
  platformView?: boolean;
  infrastructure?: Infrastructure[];
  applications?: Application[];
  stores?: MemoryStore[];
}) {
  const { toast } = useToast();
  const router = useRouter();
  const lc = LIFECYCLE_META[tenant.lifecycle];
  const recon = reconStatus(tenant.lifecycle);
  const installed = tenant.enrollment === "bound";

  return (
    <div>
      {platformView && (
        <Link href="/" className={styles.backLink}>
          <ArrowLeft size={14} strokeWidth={2.2} aria-hidden />
          Fleet
          <span className={styles.backSep} aria-hidden>
            ·
          </span>
          <span className={styles.backNote}>viewing as platform admin</span>
        </Link>
      )}

      <PageHeader
        title={platformView ? tenant.name : "Overview"}
        description={
          platformView
            ? `The agents, workloads, and infrastructure Cortex has provisioned for ${tenant.name} into the tenant's own subscription via Azure Lighthouse.`
            : "Everything Cortex runs in your tenant — agents, workloads, and the infrastructure behind them — under your own identity."
        }
        actions={
          installed ? (
            <Button variant="primary" icon={Bot} onClick={() => router.push("/agents")}>
              Browse agents
            </Button>
          ) : platformView ? (
            <Button
              variant="secondary"
              icon={ShieldCheck}
              onClick={() => toast({ title: "Awaiting the tenant's Lighthouse delegation", tone: "neutral" })}
            >
              Awaiting delegation
            </Button>
          ) : (
            <Button variant="primary" icon={ShieldCheck} iconRight={ArrowUpRight} onClick={() => router.push("/install")}>
              Set up install
            </Button>
          )
        }
      />

      {/* Compact install status — the staged checks + identity manifest live on /install */}
      <section className={installStyles.statusLine} aria-label="Install status">
        <StatusBadge tone={lc.tone} label={lc.label} pulse={tenant.lifecycle === "live"} />
        <span className={installStyles.statusHeartbeat}>
          <StatusDot tone={recon.tone} pulse={recon.pulse} />
          {recon.label}
          {recon.showTime && (
            <span className={installStyles.statusTime}>{formatRelative(tenant.lastHeartbeatMs, now)}</span>
          )}
        </span>
        <span className={installStyles.statusSpacer} aria-hidden />
        {!platformView && (
          <Link href="/install" className={styles.installLink}>
            View install
            <ArrowUpRight size={14} strokeWidth={2} aria-hidden />
          </Link>
        )}
      </section>

      {/* Dependency topology — the hero: everything enabled in the tenant's subscription */}
      <section className={installStyles.topology} aria-label="Dependency topology">
        <div className={installStyles.topologyHead}>
          <h2 className={installStyles.topologyTitle}>Topology</h2>
          <span className={installStyles.topologyDesc}>
            {platformView
              ? "What Cortex has provisioned in this tenant's subscription, and how each piece depends on the others."
              : "What Cortex has provisioned in your subscription, and how each piece depends on the others."}
          </span>
        </div>
        <DependencyGraph
          infrastructure={infrastructure ?? []}
          applications={applications ?? []}
          agents={agents}
          stores={stores ?? []}
        />
      </section>
    </div>
  );
}

// reconStatus maps the tenant's derived lifecycle to how its reconciler reads in
// the compact status line — honest about staleness (a bound-but-silent
// reconciler is "unreachable", not "healthy").
function reconStatus(lifecycle: Lifecycle): {
  tone: "success" | "warning" | "neutral";
  label: string;
  pulse: boolean;
  showTime: boolean;
} {
  switch (lifecycle) {
    case "live":
      return { tone: "success", label: "Reconciler healthy", pulse: true, showTime: true };
    case "degraded":
      return { tone: "warning", label: "Reconciler unreachable", pulse: false, showTime: true };
    case "suspended":
      return { tone: "neutral", label: "Suspended", pulse: false, showTime: false };
    default:
      return { tone: "neutral", label: "Not installed yet", pulse: false, showTime: false };
  }
}
