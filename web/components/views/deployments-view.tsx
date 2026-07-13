"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Rocket, Plus, Trash2, Boxes } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/modal";
import { Field, TextInput, Textarea } from "@/components/ui/form";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import { createApplication, deleteApplication, type ActionResult } from "@/lib/actions";
import type { Application, ClusterInfo, HealthMeta } from "@/lib/types";
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

function syncMeta(s: string): { tone: Tone; label: string } {
  switch (s.toLowerCase()) {
    case "synced":
      return { tone: "success", label: "Synced" };
    case "outofsync":
      return { tone: "warning", label: "OutOfSync" };
    case "pending":
      return { tone: "info", label: "Pending" };
    default:
      return { tone: "neutral", label: s || "Unknown" };
  }
}

function healthMeta(h: string): { tone: Tone; label: string } {
  switch (h.toLowerCase()) {
    case "healthy":
      return { tone: "success", label: "Healthy" };
    case "progressing":
      return { tone: "info", label: "Progressing" };
    case "pending":
      return { tone: "info", label: "Pending" };
    case "degraded":
    case "missing":
      return { tone: "danger", label: h };
    default:
      return { tone: "neutral", label: h || "Unknown" };
  }
}

export function DeploymentsView({
  cluster,
  applications,
}: {
  cluster: ClusterInfo;
  applications: Application[];
}) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();
  const [modal, setModal] = useState(false);

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

  const cm = clusterMeta(cluster);

  return (
    <div>
      <PageHeader
        title="Deployments"
        description="Deploy Helm charts into your cluster. Each becomes an Argo CD Application the reconciler stamps in and Argo keeps converged."
        actions={
          <Button variant="primary" icon={Plus} onClick={() => setModal(true)}>
            New deployment
          </Button>
        }
      />

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
        {cluster.gatewayIP && (
          <span className={styles.chip}>
            gateway <span className="mono">{cluster.gatewayIP}</span>
          </span>
        )}
        {cluster.detail && cluster.phase !== "ready" && <span className={styles.count}>{cluster.detail}</span>}
      </div>

      {applications.length === 0 ? (
        <div className={styles.panelEmpty}>
          <EmptyState
            icon={Boxes}
            title="No deployments yet"
            description={
              cluster.phase === "ready"
                ? "Add a Helm chart — repo, chart, and version — and Argo CD installs it into your cluster."
                : "Your cluster isn't ready yet. Once the reconciler has bootstrapped Argo CD, add a Helm chart here."
            }
            action={
              <Button variant="primary" icon={Plus} onClick={() => setModal(true)}>
                New deployment
              </Button>
            }
          />
        </div>
      ) : (
        <ul className={styles.list} role="list">
          {applications.map((a) => {
            const sm = syncMeta(a.syncStatus);
            const hm = healthMeta(a.healthStatus);
            return (
              <li key={a.id} className={styles.row}>
                <div className={styles.rowIcon} aria-hidden>
                  <Rocket size={17} strokeWidth={2} />
                </div>
                <div className={styles.rowMain}>
                  <div className={styles.rowTop}>
                    <span className={styles.rowName}>{a.name}</span>
                    <StatusBadge tone={hm.tone} label={hm.label} variant="soft" pulse={a.healthStatus.toLowerCase() === "progressing"} />
                    <StatusBadge tone={sm.tone} label={sm.label} variant="soft" />
                  </div>
                  <div className={styles.chips}>
                    <span className={styles.chip}>
                      <span className="mono">
                        {a.chart}
                        {a.targetRevision ? `@${a.targetRevision}` : ""}
                      </span>
                    </span>
                    <span className={styles.chip}>
                      ns <span className="mono">{a.namespace}</span>
                    </span>
                    <span className={styles.chip} title={a.repoURL}>
                      <span className="mono">{a.repoURL}</span>
                    </span>
                  </div>
                </div>
                <div className={styles.rowActions}>
                  <Button
                    size="sm"
                    variant="danger"
                    icon={Trash2}
                    loading={pending}
                    onClick={() => runAction(() => deleteApplication(a.id), `Removed ${a.name}`)}
                  >
                    Remove
                  </Button>
                </div>
              </li>
            );
          })}
        </ul>
      )}

      {modal && (
        <DeploymentModal
          pending={pending}
          onClose={() => setModal(false)}
          onSubmit={(input) =>
            runAction(() => createApplication(input), `Deploying ${input.name}`, () => setModal(false))
          }
        />
      )}
    </div>
  );
}

function DeploymentModal({
  pending,
  onClose,
  onSubmit,
}: {
  pending: boolean;
  onClose: () => void;
  onSubmit: (input: {
    name: string;
    namespace: string;
    repoURL: string;
    chart: string;
    targetRevision: string;
    values: string;
  }) => void;
}) {
  const [name, setName] = useState("");
  const [repoURL, setRepoURL] = useState("");
  const [chart, setChart] = useState("");
  const [targetRevision, setTargetRevision] = useState("");
  const [namespace, setNamespace] = useState("");
  const [values, setValues] = useState("");

  const valid = name.trim().length >= 2 && repoURL.trim() !== "" && chart.trim() !== "";

  const submit = () =>
    onSubmit({
      name: name.trim(),
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
      title="New deployment"
      description="A Helm chart to install into your cluster via Argo CD."
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={pending} disabled={!valid} onClick={submit}>
            Deploy
          </Button>
        </>
      }
    >
      <Field label="Name" htmlFor="dep-name" hint="A short name — becomes the Argo Application + release.">
        <TextInput id="dep-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="ingress-nginx" autoFocus />
      </Field>
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
    </Modal>
  );
}
