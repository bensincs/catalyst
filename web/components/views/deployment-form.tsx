"use client";

import {
  useEffect,
  useMemo,
  useRef,
  useState,
  useTransition,
  type ReactNode,
} from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import {
  ArrowLeft,
  Boxes,
  Cable,
  GitBranch,
  Globe,
  Package,
  Rocket,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Field, Select, TextInput, Textarea } from "@/components/ui/form";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import { ValuesEditor } from "./values-editor";
import { DependencyPicker } from "./dependency-picker";
import {
  createApplication,
  updateApplication,
  inspectChart,
  type ActionResult,
} from "@/lib/actions";
import { mapToYaml, yamlToMap } from "@/lib/values";
import { dependenciesFor, outputLabel as labelForOutput } from "@/lib/wiring";
import type {
  Application,
  ChartService,
  ClusterInfo,
  Dependency,
  DepKind,
  DepOption,
  Role,
  WireLink,
} from "@/lib/types";
import { APP_ICONS } from "@/lib/app-icons";
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

// Dotted leaf paths of a chart's default values — the Helm value-path suggestions.
function flattenPaths(obj: Obj, base = ""): string[] {
  const out: string[] = [];
  for (const k of Object.keys(obj)) {
    const path = base ? `${base}.${k}` : k;
    const v = obj[k];
    if (
      v &&
      typeof v === "object" &&
      !Array.isArray(v) &&
      Object.keys(v as object).length > 0
    ) {
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
const wireToken = (kind: string, id: string, output: string) =>
  `${kind}:${id}:${output}`;
function parseWireToken(token: string): {
  sourceKind: DepKind;
  sourceId: string;
  output: string;
} {
  const a = token.indexOf(":");
  const b = token.indexOf(":", a + 1);
  if (a < 0 || b < 0)
    return { sourceKind: "infrastructure", sourceId: "", output: token };
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
  platformRegistry = "",
}: {
  role: Role;
  app?: Application;
  platformRegistry?: string;
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
  const [targetRevision, setTargetRevision] = useState(
    app?.targetRevision ?? "",
  );
  const [values, setValues] = useState(app?.values ?? "");
  const [exposeService, setExposeService] = useState(app?.exposeService ?? "");
  const [exposePort, setExposePort] = useState(app?.exposePort ?? 80);
  const [hostname, setHostname] = useState(app?.hostname ?? "");
  const [authRequired, setAuthRequired] = useState(Boolean(app?.authRequired));
  const [embed, setEmbed] = useState(Boolean(app?.embed));
  const [icon, setIcon] = useState(app?.icon ?? APP_ICONS[0].name);
  const [oidcScope, setOidcScope] = useState(app?.oidcScope ?? "");
  const [wiring, setWiring] = useState<WireLink[]>(app?.wiring ?? []);
  const [dependencies, setDependencies] = useState<Dependency[]>(() =>
    dependenciesFor(app),
  );

  // Chart inspection → Helm value-path suggestions for the wiring canvas.
  const [helmPaths, setHelmPaths] = useState<string[]>([]);
  const [chartLoading, setChartLoading] = useState(false);
  // The version the registry resolves for this chart. Inspection resolves it
  // even when the field is blank, so it can be offered as a one-click pin.
  const [resolvedVersion, setResolvedVersion] = useState("");
  // The Services this release renders — the exposure candidates.
  const [chartServices, setChartServices] = useState<ChartService[]>([]);

  const valuesRef = useRef(values);
  valuesRef.current = values;

  useEffect(() => {
    const repo = repoURL.trim();
    const c = chart.trim();
    if (repo === "" || c === "") {
      setHelmPaths([]);
      setResolvedVersion("");
      setChartServices([]);
      return;
    }
    let cancelled = false;
    setChartLoading(true);
    const t = setTimeout(async () => {
      try {
        const r = await inspectChart(repo, c, targetRevision.trim(), {
          appId: app?.id,
          name,
          values: valuesRef.current,
        });
        if (cancelled) return;
        setChartLoading(false);
        const ok = r.ok && r.resolved && r.iface;
        setHelmPaths(ok ? flattenPaths(r.iface!.defaults) : []);
        setResolvedVersion(ok ? (r.iface!.version ?? "") : "");
        setChartServices(ok ? (r.iface!.services ?? []) : []);
      } catch {
        if (!cancelled) {
          setChartLoading(false);
          setHelmPaths([]);
          setResolvedVersion("");
          setChartServices([]);
        }
      }
    }, 600);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
  }, [repoURL, chart, targetRevision, name, app?.id]);

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
    return {
      tag: depNameByKey.get(`${sourceKind}:${sourceId}`) ?? sourceId,
      label: labelForOutput(
        output,
        sourceKind === "secret_set" ? sourceId : undefined,
      ),
    };
  };

  // Seed the wiring board from the app being edited (once; the board owns state
  // after mount): Helm static values + wired outputs (as source-namespaced tokens).
  const helmInitialStatic = yamlToMap(app?.values ?? "");
  const helmInitialWired = Object.fromEntries(
    (app?.wiring ?? []).map((w) => [
      w.helmPath,
      wireToken(w.sourceKind, w.sourceId, w.output),
    ]),
  );

  const hasChart = repoURL.trim() !== "" && chart.trim() !== "";
  // Argo rejects a Helm chart source with no targetRevision, so an unpinned
  // chart cannot deploy at all. Pinning also keeps a deploy reproducible: with
  // automated sync and selfHeal on, a floating version would silently roll a
  // new chart into every tenant the moment it was published.
  const hasVersion = targetRevision.trim() !== "";
  const valid = name.trim().length >= 2 && hasChart && hasVersion;

  const submit = () => {
    // Only keep wiring whose source is still a selected dependency.
    const cleanWiring = wiring.filter((w) =>
      selectedDepKeys.has(`${w.sourceKind}:${w.sourceId}`),
    );
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
      hostname: hostname.trim().toLowerCase(),
      authRequired,
      embed,
      icon,
      oidcScope: oidcScope.trim(),
      wiring: cleanWiring,
      dependencies,
    };
    start(async () => {
      const res: ActionResult = editing
        ? await updateApplication(app.id, input)
        : await createApplication(input);
      if (res.ok) {
        toast({
          title: editing ? `Updated ${input.name}` : `Created ${input.name}`,
          tone: "success",
        });
        router.push("/deployments");
        router.refresh();
      } else {
        toast({
          title: "Couldn't save",
          description: res.error,
          tone: "danger",
        });
      }
    });
  };

  const wiredCount = wiring.filter((w) =>
    selectedDepKeys.has(`${w.sourceKind}:${w.sourceId}`),
  ).length;

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
            <h1 className={styles.title}>
              {editing ? `Edit ${app.name}` : "New deployment"}
            </h1>
            <p className={styles.subtitle}>
              A deployable Helm chart, realized as an Argo CD Application. Add
              dependencies, then wire the outputs of its infrastructure
              dependencies into the chart&apos;s Helm values.
            </p>
          </div>
        </div>
        {!editing && cluster && <ClusterLine cluster={cluster} />}
      </div>

      <div className={styles.body}>
        <Section icon={Package} title="Essentials">
          <div className={styles.grid2}>
            <Field
              label="Name"
              htmlFor="dep-name"
              hint="Becomes the Argo Application + release."
            >
              <TextInput
                id="dep-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="ingress-nginx"
                autoFocus
              />
            </Field>
            <Field
              label="Namespace"
              htmlFor="dep-ns"
              hint="Destination namespace (created if missing)."
            >
              <TextInput
                id="dep-ns"
                value={namespace}
                onChange={(e) => setNamespace(e.target.value)}
                placeholder="default"
                spellCheck={false}
              />
            </Field>
          </div>
          <Field label="Description" htmlFor="dep-desc">
            <Textarea
              id="dep-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="What this deployment is for."
            />
          </Field>
        </Section>

        <Section
          icon={Boxes}
          title="Helm chart"
          desc={
            platformRegistry
              ? `The chart to install into the cluster as an Argo CD Application. Public registries can be used directly; a private chart is mirrored into ${platformRegistry}, which every tenant can pull from.`
              : "The chart to install into the cluster as an Argo CD Application."
          }
          status={
            chartLoading ? (
              <StatusBadge
                tone="info"
                label="inspecting…"
                variant="soft"
                pulse
              />
            ) : helmPaths.length > 0 ? (
              <StatusBadge
                tone="success"
                label={`${helmPaths.length} value paths`}
                variant="soft"
              />
            ) : hasChart ? (
              <StatusBadge tone="neutral" label="no schema" variant="soft" />
            ) : undefined
          }
        >
          <datalist id="dep-repos">
            {platformRegistry && (
              <option value={`oci://${platformRegistry}/charts`} />
            )}
            <option value="https://charts.bitnami.com/bitnami" />
            <option value="https://kubernetes.github.io/ingress-nginx" />
            <option value="https://prometheus-community.github.io/helm-charts" />
          </datalist>
          <Field
            label="Helm repo / OCI URL"
            htmlFor="dep-repo"
            hint="A Helm repository (https://…) or OCI registry (oci://…)."
          >
            <div className={styles.versionRow}>
              <TextInput
                id="dep-repo"
                list="dep-repos"
                value={repoURL}
                onChange={(e) => setRepoURL(e.target.value)}
                placeholder="https://charts.bitnami.com/bitnami"
                spellCheck={false}
              />
              {platformRegistry !== "" &&
                !repoURL.includes(platformRegistry) && (
                  <Button
                    type="button"
                    variant="secondary"
                    size="sm"
                    title={`Charts mirrored into ${platformRegistry}, which every tenant can pull from`}
                    onClick={() =>
                      setRepoURL(`oci://${platformRegistry}/charts`)
                    }
                  >
                    Platform registry
                  </Button>
                )}
            </div>
          </Field>
          <div className={styles.grid2}>
            <Field label="Chart" htmlFor="dep-chart">
              <TextInput
                id="dep-chart"
                value={chart}
                onChange={(e) => setChart(e.target.value)}
                placeholder="nginx"
                spellCheck={false}
              />
            </Field>
            <Field
              label="Version"
              htmlFor="dep-ver"
              hint={
                hasChart && !hasVersion
                  ? "Required — a chart deploys at a pinned version."
                  : "The chart version this deploys at."
              }
            >
              <div className={styles.versionRow}>
                <TextInput
                  id="dep-ver"
                  value={targetRevision}
                  onChange={(e) => setTargetRevision(e.target.value)}
                  placeholder="15.14.0"
                  spellCheck={false}
                  aria-invalid={hasChart && !hasVersion ? true : undefined}
                />
                {resolvedVersion !== "" &&
                  targetRevision.trim() !== resolvedVersion && (
                    <Button
                      type="button"
                      variant="secondary"
                      size="sm"
                      onClick={() => setTargetRevision(resolvedVersion)}
                    >
                      Use {resolvedVersion}
                    </Button>
                  )}
              </div>
            </Field>
          </div>
        </Section>

        <Section
          icon={Globe}
          title="Exposure"
          desc="Publish this app through the tenant's gateway. Choose which of the chart's Services to route to — leave it unexposed to keep the app cluster-internal. The app is published at <hostname>.<the tenant's domain>."
        >
          <div className={styles.grid2}>
            <Field
              label="Expose service"
              htmlFor="dep-svc"
              hint={
                chartServices.length > 0
                  ? "A Service this chart renders. Unexposed = internal only."
                  : "The chart's Service name to route to. Blank = internal only."
              }
            >
              {chartServices.length > 0 ? (
                <Select
                  id="dep-svc"
                  value={exposeService}
                  onChange={(e) => {
                    const picked = e.target.value;
                    setExposeService(picked);
                    // A Service names its own port, so there is no reason to make
                    // someone find and retype it.
                    const svc = chartServices.find((c) => c.name === picked);
                    if (svc?.ports?.length) setExposePort(svc.ports[0]);
                  }}
                >
                  <option value="">Not exposed — cluster-internal</option>
                  {chartServices.map((c) => (
                    <option key={c.name} value={c.name}>
                      {c.name}
                      {c.ports?.length ? ` · port ${c.ports.join(", ")}` : ""}
                      {c.type ? ` · ${c.type}` : ""}
                    </option>
                  ))}
                  {/* Keep a stored choice selectable even if the chart, its
                      version or its values no longer render that Service —
                      dropping it silently would unpublish the app on save. */}
                  {exposeService !== "" &&
                    !chartServices.some((c) => c.name === exposeService) && (
                      <option value={exposeService}>
                        {exposeService} — not in this chart
                      </option>
                    )}
                </Select>
              ) : (
                <TextInput
                  id="dep-svc"
                  value={exposeService}
                  onChange={(e) => setExposeService(e.target.value)}
                  placeholder="my-app-todo-app"
                  spellCheck={false}
                />
              )}
            </Field>
            <Field
              label="Port"
              htmlFor="dep-port"
              hint="Service port to route to."
            >
              <TextInput
                id="dep-port"
                type="number"
                value={String(exposePort)}
                onChange={(e) => setExposePort(Number(e.target.value) || 80)}
                placeholder="80"
              />
            </Field>
            <Field
              label="Hostname"
              htmlFor="dep-host"
              hint="Label under the tenant's domain. Blank = the app's name."
            >
              <TextInput
                id="dep-host"
                value={hostname}
                onChange={(e) => setHostname(e.target.value)}
                placeholder="todo"
                spellCheck={false}
              />
            </Field>
            <Field
              label="Scope"
              htmlFor="dep-scope"
              hint="Scope on the tenant's app registration this app requires. Only used when sign-in is on."
            >
              <TextInput
                id="dep-scope"
                value={oidcScope}
                onChange={(e) => setOidcScope(e.target.value)}
                placeholder="api://<client-id>/Todo.Access"
                spellCheck={false}
              />
            </Field>
          </div>
          <label
            style={{
              display: "flex",
              gap: 8,
              alignItems: "flex-start",
              marginTop: 10,
              fontSize: 13,
            }}
          >
            <input
              type="checkbox"
              checked={authRequired}
              onChange={(e) => setAuthRequired(e.target.checked)}
              style={{ marginTop: 3 }}
            />
            <span>
              <strong>Require sign-in</strong>
              <span
                style={{
                  display: "block",
                  color: "var(--text-secondary)",
                  fontSize: 12,
                }}
              >
                Puts the app behind the tenant&apos;s OIDC application. Needs a
                domain and a certificate (the callback must be HTTPS), and the
                callback URL must be registered as a redirect URI on that app
                registration.
              </span>
            </span>
          </label>
          {/* Offering the app in the console's own navigation. Only sensible
              once it publishes a Service — without one there is no URL, so the
              entry would open nothing. */}
          {exposeService.trim() !== "" && (
            <>
              <label
                style={{
                  display: "flex",
                  gap: 8,
                  alignItems: "flex-start",
                  marginTop: 14,
                  fontSize: 13,
                }}
              >
                <input
                  type="checkbox"
                  checked={embed}
                  onChange={(e) => setEmbed(e.target.checked)}
                  style={{ marginTop: 3 }}
                />
                <span>
                  <strong>Show in the sidebar</strong>
                  <span
                    style={{
                      display: "block",
                      color: "var(--text-secondary)",
                      fontSize: 12,
                    }}
                  >
                    Once a tenant enables this, it appears in their navigation and opens
                    inside the console — instead of them having to know its URL. It stays
                    their application, served from their own domain.
                  </span>
                </span>
              </label>

              {embed && (
                <div style={{ marginTop: 12 }}>
                  <Field label="Icon" htmlFor="app-icon" hint="How it is recognised in the sidebar.">
                    <div className={styles.iconGrid}>
                      {APP_ICONS.map(({ name, label, icon: I }: (typeof APP_ICONS)[number]) => (
                        <button
                          key={name}
                          type="button"
                          className={styles.iconChoice}
                          data-selected={icon === name || undefined}
                          onClick={() => setIcon(name)}
                          aria-label={label}
                          aria-pressed={icon === name}
                          title={label}
                        >
                          <I size={17} strokeWidth={2} aria-hidden />
                        </button>
                      ))}
                    </div>
                  </Field>
                </div>
              )}
            </>
          )}
        </Section>

        {depOptions.length > 0 && (
          <Section
            icon={GitBranch}
            title="Dependencies"
            desc="Infrastructure, applications, agents, or secret stores this deployment waits on — dependencies converge first (Argo sync-waves order the deploy). Each dependency's outputs become wireable below."
          >
            <DependencyPicker
              options={depOptions}
              value={dependencies}
              onChange={setDependencies}
            />
          </Section>
        )}

        {hasChart && (
          <Section
            icon={Cable}
            title="Helm values"
            desc="Set each of the chart's values — type a literal, or bind it to an output of one of this deployment's dependencies. The only place a chart's values are set."
            accent
          >
            <ValuesEditor
              outputs={liveOutputs}
              outputLabel={outputLabel}
              targets={helmPaths}
              suggestions={helmPaths}
              allowAddTarget
              targetLabel="Helm values"
              addPlaceholder="Add a value not listed — e.g. extraEnv.LOG_LEVEL"
              emptyHint="No values resolved from the chart — set a Helm chart above, or add a value below."
              initialStatic={helmInitialStatic}
              initialWired={helmInitialWired}
              onChange={(sm, wm) => {
                setValues(mapToYaml(sm));
                setWiring(
                  Object.entries(wm).map(([helmPath, token]) => {
                    const { sourceKind, sourceId, output } =
                      parseWireToken(token);
                    return { sourceKind, sourceId, output, helmPath };
                  }),
                );
              }}
            />
            {liveOutputs.length === 0 && (
              <p className={styles.note}>
                Add a dependency above to bind its outputs to these values.
              </p>
            )}
          </Section>
        )}
      </div>

      <div className={styles.footer}>
        <div className={styles.summary}>
          {hasChart && (
            <span className={styles.sumItem}>
              <Boxes size={14} strokeWidth={2.2} />{" "}
              <span className="mono">{chart.trim() || "chart"}</span>
            </span>
          )}
          {wiredCount > 0 && (
            <span className={styles.sumItem}>
              <Cable size={14} strokeWidth={2.2} /> {wiredCount} wired
            </span>
          )}
          {dependencies.length > 0 && (
            <span className={styles.sumItem}>
              <GitBranch size={14} strokeWidth={2.2} /> {dependencies.length}{" "}
              dep{dependencies.length === 1 ? "" : "s"}
            </span>
          )}
        </div>
        <div className={styles.actions}>
          <Button onClick={() => router.push("/deployments")}>Cancel</Button>
          <Button
            variant="primary"
            loading={pending}
            disabled={!valid}
            onClick={submit}
          >
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
  const tone =
    cluster.phase === "ready"
      ? "success"
      : cluster.phase === "unreachable"
        ? "danger"
        : cluster.phase === "provisioning"
          ? "info"
          : "neutral";
  const label =
    cluster.phase === "ready"
      ? "Cluster ready"
      : cluster.phase === "provisioning"
        ? "Cluster provisioning"
        : cluster.phase === "unreachable"
          ? "Cluster unreachable"
          : "No cluster";
  return (
    <div className={styles.clusterLine}>
      <StatusBadge
        tone={tone}
        label={label}
        variant="soft"
        pulse={cluster.phase === "provisioning"}
      />
      <span className={styles.clusterNote}>
        Enabling this deployment stamps it into your cluster via Argo CD.
      </span>
    </div>
  );
}
