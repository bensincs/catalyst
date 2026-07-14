"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Rocket, Plus, Trash2, Pencil, Power } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/modal";
import { Field, TextInput, Textarea } from "@/components/ui/form";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import {
  createApplication,
  updateApplication,
  deleteApplication,
  enableDeployment,
  disableDeployment,
  type ActionResult,
} from "@/lib/actions";
import { HEALTH_META, type Application, type ClusterInfo, type HealthMeta, type Role } from "@/lib/types";
import styles from "./memory-stores-view.module.css";

type Tone = HealthMeta["tone"];
type ModalState = { mode: "new" } | { mode: "edit"; app: Application } | null;

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
  const [modal, setModal] = useState<ModalState>(null);

  const runAction = (fn: () => Promise<ActionResult>, success: string, onDone?: () => void) => {
    start(async () => {
      const res = await fn();
      if (res.ok) {
        toast({ title: success, tone: "success" });
        onDone?.();
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
          <Button variant="primary" icon={Plus} onClick={() => setModal({ mode: "new" })}>
            New deployment
          </Button>
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
              <Button variant="primary" icon={Plus} onClick={() => setModal({ mode: "new" })}>
                New deployment
              </Button>
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
                    {!platform && a.enabled && a.health && (
                      <StatusBadge
                        tone={HEALTH_META[a.health].tone}
                        label={HEALTH_META[a.health].label}
                        variant="soft"
                        pulse={a.health === "reconciling"}
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
                        <Button size="sm" icon={Pencil} onClick={() => setModal({ mode: "edit", app: a })}>
                          Edit
                        </Button>
                        <Button
                          size="sm"
                          variant="danger"
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

      {modal && (
        <DeploymentModal
          key={modal.mode === "edit" ? modal.app.id : "new"}
          app={modal.mode === "edit" ? modal.app : null}
          pending={pending}
          onClose={() => setModal(null)}
          onSubmit={(input) =>
            modal.mode === "edit"
              ? runAction(
                  () => updateApplication(modal.app.id, { name: input.name, description: input.description }),
                  `Updated ${input.name}`,
                  () => setModal(null),
                )
              : runAction(() => createApplication(input), `Created ${input.name}`, () => setModal(null))
          }
        />
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
      <span className={styles.chip} title={app.repoURL}>
        <span className="mono">{app.repoURL}</span>
      </span>
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
      <span className={styles.chip} data-off={!cluster.meshInstalled}>
        Istio
      </span>
      <span className={styles.chip} data-off={!cluster.mtlsStrict} title="Mesh-wide STRICT mutual TLS">
        mTLS
      </span>
      <span className={styles.chip} data-off={!cluster.otelInstalled} title="Grafana Alloy OpenTelemetry collector">
        OTel
      </span>
      <span
        className={styles.chip}
        data-off={!cluster.ingressIssuer}
        title={cluster.ingressIssuer ? `Ingress requires an Entra token from ${cluster.ingressIssuer}` : undefined}
      >
        Entra auth
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

function DeploymentModal({
  app,
  pending,
  onClose,
  onSubmit,
}: {
  app: Application | null;
  pending: boolean;
  onClose: () => void;
  onSubmit: (input: {
    name: string;
    description: string;
    namespace: string;
    repoURL: string;
    chart: string;
    targetRevision: string;
    values: string;
  }) => void;
}) {
  const editing = app !== null;
  const [name, setName] = useState(app?.name ?? "");
  const [description, setDescription] = useState(app?.description ?? "");
  const [repoURL, setRepoURL] = useState(app?.repoURL ?? "");
  const [chart, setChart] = useState(app?.chart ?? "");
  const [targetRevision, setTargetRevision] = useState(app?.targetRevision ?? "");
  const [namespace, setNamespace] = useState(app?.namespace ?? "");
  const [values, setValues] = useState(app?.values ?? "");

  const valid = editing
    ? name.trim().length >= 2
    : name.trim().length >= 2 && repoURL.trim() !== "" && chart.trim() !== "";

  const submit = () =>
    onSubmit({
      name: name.trim(),
      description: description.trim(),
      namespace: namespace.trim() || "default",
      repoURL: repoURL.trim(),
      chart: chart.trim(),
      targetRevision: targetRevision.trim(),
      values,
    });

  return (
    <Modal
      open
      onClose={onClose}
      title={editing ? "Edit deployment" : "New deployment"}
      description={
        editing
          ? "Rename or redescribe this deployment. Its chart, repo, values, and namespace are fixed once created."
          : "A deployable Helm chart. Tenants enable it to install it into their own cluster via Argo CD."
      }
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={pending} disabled={!valid} onClick={submit}>
            {editing ? "Save" : "Create deployment"}
          </Button>
        </>
      }
    >
      <Field label="Name" htmlFor="dep-name" hint="A short name — becomes the Argo Application + release.">
        <TextInput id="dep-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="ingress-nginx" autoFocus />
      </Field>
      <Field label="Description" htmlFor="dep-desc">
        <Textarea
          id="dep-desc"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="What this deployment is for."
        />
      </Field>

      {editing ? (
        <ReadonlyDefinition app={app} />
      ) : (
        <>
          <datalist id="dep-repos">
            <option value="https://charts.bitnami.com/bitnami" />
            <option value="https://kubernetes.github.io/ingress-nginx" />
            <option value="https://prometheus-community.github.io/helm-charts" />
          </datalist>
          <Field label="Helm repo / OCI URL" htmlFor="dep-repo" hint="A Helm repository (https://…) or OCI registry (oci://…).">
            <TextInput
              id="dep-repo"
              list="dep-repos"
              value={repoURL}
              onChange={(e) => setRepoURL(e.target.value)}
              placeholder="https://charts.bitnami.com/bitnami"
              spellCheck={false}
            />
          </Field>
          <div className={styles.grid2}>
            <Field label="Chart" htmlFor="dep-chart">
              <TextInput id="dep-chart" value={chart} onChange={(e) => setChart(e.target.value)} placeholder="nginx" spellCheck={false} />
            </Field>
            <Field label="Version" htmlFor="dep-ver" hint="Chart version (blank = latest).">
              <TextInput id="dep-ver" value={targetRevision} onChange={(e) => setTargetRevision(e.target.value)} placeholder="15.14.0" spellCheck={false} />
            </Field>
          </div>
          <Field label="Namespace" htmlFor="dep-ns" hint="Destination namespace (created if missing).">
            <TextInput id="dep-ns" value={namespace} onChange={(e) => setNamespace(e.target.value)} placeholder="default" spellCheck={false} />
          </Field>
          <Field label="Values (YAML)" htmlFor="dep-values" hint="Optional Helm values overrides.">
            <Textarea id="dep-values" value={values} onChange={(e) => setValues(e.target.value)} spellCheck={false} placeholder={"replicaCount: 2"} />
          </Field>
        </>
      )}
    </Modal>
  );
}

function ReadonlyDefinition({ app }: { app: Application }) {
  return (
    <Field label="Definition">
      <div className={styles.readonlyDef}>
        <div className={styles.defFact}>
          <span className={styles.defFactKey}>Chart</span>
          <span className={`${styles.defFactVal} mono`}>
            {app.chart}
            {app.targetRevision ? `@${app.targetRevision}` : ""}
          </span>
        </div>
        <div className={styles.defFact}>
          <span className={styles.defFactKey}>Repo</span>
          <span className={`${styles.defFactVal} mono`}>{app.repoURL}</span>
        </div>
        <div className={styles.defFact}>
          <span className={styles.defFactKey}>Namespace</span>
          <span className={`${styles.defFactVal} mono`}>{app.namespace}</span>
        </div>
      </div>
      <p className={styles.readonlyNote}>
        Chart, repo, values, and namespace are fixed once created. To change them, create a new deployment.
      </p>
    </Field>
  );
}
