"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  AppWindow,
  ArrowLeft,
  ArrowUpRight,
  Bot,
  Globe,
  MessageSquare,
  Settings2,
  ShieldCheck,
} from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button } from "@/components/ui/button";
import { StatusBadge, StatusDot } from "@/components/ui/status";
import { EmptyState } from "@/components/ui/empty-state";
import { useToast } from "@/components/providers/toast-provider";
import { formatCount, formatInt, formatRelative } from "@/lib/format";
import {
  HEALTH_META,
  LIFECYCLE_META,
  type Application,
  type EnabledAgent,
  type Infrastructure,
  type Lifecycle,
  type MemoryStore,
  type PublishTarget,
  type TenantContextInfo,
} from "@/lib/types";
import { DependencyGraph } from "./dependency-graph";
import { InstallStatusChecks, ReconcilerStatus, type InfraSummary } from "./install-status";
import styles from "./tenant-overview.module.css";
import installStyles from "./install-view.module.css";

const PUBLISH: Record<PublishTarget, { label: string; icon: typeof Globe }> = {
  api: { label: "API", icon: Globe },
  teams: { label: "Teams", icon: MessageSquare },
  m365: { label: "M365", icon: AppWindow },
};

export function TenantOverview({
  tenant,
  agents,
  now,
  platformView = false,
  infrastructure,
  applications,
  stores,
  infra,
}: {
  tenant: TenantContextInfo;
  agents: EnabledAgent[];
  now: number;
  platformView?: boolean;
  infrastructure?: Infrastructure[];
  applications?: Application[];
  stores?: MemoryStore[];
  infra?: InfraSummary;
}) {
  const { toast } = useToast();
  const router = useRouter();
  const lc = LIFECYCLE_META[tenant.lifecycle];
  const recon = reconStatus(tenant.lifecycle);
  const installed = tenant.enrollment === "bound";
  const totalCalls = agents.reduce((n, a) => n + a.calls30d, 0);
  const models = new Set(agents.map((a) => a.model)).size;

  // The tenant's own overview carries the live status surfaces (topology graph,
  // staged install checks, reconciler heartbeat). The platform drill-in isn't
  // handed that tenant-scoped data (infra is absent), so it degrades to just the
  // compact status line below.
  const showStatus = !platformView && infra !== undefined;

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
            ? `Install, agents, and workloads for ${tenant.name} — provisioned by Cortex into the tenant's own subscription via Azure Lighthouse.`
            : "Your tenant's install, the agents running in it, and the workloads they're serving — everything under your own identity."
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

      {/* Compact install status — the full identity manifest lives on /install */}
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

      {showStatus && infra && (
        <>
          {/* Dependency topology — the hero: the enabled graph in the tenant's subscription */}
          <section className={installStyles.topology} aria-label="Dependency topology">
            <div className={installStyles.topologyHead}>
              <h2 className={installStyles.topologyTitle}>Topology</h2>
              <span className={installStyles.topologyDesc}>
                What Cortex has provisioned in your subscription, and how each piece depends on the others.
              </span>
            </div>
            <DependencyGraph
              infrastructure={infrastructure ?? []}
              applications={applications ?? []}
              agents={agents}
              stores={stores ?? []}
            />
          </section>

          {/* Staged install-status checks + reconciler heartbeat */}
          <InstallStatusChecks tenant={tenant} agentCount={agents.length} infra={infra} />
          <ReconcilerStatus tenant={tenant} now={now} />
        </>
      )}

      {/* Usage snapshot */}
      <section className={styles.snapshot} aria-label="Usage snapshot">
        <Snap label="Enabled agents" value={`${agents.length}`} />
        <Snap label="Calls · 30d" value={formatCount(totalCalls)} />
        <Snap label="Models in use" value={`${models}`} />
        <Snap label="Publish targets" value={agents.length ? publishSummary(agents) : "—"} wide />
      </section>

      {/* Enabled agents */}
      <section aria-label="Enabled agents">
        <div className={styles.sectionHead}>
          <h2 className={styles.sectionTitle}>
            Enabled agents
            <span className={styles.sectionCount}>{agents.length}</span>
          </h2>
          <button className={styles.sectionLink} onClick={() => router.push("/agents")}>
            Browse agents
            <ArrowUpRight size={14} strokeWidth={2} aria-hidden />
          </button>
        </div>

        {agents.length === 0 ? (
          <div className={styles.agentEmpty}>
            <EmptyState
              icon={Bot}
              title="No agents enabled yet"
              description="Browse the agents your platform team has entitled you to, configure one against your own knowledge, and the reconciler brings it live in your Foundry project."
              action={
                <Button variant="primary" onClick={() => router.push("/agents")}>
                  Browse agents
                </Button>
              }
              compact
            />
          </div>
        ) : (
          <ul className={styles.agentList} role="list">
            {agents.map((a) => (
              <AgentRow key={a.id} agent={a} onConfigure={() => toast({ title: `Configure ${a.name}`, tone: "neutral" })} />
            ))}
          </ul>
        )}
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

function publishSummary(agents: EnabledAgent[]): string {
  const set = new Set<PublishTarget>();
  agents.forEach((a) => a.publishTo.forEach((p) => set.add(p)));
  return (["api", "teams", "m365"] as PublishTarget[])
    .filter((p) => set.has(p))
    .map((p) => PUBLISH[p].label)
    .join(" · ");
}

function Snap({ label, value, wide = false }: { label: string; value: string; wide?: boolean }) {
  return (
    <div className={styles.snap} data-wide={wide || undefined}>
      <span className={styles.snapLabel}>{label}</span>
      <span className={styles.snapValue}>{value}</span>
    </div>
  );
}

function AgentRow({ agent, onConfigure }: { agent: EnabledAgent; onConfigure: () => void }) {
  const h = HEALTH_META[agent.health];
  return (
    <li className={styles.agent}>
      <div className={styles.agentMain}>
        <div className={styles.agentIcon} aria-hidden data-tone={h.tone}>
          <StatusDot tone={h.tone} pulse={agent.health === "reconciling"} />
        </div>
        <div className={styles.agentInfo}>
          <div className={styles.agentNameRow}>
            <span className={styles.agentName}>{agent.name}</span>
            {agent.channel === "beta" && <span className={styles.betaTag}>beta</span>}
          </div>
          <div className={styles.agentMeta}>
            <span className="mono">v{agent.version}</span>
            <span className={styles.metaSep} aria-hidden>·</span>
            <span className="mono">{agent.model}</span>
            {agent.note && (
              <>
                <span className={styles.metaSep} data-note aria-hidden>·</span>
                <span className={styles.agentNote}>{agent.note}</span>
              </>
            )}
          </div>
        </div>
      </div>

      <div className={styles.publish}>
        {agent.publishTo.map((p) => {
          const Icon = PUBLISH[p].icon;
          return (
            <span key={p} className={styles.publishChip} title={`Published to ${PUBLISH[p].label}`}>
              <Icon size={12} strokeWidth={2.2} aria-hidden />
              {PUBLISH[p].label}
            </span>
          );
        })}
      </div>

      <div className={styles.agentCalls}>
        <span className="tnum">{formatInt(agent.calls30d)}</span>
        <span className={styles.agentCallsLabel}>calls · 30d</span>
      </div>

      <div className={styles.agentStatus}>
        <StatusBadge tone={h.tone} label={h.label} pulse={agent.health === "reconciling"} variant={agent.health === "live" ? "plain" : "soft"} />
      </div>

      <Button size="sm" variant="ghost" icon={Settings2} iconOnly aria-label={`Configure ${agent.name}`} onClick={onConfigure} className={styles.agentConfig} />
    </li>
  );
}
