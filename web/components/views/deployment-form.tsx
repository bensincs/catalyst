"use client";

import { useEffect, useState, useTransition, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { ArrowLeft, Bot, Boxes, Cable, Cloud, GitBranch, Package, Rocket, SlidersHorizontal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Field, TextInput, Textarea } from "@/components/ui/form";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import { WiringCanvas } from "./wiring-canvas";
import {
  createApplication,
  updateApplication,
  inspectModule,
  inspectChart,
  type ActionResult,
} from "@/lib/actions";
import { coerce, mapToYaml, toText, yamlToMap } from "@/lib/values";
import type {
  Application,
  BicepOutputSpec,
  BicepParamSpec,
  ClusterInfo,
  DepOption,
  Role,
  WireLink,
} from "@/lib/types";
import styles from "./deployment-form.module.css";

type Obj = Record<string, unknown>;

// Dotted leaf paths of a chart's default values — the Helm value-path suggestions.
function flattenPaths(obj: Obj, base = ""): string[] {
  const out: string[] = [];
  for (const k of Object.keys(obj)) {
    const path = base ? `${base}.${k}` : k;
    const v = obj[k];
    if (v && typeof v === "object" && !Array.isArray(v) && Object.keys(v as object).length > 0) {
      out.push(...flattenPaths(v as Obj, path));
    } else {
      out.push(path);
    }
  }
  return out;
}

export function DeploymentForm({
  role,
  app,
  depOptions = [],
  cluster,
}: {
  role: Role;
  app?: Application;
  depOptions?: DepOption[];
  cluster?: ClusterInfo;
}) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();
  const editing = app !== undefined;

  const [name, setName] = useState(app?.name ?? "");
  const [description, setDescription] = useState(app?.description ?? "");
  const [namespace, setNamespace] = useState(app?.namespace ?? "");
  const [repoURL, setRepoURL] = useState(app?.repoURL ?? "");
  const [chart, setChart] = useState(app?.chart ?? "");
  const [targetRevision, setTargetRevision] = useState(app?.targetRevision ?? "");
  const [bicepModule, setBicepModule] = useState(app?.bicepModule ?? "");
  const [values, setValues] = useState(app?.values ?? "");
  const [paramValues, setParamValues] = useState<Obj>(app?.bicepParams ?? {});
  const [wiring, setWiring] = useState<WireLink[]>(app?.wiring ?? []);
  const [dependsOn, setDependsOn] = useState<string[]>(app?.dependsOn ?? []);

  // Module inspection → typed inputs + wireable outputs.
  const [inspect, setInspect] = useState<{
    loading: boolean;
    resolved: boolean;
    params: BicepParamSpec[];
    outputs: BicepOutputSpec[];
    error?: string;
  }>({ loading: false, resolved: false, params: [], outputs: [] });

  // Chart inspection → Helm value-path suggestions for the wiring canvas.
  const [helmPaths, setHelmPaths] = useState<string[]>([]);
  const [chartLoading, setChartLoading] = useState(false);

  useEffect(() => {
    const ref = bicepModule.trim();
    if (ref === "") {
      setInspect({ loading: false, resolved: false, params: [], outputs: [] });
      return;
    }
    let cancelled = false;
    setInspect((s) => ({ ...s, loading: true, error: undefined }));
    const t = setTimeout(async () => {
      const r = await inspectModule(ref);
      if (cancelled) return;
      if (r.ok) setInspect({ loading: false, resolved: r.resolved, params: r.params, outputs: r.outputs });
      else setInspect({ loading: false, resolved: false, params: [], outputs: [], error: r.error });
    }, 500);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
  }, [bicepModule]);

  useEffect(() => {
    const repo = repoURL.trim();
    const c = chart.trim();
    if (repo === "" || c === "") {
      setHelmPaths([]);
      return;
    }
    let cancelled = false;
    setChartLoading(true);
    const t = setTimeout(async () => {
      const r = await inspectChart(repo, c, targetRevision.trim());
      if (cancelled) return;
      setChartLoading(false);
      setHelmPaths(r.ok && r.resolved && r.iface ? flattenPaths(r.iface.defaults) : []);
    }, 600);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
  }, [repoURL, chart, targetRevision]);

  const liveOutputs = inspect.outputs.length > 0 ? inspect.outputs.map((o) => o.name) : (app?.bicepOutputs ?? []);
  const paramNames = inspect.params.map((p) => p.name);

  // Seed the wiring boards from the app being edited (once; the boards own state
  // after mount): Helm static values + wired outputs, and Bicep static inputs.
  const helmInitialStatic = yamlToMap(app?.values ?? "");
  const helmInitialWired = Object.fromEntries((app?.wiring ?? []).map((w) => [w.helmPath, w.bicepOutput]));
  const bicepInitialStatic = Object.fromEntries(Object.entries(app?.bicepParams ?? {}).map(([k, v]) => [k, toText(v)]));

  const hasChart = repoURL.trim() !== "" && chart.trim() !== "";
  const hasModule = bicepModule.trim() !== "";
  const valid = name.trim().length >= 2 && hasChart;

  const toggleDep = (id: string) =>
    setDependsOn((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]));

  const submit = () => {
    const input = {
      name: name.trim(),
      description: description.trim(),
      namespace: namespace.trim() || "default",
      repoURL: repoURL.trim(),
      chart: chart.trim(),
      targetRevision: targetRevision.trim(),
      values,
      bicepModule: bicepModule.trim(),
      bicepParams: paramValues,
      wiring,
      dependsOn,
    };
    start(async () => {
      const res: ActionResult = editing ? await updateApplication(app.id, input) : await createApplication(input);
      if (res.ok) {
        toast({ title: editing ? `Updated ${input.name}` : `Created ${input.name}`, tone: "success" });
        router.push("/deployments");
        router.refresh();
      } else {
        toast({ title: "Couldn't save", description: res.error, tone: "danger" });
      }
    });
  };

  const wiredCount = wiring.length;

  return (
    <div className={styles.page}>
      <div className={styles.head}>
        <Link href="/deployments" className={styles.back}>
          <ArrowLeft size={15} strokeWidth={2.4} /> Deployments
        </Link>
        <div className={styles.titleRow}>
          <span className={styles.titleIcon} aria-hidden>
            <Rocket size={20} strokeWidth={2} />
          </span>
          <div>
            <h1 className={styles.title}>{editing ? `Edit ${app.name}` : "New deployment"}</h1>
            <p className={styles.subtitle}>
              A deployable Helm chart, optionally backed by Azure infra (Bicep). Set each side&apos;s inputs, then wire
              the infra&apos;s outputs into the chart.
            </p>
          </div>
        </div>
        {!editing && cluster && <ClusterLine cluster={cluster} />}
      </div>

      <div className={styles.body}>
        <Section icon={Package} title="Essentials">
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
        </Section>

        <div className={styles.columns}>
          <div className={styles.col}>
            <Section
              icon={Cloud}
              title="Azure infrastructure"
              desc="An optional Bicep module, provisioned in the tenant's resource group before the chart."
              status={
                !hasModule ? (
                  <StatusBadge tone="neutral" label="optional" variant="soft" />
                ) : inspect.loading ? (
                  <StatusBadge tone="info" label="inspecting…" variant="soft" pulse />
                ) : inspect.resolved ? (
                  <StatusBadge tone="success" label={`${inspect.params.length} inputs · ${inspect.outputs.length} outputs`} variant="soft" />
                ) : inspect.error ? (
                  <StatusBadge tone="warning" label="couldn't inspect" variant="soft" />
                ) : undefined
              }
            >
              <Field
                label="Bicep module reference"
                htmlFor="dep-bicep"
                hint="An OCI reference to a published Bicep module — its inputs + outputs resolve as you type."
              >
                <TextInput
                  id="dep-bicep"
                  value={bicepModule}
                  onChange={(e) => setBicepModule(e.target.value)}
                  spellCheck={false}
                  placeholder="br:cortexcpacrzo7yflmq.azurecr.io/bicep/postgres:1.2.0"
                />
              </Field>
            </Section>
          </div>

          <div className={styles.col}>
            <Section
              icon={Boxes}
              title="Helm chart"
              desc="The chart to install into the cluster as an Argo CD Application."
              status={
                chartLoading ? (
                  <StatusBadge tone="info" label="inspecting…" variant="soft" pulse />
                ) : helmPaths.length > 0 ? (
                  <StatusBadge tone="success" label={`${helmPaths.length} value paths`} variant="soft" />
                ) : hasChart ? (
                  <StatusBadge tone="neutral" label="no schema" variant="soft" />
                ) : undefined
              }
            >
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
            </Section>
          </div>
        </div>

        {hasModule && (
          <Section
            icon={SlidersHorizontal}
            title="Bicep inputs"
            desc="Wire a static value into each of the module's parameters — baked into the template on save."
          >
            <WiringCanvas
              targets={paramNames}
              allowAddTarget
              sourceLabel="Static inputs"
              targetLabel="Bicep inputs"
              addPlaceholder="Add a parameter not listed…"
              emptyHint={inspect.loading ? "Resolving the module's inputs…" : "No inputs resolved — add a parameter below."}
              initialStatic={bicepInitialStatic}
              initialWired={{}}
              onChange={(sm) => {
                const params: Record<string, unknown> = {};
                for (const [k, v] of Object.entries(sm)) if (v.trim() !== "") params[k] = coerce(v);
                setParamValues(params);
              }}
            />
          </Section>
        )}

        {hasChart && (
          <Section
            icon={Cable}
            title="Helm values"
            desc="Wire a source — a static value you type, or a Bicep output — into each Helm value. The only place a chart's values are set."
            accent
          >
            <WiringCanvas
              outputs={liveOutputs}
              targets={helmPaths}
              suggestions={helmPaths}
              allowAddTarget
              sourceLabel="Sources"
              targetLabel="Helm values"
              addPlaceholder="Add a value not listed — e.g. extraEnv.LOG_LEVEL"
              emptyHint="No values resolved from the chart — set a Helm chart above, or add a value below."
              initialStatic={helmInitialStatic}
              initialWired={helmInitialWired}
              onChange={(sm, wm) => {
                setValues(mapToYaml(sm));
                setWiring(Object.entries(wm).map(([helmPath, bicepOutput]) => ({ bicepOutput, helmPath })));
              }}
            />
          </Section>
        )}

        {depOptions.length > 0 && (
          <Section icon={GitBranch} title="Deploy after" desc="Dependencies converge first — Argo sync-waves order the deploy.">
            <div className={styles.depGrid}>
              {depOptions.map((o) => {
                const on = dependsOn.includes(o.id);
                return (
                  <button type="button" key={o.id} className={styles.depChip} data-on={on || undefined} onClick={() => toggleDep(o.id)}>
                    {o.kind === "agent" ? <Bot size={14} strokeWidth={2.2} /> : <Rocket size={14} strokeWidth={2.2} />}
                    <span>{o.name}</span>
                  </button>
                );
              })}
            </div>
          </Section>
        )}
      </div>

      <div className={styles.footer}>
        <div className={styles.summary}>
          {hasChart && (
            <span className={styles.sumItem}>
              <Boxes size={14} strokeWidth={2.2} /> <span className="mono">{chart.trim() || "chart"}</span>
            </span>
          )}
          {hasModule && (
            <span className={styles.sumItem}>
              <Cloud size={14} strokeWidth={2.2} /> Azure infra
            </span>
          )}
          {wiredCount > 0 && (
            <span className={styles.sumItem}>
              <Cable size={14} strokeWidth={2.2} /> {wiredCount} wired
            </span>
          )}
          {dependsOn.length > 0 && (
            <span className={styles.sumItem}>
              <GitBranch size={14} strokeWidth={2.2} /> {dependsOn.length} dep{dependsOn.length === 1 ? "" : "s"}
            </span>
          )}
        </div>
        <div className={styles.actions}>
          <Button onClick={() => router.push("/deployments")}>Cancel</Button>
          <Button variant="primary" loading={pending} disabled={!valid} onClick={submit}>
            {editing ? "Save deployment" : "Create deployment"}
          </Button>
        </div>
      </div>
    </div>
  );
}

function Section({
  icon: Icon,
  title,
  desc,
  status,
  accent,
  children,
}: {
  icon: typeof Package;
  title: string;
  desc?: string;
  status?: ReactNode;
  accent?: boolean;
  children: ReactNode;
}) {
  return (
    <section className={styles.section} data-accent={accent || undefined}>
      <div className={styles.sectionHead}>
        <span className={styles.sectionIcon} aria-hidden>
          <Icon size={16} strokeWidth={2.1} />
        </span>
        <div className={styles.sectionMeta}>
          <h2 className={styles.sectionTitle}>{title}</h2>
          {desc && <p className={styles.sectionDesc}>{desc}</p>}
        </div>
        {status && <div className={styles.sectionStatus}>{status}</div>}
      </div>
      <div className={styles.sectionBody}>{children}</div>
    </section>
  );
}

function ClusterLine({ cluster }: { cluster: ClusterInfo }) {
  const tone = cluster.phase === "ready" ? "success" : cluster.phase === "unreachable" ? "danger" : cluster.phase === "provisioning" ? "info" : "neutral";
  const label =
    cluster.phase === "ready" ? "Cluster ready" : cluster.phase === "provisioning" ? "Cluster provisioning" : cluster.phase === "unreachable" ? "Cluster unreachable" : "No cluster";
  return (
    <div className={styles.clusterLine}>
      <StatusBadge tone={tone} label={label} variant="soft" pulse={cluster.phase === "provisioning"} />
      <span className={styles.clusterNote}>Enabling this deployment stamps it into your cluster via Argo CD.</span>
    </div>
  );
}
