"use client";

import {
  ArrowUpRight,
  Bot,
  Check,
  Cpu,
  Fingerprint,
  Landmark,
  ServerCog,
  ShieldCheck,
} from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button } from "@/components/ui/button";
import { StatusBadge, StatusDot } from "@/components/ui/status";
import { formatRelative } from "@/lib/format";
import { LIFECYCLE_META, type Lifecycle, type TenantContextInfo } from "@/lib/types";
import styles from "./install-view.module.css";

const DEPLOY_URL =
  process.env.NEXT_PUBLIC_CORTEX_DEPLOY_URL ??
  "https://portal.azure.com/#create/Microsoft.Solutions%2FmanagedApplications";

type StepState = "done" | "current" | "pending";

export function InstallView({
  tenant,
  agentCount,
  now,
}: {
  tenant: TenantContextInfo;
  agentCount: number;
  now: number;
}) {
  const lc = LIFECYCLE_META[tenant.lifecycle];
  const recon = reconStatus(tenant.lifecycle);
  const bound = tenant.enrollment === "bound";
  const live = tenant.lifecycle === "live";

  const steps = buildSteps({ bound, live, degraded: tenant.lifecycle === "degraded", agentCount });
  const deploy = () => window.open(DEPLOY_URL, "_blank", "noopener,noreferrer");

  return (
    <div>
      <PageHeader
        title="Install"
        description="The Cortex app in your own Azure subscription — reconciler, Foundry project, and enrollment. Everything runs under your identity, in your tenant."
        meta={<StatusBadge tone={lc.tone} label={lc.label} pulse={live} />}
        actions={
          bound ? (
            <Button variant="secondary" icon={ArrowUpRight} onClick={deploy}>
              Manage in Azure
            </Button>
          ) : (
            <Button variant="primary" icon={ShieldCheck} iconRight={ArrowUpRight} onClick={deploy}>
              Deploy to Azure
            </Button>
          )
        }
      />

      {/* Deployment lifecycle — desired vs. actual, made legible */}
      <section className={styles.panel} aria-label="Deployment lifecycle">
        <ol className={styles.steps} role="list">
          {steps.map((s) => (
            <li key={s.key} className={styles.step} data-state={s.state} data-tone={s.tone}>
              <span className={styles.stepMark} aria-hidden>
                {s.state === "done" ? <Check size={13} strokeWidth={3} /> : <s.icon size={14} strokeWidth={2} />}
              </span>
              <span className={styles.stepText}>
                <span className={styles.stepTitle}>{s.title}</span>
                <span className={styles.stepNote}>{s.note}</span>
              </span>
            </li>
          ))}
        </ol>
      </section>

      {/* Reconciler status */}
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

      {/* Identity manifest — what runs where, and as whom */}
      <section aria-label="Install identity" className={styles.manifestWrap}>
        <h2 className={styles.sectionTitle}>Install identity</h2>
        <dl className={styles.manifest}>
          <Fact icon={Fingerprint} label="Directory (tenant)" value={tenant.tenantId} mono />
          <Fact icon={Landmark} label="Subscription" value={tenant.subscriptionId || "—"} mono />
          <Fact icon={Cpu} label="Region" value={tenant.region || "—"} />
          <Fact icon={ShieldCheck} label="Reconciler identity" value={tenant.reconcilerIdentity || "—"} mono />
          <Fact icon={Bot} label="Foundry project" value={tenant.foundryProject || "—"} mono />
          <Fact
            icon={ServerCog}
            label="Installed"
            value={tenant.installedAt ? `${tenant.installedAt} · self-updating` : "Not yet installed"}
          />
        </dl>
        <p className={styles.sovereign}>
          <ShieldCheck size={14} strokeWidth={2.2} aria-hidden />
          Cortex holds no data plane. Models, agents, and knowledge stay in your subscription; the
          reconciler only reports state and pulls desired configuration.
        </p>
      </section>
    </div>
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
        sub: "Deploy the Cortex app into your subscription; its reconciler enrolls over its own managed identity and this turns live.",
      };
  }
}

function buildSteps(x: {
  bound: boolean;
  live: boolean;
  degraded: boolean;
  agentCount: number;
}) {
  const s2: StepState = x.bound ? "done" : "current";
  const s3: StepState = x.live ? "done" : x.bound ? "current" : "pending";
  // Agents only count as "serving" once a live reconciler confirms them — a
  // later step never lands done while an earlier one is still in flight.
  const s4: StepState = x.live && x.agentCount > 0 ? "done" : x.live ? "current" : "pending";
  const agentsNote =
    x.live && x.agentCount > 0
      ? `${x.agentCount} enabled and converged`
      : x.agentCount > 0
        ? `${x.agentCount} enabled · awaiting reconciler`
        : "Enable agents from your catalog";
  return [
    {
      key: "directory",
      icon: Fingerprint,
      title: "Directory connected",
      note: "Signed in with Microsoft Entra",
      state: "done" as StepState,
      tone: "success" as const,
    },
    {
      key: "deployed",
      icon: ServerCog,
      title: "App deployed",
      note: x.bound ? "Reconciler enrolled in your subscription" : "Deploy the managed app to your subscription",
      state: s2,
      tone: "success" as const,
    },
    {
      key: "live",
      icon: ShieldCheck,
      title: "Reconciler live",
      note: x.degraded ? "Heartbeat stale — reconciler unreachable" : "Heartbeating desired vs. actual state",
      state: s3,
      tone: (x.degraded ? "warning" : "success") as "success" | "warning",
    },
    {
      key: "agents",
      icon: Bot,
      title: "Agents serving",
      note: agentsNote,
      state: s4,
      tone: "success" as const,
    },
  ];
}

function Fact({
  icon: Icon,
  label,
  value,
  mono = false,
}: {
  icon: typeof Fingerprint;
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className={styles.fact}>
      <dt className={styles.factLabel}>
        <Icon size={13} strokeWidth={2} aria-hidden />
        {label}
      </dt>
      <dd className={styles.factValue + (mono ? " mono" : "")}>{value}</dd>
    </div>
  );
}
