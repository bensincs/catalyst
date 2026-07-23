"use client";

import { useEffect, useMemo, useState, useTransition, type ReactNode } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { ArrowLeft, Boxes, Cable, GitBranch, Globe, Package, Rocket } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Field, TextInput, Textarea } from "@/components/ui/form";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import { WiringCanvas } from "./wiring-canvas";
import { DependencyPicker } from "./dependency-picker";
import { createApplication, updateApplication, inspectChart, type ActionResult } from "@/lib/actions";
import { mapToYaml, yamlToMap } from "@/lib/values";
import type { Application, ClusterInfo, Dependency, DepKind, DepOption, Role, WireLink } from "@/lib/types";
import styles from "./deployment-form.module.css";

type Obj = Record<string, unknown>;

/** A dependency candidate's wireable outputs — the sources the author can wire
 *  into Helm values once it's a dependency. Infrastructure exposes its resolved
 *  Bicep outputs; applications/agents expose derived outputs (see below). */
export interface DepOutputs {
  kind: DepKind;
  id: string;
  name: string;
  outputs: string[];
}

// Derived outputs a dependency application / agent exposes for wiring.
export const APP_OUTPUTS = ["name", "namespace", "serviceHost"];
export const AGENT_OUTPUTS = ["agentId", "name"];

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

// A wiring source token encodes which dependency an output came from, so every
// emitted WireLink carries its source. Format: `<kind>:<id>:<output>` (kinds are
// fixed words and ids are colon-free slugs; the output is the rest).
const wireToken = (kind: string, id: string, output: string) => `${kind}:${id}:${output}`;
function parseWireToken(token: string): { sourceKind: DepKind; sourceId: string; output: string } {
  const a = token.indexOf(":");
  const b = token.indexOf(":", a + 1);
  if (a < 0 || b < 0) return { sourceKind: "infrastructure", sourceId: "", output: token };
  return {
    sourceKind: token.slice(0, a) as DepKind,
    sourceId: token.slice(a + 1, b),
    output: token.slice(b + 1),
  };
}

export function DeploymentForm({
  role,
  app,
  depOptions = [],
  depOutputs = [],
  cluster,
}: {
  role: Role;
  app?: Application;
  depOptions?: DepOption[];
  depOutputs?: DepOutputs[];
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
  const [values, setValues] = useState(app?.values ?? "");
  const [exposeService, setExposeService] = useState(app?.exposeService ?? "");
  const [exposePort, setExposePort] = useState(app?.exposePort ?? 80);
  const [wiring, setWiring] = useState<WireLink[]>(app?.wiring ?? []);
  const [dependencies, setDependencies] = useState<Dependency[]>(app?.dependencies ?? []);

  // Chart inspection → Helm value-path suggestions for the wiring canvas.
  const [helmPaths, setHelmPaths] = useState<string[]>([]);
  const [chartLoading, setChartLoading] = useState(false);

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
      try {
        const r = await inspectChart(repo, c, targetRevision.trim());
        if (cancelled) return;
        setChartLoading(false);
        setHelmPaths(r.ok && r.resolved && r.iface ? flattenPaths(r.iface.defaults) : []);
      } catch {
        if (!cancelled) {
          setChartLoading(false);
          setHelmPaths([]);
        }
      }
    }, 600);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
  }, [repoURL, chart, targetRevision]);

  // The wireable sources are the outputs of the app's chosen dependencies — each
  // namespaced by "<kind>:<id>" so the WireLink knows its origin.
  const selectedDepKeys = useMemo(
    () => new Set(dependencies.map((d) => `${d.kind}:${d.id}`)),
    [dependencies],
  );
  const liveOutputs = useMemo(
    () =>
      depOutputs
        .filter((d) => selectedDepKeys.has(`${d.kind}:${d.id}`))
        .flatMap((d) => d.outputs.map((o) => wireToken(d.kind, d.id, o))),
    [depOutputs, selectedDepKeys],
  );
  // Render a wiring source as "<dependency name> / <output>" — the name tags the
  // node so identical output names (e.g. two deps' `name`) stay distinguishable.
  const depNameByKey = useMemo(
    () => new Map(depOutputs.map((d) => [`${d.kind}:${d.id}`, d.name])),
    [depOutputs],
  );
  const outputLabel = (token: string) => {
    const { sourceKind, sourceId, output } = parseWireToken(token);
    return { tag: depNameByKey.get(`${sourceKind}:${sourceId}`) ?? sourceId, label: output };
  };

  // Seed the wiring board from the app being edited (once; the board owns state
  // after mount): Helm static values + wired outputs (as source-namespaced tokens).
  const helmInitialStatic = yamlToMap(app?.values ?? "");
  const helmInitialWired = Object.fromEntries(
    (app?.wiring ?? []).map((w) => [w.helmPath, wireToken(w.sourceKind, w.sourceId, w.output)]),
  );

  const hasChart = repoURL.trim() !== "" && chart.trim() !== "";
  const valid = name.trim().length >= 2 && hasChart;

  const submit = () => {
    // Only keep wiring whose source is still a selected dependency.
    const cleanWiring = wiring.filter((w) => selectedDepKeys.has(`${w.sourceKind}:${w.sourceId}`));
    const input = {
      name: name.trim(),
      description: description.trim(),
      namespace: namespace.trim() || "default",
      repoURL: repoURL.trim(),
      chart: chart.trim(),
      targetRevision: targetRevision.trim(),
      values,
      exposeService: exposeService.trim(),
      exposePort: exposePort || 80,
      wiring: cleanWiring,
      dependencies,
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

  const wiredCount = wiring.filter((w) => selectedDepKeys.has(`${w.sourceKind}:${w.sourceId}`)).length;

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
              A deployable Helm chart, realized as an Argo CD Application. Add dependencies, then wire the outputs
              of its infrastructure dependencies into the chart&apos;s Helm values.
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

        <Section
          icon={Globe}
          title="Exposure"
          desc="Publish this app through the tenant's gateway. Name the in-cluster Service the chart creates (often <release>-<chart>, e.g. my-app-todo-app) — leave blank to keep the app cluster-internal (no ingress)."
        >
          <div className={styles.grid2}>
            <Field label="Expose service" htmlFor="dep-svc" hint="The chart's Service name to route to. Blank = internal only.">
              <TextInput id="dep-svc" value={exposeService} onChange={(e) => setExposeService(e.target.value)} placeholder="my-app-todo-app" spellCheck={false} />
            </Field>
            <Field label="Port" htmlFor="dep-port" hint="Service port to route to.">
              <TextInput
                id="dep-port"
                type="number"
                value={String(exposePort)}
                onChange={(e) => setExposePort(Number(e.target.value) || 80)}
                placeholder="80"
              />
            </Field>
          </div>
        </Section>

        {depOptions.length > 0 && (
          <Section
            icon={GitBranch}
            title="Dependencies"
            desc="Infrastructure, applications, or agents this deployment waits on — dependencies converge first (Argo sync-waves order the deploy). Each dependency's outputs become wireable below."
          >
            <DependencyPicker options={depOptions} value={dependencies} onChange={setDependencies} />
          </Section>
        )}

        {hasChart && (
          <Section
            icon={Cable}
            title="Helm values"
            desc="Wire a source — a static value you type, or an output of a chosen dependency (infrastructure, application, or agent) — into each Helm value. The only place a chart's values are set."
            accent
          >
            <WiringCanvas
              outputs={liveOutputs}
              outputLabel={outputLabel}
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
                setWiring(
                  Object.entries(wm).map(([helmPath, token]) => {
                    const { sourceKind, sourceId, output } = parseWireToken(token);
                    return { sourceKind, sourceId, output, helmPath };
                  }),
                );
              }}
            />
            {liveOutputs.length === 0 && (
              <p className={styles.note}>
                Add a dependency above to wire its outputs into these values.
              </p>
            )}
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
          {wiredCount > 0 && (
            <span className={styles.sumItem}>
              <Cable size={14} strokeWidth={2.2} /> {wiredCount} wired
            </span>
          )}
          {dependencies.length > 0 && (
            <span className={styles.sumItem}>
              <GitBranch size={14} strokeWidth={2.2} /> {dependencies.length} dep{dependencies.length === 1 ? "" : "s"}
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
