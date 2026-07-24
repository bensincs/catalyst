"use client";

import { useTransition } from "react";
import { useRouter } from "next/navigation";
import { Rocket, Plus, Trash2, Pencil, Power, GitBranch } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button, ButtonLink } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import { deleteApplication, enableDeployment, disableDeployment, type ActionResult } from "@/lib/actions";
import {
  type Application,
  type ClusterInfo,
  type HealthMeta,
  type Role,
} from "@/lib/types";
import { applicationStatus } from "@/lib/status";
import styles from "./memory-stores-view.module.css";

type Tone = HealthMeta["tone"];

function clusterMeta(c: ClusterInfo): { tone: Tone; label: string } {
  switch (c.phase) {
    case "ready":
      return { tone: "success", label: "Cluster ready" };
    case "provisioning":
      return { tone: "info", label: "Cluster provisioning" };
    case "unreachable":
      return { tone: "danger", label: "Cluster unreachable" };
    default:
      return { tone: "neutral", label: "No cluster" };
  }
}

export function DeploymentsView({
  role,
  applications,
  cluster,
}: {
  role: Role;
  applications: Application[];
  cluster?: ClusterInfo;
}) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();
  const platform = role === "platform";

  const runAction = (fn: () => Promise<ActionResult>, success: string) => {
    start(async () => {
      const res = await fn();
      if (res.ok) {
        toast({ title: success, tone: "success" });
        router.refresh();
      } else {
        toast({ title: "Couldn't complete that", description: res.error, tone: "danger" });
      }
    });
  };

  const manageable = (a: Application) => (platform ? a.owner === "" : a.owned);
  const scope = (a: Application): { label: string; tone: "success" | "info" | "neutral" } =>
    a.owned
      ? { label: "Yours", tone: "success" }
      : a.platform
        ? { label: platform ? "Platform" : "Entitled", tone: "info" }
        : { label: "Tenant", tone: "neutral" };

  return (
    <div>
      <PageHeader
        title="Deployments"
        description={
          platform
            ? "Author deployable Helm charts, entitle tenants to them from a tenant's page, and let each tenant enable them into its own cluster."
            : "Create your own deployments or enable ones you're entitled to — enabling stamps the chart into your cluster as an Argo CD Application and keeps it converged."
        }
        actions={
          <ButtonLink href="/deployments/new" variant="primary" icon={Plus}>
            New deployment
          </ButtonLink>
        }
      />

      {!platform && cluster && <ClusterStrip cluster={cluster} />}

      {applications.length === 0 ? (
        <div className={styles.panelEmpty}>
          <EmptyState
            icon={Rocket}
            title="No deployments yet"
            description={
              platform
                ? "Author a deployable Helm chart, then entitle tenants to it from their page."
                : "Create a deployment or enable one you're entitled to — it installs into your cluster via Argo CD."
            }
            action={
              <ButtonLink href="/deployments/new" variant="primary" icon={Plus}>
                New deployment
              </ButtonLink>
            }
          />
        </div>
      ) : (
        <ul className={styles.list} role="list">
          {applications.map((a) => {
            const sc = scope(a);
            return (
              <li key={a.id} className={styles.row}>
                <div className={styles.rowIcon} aria-hidden>
                  <Rocket size={17} strokeWidth={2} />
                </div>
                <div className={styles.rowMain}>
                  <div className={styles.rowTop}>
                    <span className={styles.rowName}>{a.name}</span>
                    <StatusBadge tone={sc.tone} label={sc.label} variant="soft" />
                    {!platform && a.enabled && (
                      <StatusBadge
                        tone={applicationStatus(a).tone}
                        label={applicationStatus(a).label}
                        variant="soft"
                        pulse={applicationStatus(a).pulse}
                      />
                    )}
                    {platform && a.owner !== "" && a.ownerName && (
                      <span className={styles.count}>owned by {a.ownerName}</span>
                    )}
                  </div>
                  {a.description && <p className={styles.rowDesc}>{a.description}</p>}
                  <DefinitionChips app={a} showRuntime={!platform && !!a.enabled} />
                </div>
                {(manageable(a) || (!platform && (a.owned || a.entitled))) && (
                  <div className={styles.rowActions}>
                    {!platform &&
                      (a.owned || a.entitled) &&
                      (a.enabled ? (
                        <Button
                          size="sm"
                          icon={Power}
                          loading={pending}
                          onClick={() => runAction(() => disableDeployment(a.id), `Disabled ${a.name}`)}
                        >
                          Disable
                        </Button>
                      ) : (
                        <Button
                          size="sm"
                          variant="primary"
                          icon={Power}
                          loading={pending}
                          onClick={() => runAction(() => enableDeployment(a.id), `Deploying ${a.name}`)}
                        >
                          Enable
                        </Button>
                      ))}
                    {manageable(a) && (
                      <>
                        <ButtonLink size="sm" variant="ghost" icon={Pencil} href={`/deployments/${a.id}/edit`}>
                          Edit
                        </ButtonLink>
                        <Button
                          size="sm"
                          variant="danger-ghost"
                          icon={Trash2}
                          loading={pending}
                          onClick={() => runAction(() => deleteApplication(a.id), `Deleted ${a.name}`)}
                        >
                          Delete
                        </Button>
                      </>
                    )}
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

function DefinitionChips({ app, showRuntime }: { app: Application; showRuntime: boolean }) {
  return (
    <div className={styles.chips}>
      <span className={styles.chip} title="Helm chart and version">
        <span className="mono">
          {app.chart}
          {app.targetRevision ? `@${app.targetRevision}` : ""}
        </span>
      </span>
      <span className={styles.chip}>
        ns <span className="mono">{app.namespace}</span>
      </span>
      {app.dependencies.length > 0 && (
        <span className={styles.chip} title="Typed dependencies that converge first">
          <GitBranch size={12} strokeWidth={2.2} /> {app.dependencies.length} dep
          {app.dependencies.length === 1 ? "" : "s"}
        </span>
      )}
      {app.wiring.length > 0 && (
        <span className={styles.chip} title="Infrastructure outputs wired into Helm values">
          {app.wiring.length} wired
        </span>
      )}
      {showRuntime && app.syncStatus && (
        <span className={styles.chip} title="Argo CD sync / health">
          {app.syncStatus}
          {app.healthStatus ? ` · ${app.healthStatus}` : ""}
        </span>
      )}
    </div>
  );
}

function ClusterStrip({ cluster }: { cluster: ClusterInfo }) {
  const cm = clusterMeta(cluster);
  return (
    <div className={styles.chips} style={{ marginBottom: 16 }}>
      <StatusBadge tone={cm.tone} label={cm.label} variant="soft" pulse={cluster.phase === "provisioning"} />
      {cluster.name && (
        <span className={styles.chip}>
          <span className="mono">{cluster.name}</span>
        </span>
      )}
      {cluster.kubernetesVersion && (
        <span className={styles.chip}>
          k8s <span className="mono">{cluster.kubernetesVersion}</span>
        </span>
      )}
      {cluster.nodeCount > 0 && (
        <span className={styles.chip}>
          {cluster.nodeCount} node{cluster.nodeCount === 1 ? "" : "s"}
        </span>
      )}
      <span className={styles.chip} data-off={!cluster.argoInstalled}>
        Argo CD
      </span>
      <span className={styles.chip} data-off={!cluster.ingressInstalled} title="Application Gateway for Containers (Gateway API)">
        Gateway
      </span>
      {cluster.gatewayIP && (
        <span className={styles.chip}>
          gateway <span className="mono">{cluster.gatewayIP}</span>
        </span>
      )}
      {cluster.detail && cluster.phase !== "ready" && <span className={styles.count}>{cluster.detail}</span>}
    </div>
  );
}
