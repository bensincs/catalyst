"use client";

import {
  AlertTriangle,
  Bot,
  Boxes,
  Check,
  Fingerprint,
  Layers,
  Minus,
  Radio,
  RefreshCw,
  ServerCog,
  ShieldCheck,
  X,
  type LucideIcon,
} from "lucide-react";
import { StatusDot } from "@/components/ui/status";
import { formatRelative } from "@/lib/format";
import {
  type Application,
  type ClusterInfo,
  type EnabledAgent,
  type Infrastructure,
  type Lifecycle,
  type TenantContextInfo,
} from "@/lib/types";
import styles from "./install-view.module.css";

// Staged system checks — reads top to bottom as the install comes online. The
// last three mirror the topology's deployable kinds: infrastructure → applications
// → agents, each summarized from the tenant's enabled resources.
export function InstallStatusChecks({
  tenant,
  infrastructure,
  applications,
  agents,
}: {
  tenant: TenantContextInfo;
  infrastructure: Infrastructure[];
  applications: Application[];
  agents: EnabledAgent[];
}) {
  const checks = buildChecks({
    live: tenant.lifecycle === "live",
    degraded: tenant.lifecycle === "degraded",
    cluster: tenant.cluster,
    infrastructure,
    applications,
    agents,
  });

  return (
    <section className={styles.checks} aria-label="Install status checks">
      {checks.map((c) => (
        <div key={c.key} className={styles.check} data-status={c.status}>
          <span className={styles.checkMark} aria-hidden>
            <CheckMark check={c} />
          </span>
          <span className={styles.checkText}>
            <span className={styles.checkTitle}>{c.title}</span>
            <span className={styles.checkNote}>{c.note}</span>
          </span>
          <span className={styles.checkState}>{STATUS_LABEL[c.status]}</span>
        </div>
      ))}
    </section>
  );
}

// Reconciler status strip — honest about staleness on each poll.
export function ReconcilerStatus({ tenant, now }: { tenant: TenantContextInfo; now: number }) {
  const recon = reconStatus(tenant.lifecycle);
  const live = tenant.lifecycle === "live";

  return (
    <section className={styles.reconciler} aria-label="Reconciler">
      <span className={styles.reconIcon} data-tone={recon.tone} aria-hidden>
        <ServerCog size={19} strokeWidth={2} />
      </span>
      <div className={styles.reconBody}>
        <div className={styles.reconTop}>
          <StatusDot tone={recon.tone} pulse={live} />
          <span className={styles.reconLabel}>{recon.label}</span>
          {(live || tenant.lifecycle === "degraded") && (
            <span className={styles.reconTime}>· heartbeat {formatRelative(tenant.lastHeartbeatMs, now)}</span>
          )}
        </div>
        <p className={styles.reconSub}>{recon.sub}</p>
      </div>
      <div className={styles.reconVersion}>
        <span className={styles.reconVersionLabel}>Reconciler</span>
        <span className={"mono " + styles.reconVersionValue}>
          {tenant.reconcilerVersion ? `v${tenant.reconcilerVersion}` : "—"}
        </span>
      </div>
    </section>
  );
}

function reconStatus(lifecycle: Lifecycle): {
  tone: "success" | "warning" | "neutral" | "info";
  label: string;
  sub: string;
} {
  switch (lifecycle) {
    case "live":
      return {
        tone: "success",
        label: "Reconciler healthy",
        sub: "Converging your tenant to desired state and reporting agent health on each poll.",
      };
    case "degraded":
      return {
        tone: "warning",
        label: "Reconciler unreachable",
        sub: "The last heartbeat is stale. The reconciler may be restarting, throttled, or blocked from reaching the control plane.",
      };
    case "suspended":
      return {
        tone: "neutral",
        label: "Suspended",
        sub: "This tenant is administratively suspended. Reconciliation is paused.",
      };
    default:
      return {
        tone: "info",
        label: "Awaiting first heartbeat",
        sub: "Once you delegate your subscription, Cortex provisions the reconciler and it enrolls over its own managed identity — this turns live on its first heartbeat.",
      };
  }
}

type CheckStatus = "ok" | "working" | "warn" | "failed" | "pending";

interface Check {
  key: string;
  title: string;
  note: string;
  status: CheckStatus;
  icon: LucideIcon; // shown while pending
}

const STATUS_LABEL: Record<CheckStatus, string> = {
  ok: "OK",
  working: "Working",
  warn: "Stale",
  failed: "Action needed",
  pending: "Pending",
};

function CheckMark({ check }: { check: Check }) {
  switch (check.status) {
    case "ok":
      return <Check size={14} strokeWidth={3} />;
    case "failed":
      return <X size={14} strokeWidth={3} />;
    case "working":
      return <RefreshCw size={13} strokeWidth={2.4} />;
    case "warn":
      return <AlertTriangle size={13} strokeWidth={2.4} />;
    default:
      return <Minus size={13} strokeWidth={2.4} />;
  }
}

// buildChecks reads top-to-bottom as the install comes online. Onboarding is a
// single Lighthouse delegation; everything after it is provisioned by the control
// plane: directory → delegation → environment (reconciler + Foundry) → reconciler
// heartbeat → infrastructure → applications → agents.
function buildChecks(x: {
  live: boolean;
  degraded: boolean;
  cluster: ClusterInfo;
  infrastructure: Infrastructure[];
  applications: Application[];
  agents: EnabledAgent[];
}): Check[] {
  const { live, degraded, cluster, infrastructure, applications, agents } = x;

  const lighthouse: Check = cluster.infraDelegated
    ? {
        key: "lighthouse",
        title: "Delegation active",
        note: cluster.infraDetail || "Cortex manages this subscription via Azure Lighthouse.",
        status: "ok",
        icon: Radio,
      }
    : {
        key: "lighthouse",
        title: "Delegate via Lighthouse",
        note: "Deploy the delegation below — the one step that hands provisioning to Cortex.",
        status: "pending",
        icon: Radio,
      };

  const fp = cluster.footprintState ?? "";
  let environment: Check;
  if (fp === "ready") {
    environment = { key: "environment", title: "Environment provisioned", note: cluster.footprintDetail || "Reconciler, Foundry, and cluster deployed into your subscription.", status: "ok", icon: ServerCog };
  } else if (fp === "provisioning") {
    environment = { key: "environment", title: "Provisioning environment", note: cluster.footprintDetail || "Cortex is deploying the reconciler + Foundry into your subscription…", status: "working", icon: ServerCog };
  } else if (fp === "failed") {
    environment = { key: "environment", title: "Environment failed", note: cluster.footprintDetail || "The footprint deployment failed — check quota and the delegation's permissions.", status: "failed", icon: ServerCog };
  } else if (!cluster.infraDelegated) {
    environment = { key: "environment", title: "Environment", note: "Provisioned automatically by Cortex once you delegate.", status: "pending", icon: ServerCog };
  } else {
    environment = { key: "environment", title: "Environment", note: "Queued — a platform admin enables the tenant, then Cortex provisions it.", status: "pending", icon: ServerCog };
  }

  const reconciler: Check = live
    ? { key: "reconciler", title: "Reconciler running", note: "Heartbeating desired vs. actual state.", status: "ok", icon: ShieldCheck }
    : degraded
      ? { key: "reconciler", title: "Reconciler unreachable", note: "The last heartbeat is stale — it may be restarting or blocked from the control plane.", status: "warn", icon: ShieldCheck }
      : { key: "reconciler", title: "Reconciler", note: "Comes online once the environment is provisioned.", status: "pending", icon: ShieldCheck };

  // Infrastructure (Azure/Bicep) — provisioned by the control plane via Lighthouse.
  const infraTotal = infrastructure.length;
  const infraReady = infrastructure.filter((i) => i.infraState === "ready").length;
  const infraFailed = infrastructure.filter((i) => i.infraState === "failed").length;
  const infraWorking = infraTotal - infraReady - infraFailed;
  let infraCheck: Check;
  if (infraTotal === 0) {
    infraCheck = { key: "infra", title: "Infrastructure", note: "No infrastructure enabled yet.", status: "pending", icon: Boxes };
  } else if (infraFailed > 0) {
    infraCheck = { key: "infra", title: "Infrastructure failed", note: `${infraFailed} of ${infraTotal} module${infraTotal === 1 ? "" : "s"} failed to provision — check its module + parameters.`, status: "failed", icon: Boxes };
  } else if (infraWorking > 0) {
    infraCheck = { key: "infra", title: "Provisioning infrastructure", note: `${infraReady}/${infraTotal} ready · ${infraWorking} deploying via Lighthouse…`, status: "working", icon: Boxes };
  } else {
    infraCheck = { key: "infra", title: "Infrastructure ready", note: `${infraTotal} module${infraTotal === 1 ? "" : "s"} provisioned into your subscription.`, status: "ok", icon: Boxes };
  }

  // Applications (Helm) — synced into the cluster by the reconciler's Argo CD.
  const appTotal = applications.length;
  const appLive = applications.filter((a) => a.health === "live").length;
  const appBlocked = applications.filter((a) => a.health === "blocked").length;
  let appsCheck: Check;
  if (appTotal === 0) {
    appsCheck = { key: "applications", title: "Applications", note: "No applications enabled yet.", status: "pending", icon: Layers };
  } else if (appBlocked > 0) {
    appsCheck = { key: "applications", title: "Applications blocked", note: `${appBlocked} of ${appTotal} deployment${appTotal === 1 ? "" : "s"} blocked — check its chart + dependencies.`, status: "failed", icon: Layers };
  } else if (appLive < appTotal) {
    appsCheck = { key: "applications", title: "Applications converging", note: `${appLive}/${appTotal} live · syncing into the cluster via Argo CD…`, status: "working", icon: Layers };
  } else {
    appsCheck = { key: "applications", title: "Applications serving", note: `${appTotal} deployment${appTotal === 1 ? "" : "s"} synced and healthy.`, status: "ok", icon: Layers };
  }

  // Agents — converged into the tenant's Foundry project by the reconciler.
  const agentTotal = agents.length;
  const agentsLive = agents.filter((a) => a.health === "live").length;
  let agentsCheck: Check;
  if (agentTotal === 0) {
    agentsCheck = { key: "agents", title: "Agents", note: "Enable agents from your catalog.", status: "pending", icon: Bot };
  } else if (live && agentsLive === agentTotal) {
    agentsCheck = { key: "agents", title: "Agents serving", note: `${agentTotal} enabled and converged.`, status: "ok", icon: Bot };
  } else {
    agentsCheck = { key: "agents", title: "Agents converging", note: `${agentsLive}/${agentTotal} live · awaiting reconciler.`, status: "working", icon: Bot };
  }

  return [
    { key: "directory", title: "Directory connected", note: "Signed in with Microsoft Entra.", status: "ok", icon: Fingerprint },
    lighthouse,
    environment,
    reconciler,
    infraCheck,
    appsCheck,
    agentsCheck,
  ];
}
