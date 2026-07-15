"use client";

import {
  AlertTriangle,
  ArrowUpRight,
  Bot,
  Check,
  Cloud,
  Cpu,
  Fingerprint,
  Landmark,
  Minus,
  Radio,
  RefreshCw,
  ServerCog,
  ShieldCheck,
  X,
  type LucideIcon,
} from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button } from "@/components/ui/button";
import { StatusBadge, StatusDot } from "@/components/ui/status";
import { formatRelative } from "@/lib/format";
import { LIFECYCLE_META, type ClusterInfo, type Lifecycle, type TenantContextInfo } from "@/lib/types";
import styles from "./install-view.module.css";

/** Aggregate provisioning state of a tenant's enabled deployments that carry
 *  Azure infra (deployed by the control plane via Lighthouse). */
export interface InfraSummary {
  total: number;
  ready: number;
  provisioning: number;
  failed: number;
}

const DEPLOY_URL =
  process.env.NEXT_PUBLIC_CORTEX_DEPLOY_URL ??
  "https://portal.azure.com/#create/Microsoft.Solutions%2FmanagedApplications";

// Published by Cortex — the managing tenant + control-plane service principal a
// customer delegates their cortex-infra resource group to via Azure Lighthouse.
const CORTEX_TENANT_ID = process.env.NEXT_PUBLIC_CORTEX_TENANT_ID ?? "<your Cortex tenant id>";
const CORTEX_SP_OBJECT_ID =
  process.env.NEXT_PUBLIC_CORTEX_SP_OBJECT_ID ?? "<Cortex control-plane service principal object id>";

export function InstallView({
  tenant,
  agentCount,
  infra,
  now,
}: {
  tenant: TenantContextInfo;
  agentCount: number;
  infra: InfraSummary;
  now: number;
}) {
  const lc = LIFECYCLE_META[tenant.lifecycle];
  const recon = reconStatus(tenant.lifecycle);
  const bound = tenant.enrollment === "bound";
  const live = tenant.lifecycle === "live";

  const checks = buildChecks({
    bound,
    live,
    degraded: tenant.lifecycle === "degraded",
    agentCount,
    cluster: tenant.cluster,
    infra,
  });
  const deploy = () => window.open(DEPLOY_URL, "_blank", "noopener,noreferrer");

  return (
    <div>
      <PageHeader
        title="Install"
        description="One Azure Lighthouse delegation is the whole install — Cortex then provisions the reconciler, Foundry, cluster, and app infrastructure into your subscription. Everything runs in your tenant."
        meta={<StatusBadge tone={lc.tone} label={lc.label} pulse={live} />}
        actions={
          bound ? (
            <Button variant="secondary" icon={ArrowUpRight} onClick={deploy}>
              Manage in Azure
            </Button>
          ) : (
            <Button variant="primary" icon={ShieldCheck} iconRight={ArrowUpRight} onClick={deploy}>
              Deploy delegation
            </Button>
          )
        }
      />

      {/* Staged system checks — reads top to bottom as the install comes online */}
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

      {/* Infrastructure delegation — how the control plane gets to provision infra */}
      <DelegationSection subscriptionId={tenant.subscriptionId} region={tenant.region} />

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
          Your <strong>data plane</strong> stays yours — models, agents, and knowledge run under
          identities Cortex creates <em>in your tenant</em>. Cortex holds delegated management of this
          subscription (revocable anytime), but no standing access to your data.
        </p>
      </section>
    </div>
  );
}

// DelegationSection is the whole onboarding: one Azure Lighthouse delegation that
// hands provisioning of the entire footprint to the Cortex control plane.
function DelegationSection({ subscriptionId, region }: { subscriptionId: string; region: string }) {
  const cmd = [
    "az deployment sub create \\",
    `  --subscription ${subscriptionId || "<your-subscription-id>"} --location ${region || "<region>"} \\`,
    "  --template-file onboarding/lighthouse-delegation.bicep \\",
    `  -p controlPlaneTenantId=${CORTEX_TENANT_ID} \\`,
    `  -p controlPlanePrincipalId=${CORTEX_SP_OBJECT_ID}`,
  ].join("\n");

  return (
    <section aria-label="Lighthouse delegation" className={styles.manifestWrap}>
      <h2 className={styles.sectionTitle}>Onboarding — delegate to Cortex (Azure Lighthouse)</h2>
      <p className={styles.delegationLead}>
        This one delegation <strong>is the entire install</strong>. Once it lands, the Cortex control plane
        provisions the reconciler, Foundry, the cluster, and every deployment&apos;s infrastructure into your
        subscription — cross-tenant, hands-off. You run nothing else. Publish it as a Marketplace managed
        service offer, or run the command below.
      </p>

      <dl className={styles.manifest}>
        <Fact icon={Fingerprint} label="Cortex tenant (managing)" value={CORTEX_TENANT_ID} mono />
        <Fact icon={ShieldCheck} label="Control-plane principal" value={CORTEX_SP_OBJECT_ID} mono />
        <Fact icon={Cloud} label="Scope" value="Subscription · Contributor + limited User Access Admin" />
      </dl>

      <p className={styles.delegationStep}>Run as a subscription Owner:</p>
      <pre className={styles.cmd}>
        <code>{cmd}</code>
      </pre>
      <p className={styles.delegationNote}>
        Least privilege: Contributor to build resources, plus a <em>limited</em> User Access Administrator
        that can only grant the reconciler&apos;s managed identity its Foundry + AKS roles — nothing else, to
        nobody else. Revoke anytime under <em>Service providers → Delegations</em>. Prefer just-in-time? Grant
        the principal via PIM instead of standing access.
      </p>
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
        sub: "Deploy the Cortex app into your subscription; its reconciler enrolls over its own managed identity and this turns live.",
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
