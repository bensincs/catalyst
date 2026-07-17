"use client";

import {
  ArrowUpRight,
  Bot,
  Cloud,
  Cpu,
  Fingerprint,
  Download,
  Landmark,
  ServerCog,
  ShieldCheck,
} from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/status";
import {
  LIFECYCLE_META,
  type Application,
  type EnabledAgent,
  type Infrastructure,
  type TenantContextInfo,
} from "@/lib/types";
import { InstallStatusChecks, ReconcilerStatus } from "./install-status";
import styles from "./install-view.module.css";

const DEPLOY_URL =
  process.env.NEXT_PUBLIC_CORTEX_DEPLOY_URL ??
  "https://portal.azure.com/#create/Microsoft.Solutions%2FmanagedApplications";

// The compiled delegation ARM template, served as a static asset so the Azure
// portal can fetch it and the customer can download it.
const DELEGATION_TEMPLATE_PATH = "/onboarding/lighthouse-delegation.json";

export function InstallView({
  tenant,
  cortexTenantId,
  cortexPrincipalId,
  infrastructure,
  applications,
  agents,
  now,
}: {
  tenant: TenantContextInfo;
  cortexTenantId: string;
  cortexPrincipalId: string;
  infrastructure: Infrastructure[];
  applications: Application[];
  agents: EnabledAgent[];
  now: number;
}) {
  const lc = LIFECYCLE_META[tenant.lifecycle];
  const bound = tenant.enrollment === "bound";
  const live = tenant.lifecycle === "live";
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

      {/* Live install status — staged checks that read top-to-bottom as the
          install comes online, plus the reconciler heartbeat. */}
      <InstallStatusChecks tenant={tenant} infrastructure={infrastructure} applications={applications} agents={agents} />
      <ReconcilerStatus tenant={tenant} now={now} />

      {/* Infrastructure delegation — how the control plane gets to provision infra */}
      <DelegationSection
        subscriptionId={tenant.subscriptionId}
        region={tenant.region}
        cortexTenantId={cortexTenantId}
        cortexPrincipalId={cortexPrincipalId}
      />

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
function DelegationSection({
  subscriptionId,
  region,
  cortexTenantId,
  cortexPrincipalId,
}: {
  subscriptionId: string;
  region: string;
  cortexTenantId: string;
  cortexPrincipalId: string;
}) {
  const cmd = [
    "az deployment sub create \\",
    `  --subscription ${subscriptionId || "<your-subscription-id>"} --location ${region || "<region>"} \\`,
    "  --template-file lighthouse-delegation.json \\",
    `  -p controlPlaneTenantId=${cortexTenantId} \\`,
    `  -p controlPlanePrincipalId=${cortexPrincipalId}`,
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
        <Fact icon={Fingerprint} label="Cortex tenant (managing)" value={cortexTenantId} mono />
        <Fact icon={ShieldCheck} label="Control-plane principal" value={cortexPrincipalId} mono />
        <Fact icon={Cloud} label="Scope" value="Subscription · Contributor + limited User Access Admin" />
      </dl>

      <p className={styles.delegationStep}>
        One click via the button below, or download the template and run it as a subscription Owner:
      </p>
      <pre className={styles.cmd}>
        <code>{cmd}</code>
      </pre>
      <div className={styles.delegationActions}>
        <Button
          variant="primary"
          icon={ShieldCheck}
          iconRight={ArrowUpRight}
          onClick={() => {
            const override = process.env.NEXT_PUBLIC_CORTEX_DELEGATION_DEPLOY_URL;
            const url =
              override ||
              `https://portal.azure.com/#create/Microsoft.Template/uri/${encodeURIComponent(
                `${window.location.origin}${DELEGATION_TEMPLATE_PATH}`,
              )}`;
            window.open(url, "_blank", "noopener,noreferrer");
          }}
        >
          Deploy to Azure
        </Button>
        <a href={DELEGATION_TEMPLATE_PATH} download className={styles.downloadLink}>
          <Download size={14} strokeWidth={2.2} aria-hidden /> Download template
        </a>
        <span className={styles.delegationActionsHint}>The portal asks for the two values above.</span>
      </div>
      <p className={styles.delegationNote}>
        Least privilege: Contributor to build resources, plus a <em>limited</em> User Access Administrator
        that can only grant the reconciler&apos;s managed identity its Foundry + AKS roles — nothing else, to
        nobody else. Revoke anytime under <em>Service providers → Delegations</em>. Prefer just-in-time? Grant
        the principal via PIM instead of standing access.
      </p>
    </section>
  );
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
