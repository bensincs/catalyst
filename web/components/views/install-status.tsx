"use client";

import {
  AlertTriangle,
  Bot,
  Check,
  Cloud,
  Fingerprint,
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
import { type ClusterInfo, type Lifecycle, type TenantContextInfo } from "@/lib/types";
import styles from "./install-view.module.css";

/** Aggregate provisioning state of a tenant's enabled deployments that carry
 *  Azure infra (deployed by the control plane via Lighthouse). */
export interface InfraSummary {
  total: number;
  ready: number;
  provisioning: number;
  failed: number;
}

// Staged system checks — reads top to bottom as the install comes online.
export function InstallStatusChecks({
  tenant,
  agentCount,
  infra,
}: {
  tenant: TenantContextInfo;
  agentCount: number;
  infra: InfraSummary;
}) {
  const checks = buildChecks({
    bound: tenant.enrollment === "bound",
    live: tenant.lifecycle === "live",
    degraded: tenant.lifecycle === "degraded",
    agentCount,
    cluster: tenant.cluster,
    infra,
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
// heartbeat → application infra → agents.
function buildChecks(x: {
  bound: boolean;
  live: boolean;
  degraded: boolean;
  agentCount: number;
  cluster: ClusterInfo;
  infra: InfraSummary;
}): Check[] {
  const { live, degraded, agentCount, cluster, infra } = x;

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

  let infraCheck: Check;
  if (infra.total === 0) {
    infraCheck = { key: "infra", title: "Application infrastructure", note: "No deployment declares Azure infrastructure yet.", status: "pending", icon: Cloud };
  } else if (infra.failed > 0) {
    infraCheck = { key: "infra", title: "Infrastructure failed", note: `${infra.failed} of ${infra.total} deployment${infra.total === 1 ? "" : "s"} failed to provision — check its module + parameters.`, status: "failed", icon: Cloud };
  } else if (infra.provisioning > 0) {
    infraCheck = { key: "infra", title: "Provisioning infrastructure", note: `${infra.ready}/${infra.total} ready · ${infra.provisioning} deploying via Lighthouse…`, status: "working", icon: Cloud };
  } else {
    infraCheck = { key: "infra", title: "Application infrastructure", note: `${infra.total} deployment${infra.total === 1 ? "" : "s"} provisioned into your subscription.`, status: "ok", icon: Cloud };
  }

  const agents: Check =
    live && agentCount > 0
      ? { key: "agents", title: "Agents serving", note: `${agentCount} enabled and converged.`, status: "ok", icon: Bot }
      : agentCount > 0
        ? { key: "agents", title: "Agents converging", note: `${agentCount} enabled · awaiting reconciler.`, status: "working", icon: Bot }
        : { key: "agents", title: "Agents", note: "Enable agents from your catalog.", status: "pending", icon: Bot };

  return [
    { key: "directory", title: "Directory connected", note: "Signed in with Microsoft Entra.", status: "ok", icon: Fingerprint },
    lighthouse,
    environment,
    reconciler,
    infraCheck,
    agents,
  ];
}
