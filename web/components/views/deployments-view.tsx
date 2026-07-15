"use client";

import { useMemo, useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Rocket, Plus, Trash2, Pencil, Power, Cloud, GitBranch, Bot } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/modal";
import { Field, TextInput, Textarea } from "@/components/ui/form";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import { WiringEditor } from "./wiring-editor";
import {
  createApplication,
  updateApplication,
  deleteApplication,
  enableDeployment,
  disableDeployment,
  type ActionResult,
} from "@/lib/actions";
import {
  HEALTH_META,
  type Application,
  type ClusterInfo,
  type DepOption,
  type HealthMeta,
  type Role,
  type WireLink,
} from "@/lib/types";
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
  depOptions = [],
}: {
  role: Role;
  applications: Application[];
  cluster?: ClusterInfo;
  depOptions?: DepOption[];
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
                    {!platform && a.enabled && a.waiting && (
                      <StatusBadge tone="warning" label="Waiting on deps" variant="soft" />
                    )}
                    {!platform && a.enabled && a.infraState && (
                      <StatusBadge
                        tone={a.infraState === "ready" ? "success" : a.infraState === "failed" ? "danger" : "info"}
                        label={`Infra ${a.infraState}`}
                        variant="soft"
                        pulse={a.infraState === "provisioning"}
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
          depOptions={depOptions.filter((d) => modal.mode !== "edit" || d.id !== modal.app.id)}
          pending={pending}
          onClose={() => setModal(null)}
          onSubmit={(input) =>
            modal.mode === "edit"
              ? runAction(() => updateApplication(modal.app.id, input), `Updated ${input.name}`, () => setModal(null))
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
      {app.bicepModule ? (
        <span className={styles.chip} title={`Azure infra: ${app.bicepModule}`}>
          <Cloud size={12} strokeWidth={2.2} /> Azure infra
          {app.wiring.length > 0 ? ` · ${app.wiring.length} wired` : ""}
        </span>
      ) : null}
      {app.dependsOn.length > 0 && (
        <span className={styles.chip} title="Deploys after its dependencies">
          <GitBranch size={12} strokeWidth={2.2} /> {app.dependsOn.length} dep
          {app.dependsOn.length === 1 ? "" : "s"}
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
  depOptions,
  pending,
  onClose,
  onSubmit,
}: {
  app: Application | null;
  depOptions: DepOption[];
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
    bicepModule: string;
    bicepParams: Record<string, unknown>;
    wiring: WireLink[];
    dependsOn: string[];
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
  const [bicepModule, setBicepModule] = useState(app?.bicepModule ?? "");
  const [paramsText, setParamsText] = useState(
    app?.bicepParams && Object.keys(app.bicepParams).length
      ? JSON.stringify(app.bicepParams, null, 2)
      : "",
  );
  const [wiring, setWiring] = useState<WireLink[]>(app?.wiring ?? []);
  const [dependsOn, setDependsOn] = useState<string[]>(app?.dependsOn ?? []);

  // Params are a JSON object baked into the module; validate before enabling save.
  const params = useMemo((): { ok: boolean; value: Record<string, unknown> } => {
    const t = paramsText.trim();
    if (t === "") return { ok: true, value: {} };
    try {
      const v = JSON.parse(t);
      if (v && typeof v === "object" && !Array.isArray(v)) return { ok: true, value: v as Record<string, unknown> };
    } catch {
      /* fall through */
    }
    return { ok: false, value: {} };
  }, [paramsText]);

  const valid = name.trim().length >= 2 && repoURL.trim() !== "" && chart.trim() !== "" && params.ok;

  const submit = () =>
    onSubmit({
      name: name.trim(),
      description: description.trim(),
      namespace: namespace.trim() || "default",
      repoURL: repoURL.trim(),
      chart: chart.trim(),
      targetRevision: targetRevision.trim(),
      values,
      bicepModule: bicepModule.trim(),
      bicepParams: params.value,
      wiring,
      dependsOn,
    });

  const toggleDep = (id: string) =>
    setDependsOn((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]));

  return (
    <Modal
      open
      onClose={onClose}
      width={960}
      title={editing ? "Edit deployment" : "New deployment"}
      description="A deployable Helm chart, optionally backed by Azure infra (Bicep). Wire the infra's outputs into the chart, and set what it must deploy after."
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={pending} disabled={!valid} onClick={submit}>
            {editing ? "Save deployment" : "Create deployment"}
          </Button>
        </>
      }
    >
      <div className={styles.grid2}>
        <Field label="Name" htmlFor="dep-name" hint="Becomes the Argo Application + release.">
          <TextInput id="dep-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="ingress-nginx" autoFocus />
        </Field>
        <Field label="Namespace" htmlFor="dep-ns" hint="Destination namespace (created if missing).">
          <TextInput id="dep-ns" value={namespace} onChange={(e) => setNamespace(e.target.value)} placeholder="default" spellCheck={false} />
        </Field>
      </div>
      <Field label="Description" htmlFor="dep-desc">
        <Textarea id="dep-desc" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="What this deployment is for." />
      </Field>

      <p className={styles.groupLabel}>Helm chart</p>
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
      <Field label="Values (YAML)" htmlFor="dep-values" hint="Base Helm values — wired Bicep outputs are merged in on deploy.">
        <Textarea id="dep-values" value={values} onChange={(e) => setValues(e.target.value)} spellCheck={false} placeholder={"replicaCount: 2"} />
      </Field>

      <p className={styles.groupLabel}>Azure infrastructure (Bicep)</p>
      <Field
        label="Bicep module reference"
        htmlFor="dep-bicep"
        hint="An OCI reference to a published Bicep module — provisioned in the tenant's resource group before the chart. Its outputs resolve on save."
      >
        <TextInput
          id="dep-bicep"
          value={bicepModule}
          onChange={(e) => setBicepModule(e.target.value)}
          spellCheck={false}
          placeholder="br:cortexcpacrzo7yflmq.azurecr.io/bicep/postgres:1.2.0"
        />
      </Field>

      {bicepModule.trim() !== "" && (
        <>
          <Field
            label="Module parameters (JSON)"
            htmlFor="dep-params"
            hint="Author the module's inputs as a JSON object — baked into the template on save. Required params (e.g. name) must be set or the module won't resolve."
          >
            <Textarea
              id="dep-params"
              value={paramsText}
              onChange={(e) => setParamsText(e.target.value)}
              spellCheck={false}
              className={styles.codeArea}
              placeholder={'{\n  "name": "cortex-db",\n  "skuName": "Standard_B1ms",\n  "tier": "Burstable"\n}'}
            />
            {paramsText.trim() !== "" && !params.ok && (
              <p className={styles.paramsError}>Must be a JSON object.</p>
            )}
          </Field>

          <p className={styles.groupLabel}>Wire outputs → Helm values</p>
          <WiringEditor outputs={app?.bicepOutputs ?? []} wiring={wiring} onChange={setWiring} />
        </>
      )}

      {depOptions.length > 0 && (
        <>
          <p className={styles.groupLabel}>Deploy after</p>
          <p className={styles.groupHint}>Dependencies converge first — Argo sync-waves order the deploy.</p>
          <div className={styles.depGrid}>
            {depOptions.map((o) => {
              const on = dependsOn.includes(o.id);
              return (
                <button
                  type="button"
                  key={o.id}
                  className={styles.depChip}
                  data-on={on || undefined}
                  onClick={() => toggleDep(o.id)}
                >
                  {o.kind === "agent" ? <Bot size={14} strokeWidth={2.2} /> : <Rocket size={14} strokeWidth={2.2} />}
                  <span>{o.name}</span>
                </button>
              );
            })}
          </div>
        </>
      )}
    </Modal>
  );
}
