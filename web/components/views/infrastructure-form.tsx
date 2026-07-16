"use client";

import { useEffect, useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Boxes, Cloud, GitBranch, Package, SlidersHorizontal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Field, TextInput, Textarea } from "@/components/ui/form";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import { FormShell, FormSection } from "./form-shell";
import { WiringCanvas } from "./wiring-canvas";
import { DependencyPicker } from "./dependency-picker";
import {
  createInfrastructure,
  updateInfrastructure,
  inspectInfraModule,
  type ActionResult,
} from "@/lib/actions";
import { coerce, toText } from "@/lib/values";
import type { BicepOutputSpec, BicepParamSpec, Dependency, DepOption, Infrastructure } from "@/lib/types";
import styles from "./form-shell.module.css";

type Obj = Record<string, unknown>;

export function InfrastructureForm({
  infra,
  depOptions = [],
}: {
  infra?: Infrastructure;
  depOptions?: DepOption[];
}) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();
  const editing = infra !== undefined;

  const [name, setName] = useState(infra?.name ?? "");
  const [description, setDescription] = useState(infra?.description ?? "");
  const [bicepModule, setBicepModule] = useState(infra?.bicepModule ?? "");
  const [paramValues, setParamValues] = useState<Obj>(infra?.bicepParams ?? {});
  const [dependencies, setDependencies] = useState<Dependency[]>(infra?.dependencies ?? []);

  // Module inspection → typed inputs + resolved outputs.
  const [inspect, setInspect] = useState<{
    loading: boolean;
    resolved: boolean;
    params: BicepParamSpec[];
    outputs: BicepOutputSpec[];
    error?: string;
  }>({ loading: false, resolved: false, params: [], outputs: [] });

  useEffect(() => {
    const ref = bicepModule.trim();
    if (ref === "") {
      setInspect({ loading: false, resolved: false, params: [], outputs: [] });
      return;
    }
    let cancelled = false;
    setInspect((s) => ({ ...s, loading: true, error: undefined }));
    const t = setTimeout(async () => {
      try {
        const r = await inspectInfraModule(ref);
        if (cancelled) return;
        if (r.ok) setInspect({ loading: false, resolved: r.resolved, params: r.params, outputs: r.outputs });
        else setInspect({ loading: false, resolved: false, params: [], outputs: [], error: r.error });
      } catch {
        if (!cancelled) setInspect({ loading: false, resolved: false, params: [], outputs: [], error: "Couldn't inspect the module." });
      }
    }, 500);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
  }, [bicepModule]);

  const paramNames = inspect.params.map((p) => p.name);
  // Seed the Bicep inputs board from the entity being edited (once; the board
  // owns its state after mount).
  const bicepInitialStatic = Object.fromEntries(Object.entries(infra?.bicepParams ?? {}).map(([k, v]) => [k, toText(v)]));

  const hasModule = bicepModule.trim() !== "";
  // A resolved module's required inputs must be set, or the save-time `bicep build`
  // fails (BCP035). paramValues only holds inputs that were given a value.
  const requiredParams = inspect.params.filter((p) => p.required).map((p) => p.name);
  const missingRequired = requiredParams.filter((n) => !(n in paramValues));
  const valid = name.trim().length >= 2 && hasModule && missingRequired.length === 0;

  const submit = () => {
    const input = {
      name: name.trim(),
      description: description.trim(),
      bicepModule: bicepModule.trim(),
      bicepParams: paramValues,
      dependencies,
    };
    start(async () => {
      const res: ActionResult = editing ? await updateInfrastructure(infra.id, input) : await createInfrastructure(input);
      if (res.ok) {
        toast({ title: editing ? `Updated ${input.name}` : `Created ${input.name}`, tone: "success" });
        router.push("/infrastructure");
        router.refresh();
      } else {
        toast({ title: "Couldn't save", description: res.error, tone: "danger" });
      }
    });
  };

  return (
    <FormShell
      backHref="/infrastructure"
      backLabel="Infrastructure"
      icon={Boxes}
      title={editing ? `Edit ${infra.name}` : "New infrastructure"}
      subtitle="An Azure (Bicep) module the control plane provisions into the tenant's resource group. Set its inputs; its outputs can be wired into a deployment's Helm values."
      footer={
        <>
          <div className={styles.summary}>
            {hasModule && (
              <span className={styles.sumItem}>
                <Cloud size={14} strokeWidth={2.2} /> Azure infra
              </span>
            )}
            {dependencies.length > 0 && (
              <span className={styles.sumItem}>
                <GitBranch size={14} strokeWidth={2.2} /> {dependencies.length} dep{dependencies.length === 1 ? "" : "s"}
              </span>
            )}
          </div>
          <div className={styles.actions}>
            <Button onClick={() => router.push("/infrastructure")}>Cancel</Button>
            <Button variant="primary" loading={pending} disabled={!valid} onClick={submit}>
              {editing ? "Save infrastructure" : "Create infrastructure"}
            </Button>
          </div>
        </>
      }
    >
      <FormSection icon={Package} title="Essentials">
        <Field label="Name" htmlFor="infra-name" hint="A short, human name — e.g. Postgres.">
          <TextInput id="infra-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Postgres" autoFocus />
        </Field>
        <Field label="Description" htmlFor="infra-desc">
          <Textarea id="infra-desc" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="What this infrastructure provisions, for which deployments." />
        </Field>
      </FormSection>

      <FormSection
        icon={Cloud}
        title="Azure module (Bicep)"
        desc="An OCI reference to a published Bicep module — its inputs + outputs resolve as you type."
        status={
          !hasModule ? (
            <StatusBadge tone="neutral" label="required" variant="soft" />
          ) : inspect.loading ? (
            <StatusBadge tone="info" label="inspecting…" variant="soft" pulse />
          ) : inspect.resolved ? (
            <StatusBadge tone="success" label={`${inspect.params.length} inputs · ${inspect.outputs.length} outputs`} variant="soft" />
          ) : inspect.error ? (
            <StatusBadge tone="warning" label="couldn't inspect" variant="soft" />
          ) : undefined
        }
      >
        <Field label="Bicep module reference" htmlFor="infra-bicep">
          <TextInput
            id="infra-bicep"
            value={bicepModule}
            onChange={(e) => setBicepModule(e.target.value)}
            spellCheck={false}
            placeholder="br:cortexcpacrzo7yflmq.azurecr.io/bicep/postgres:1.2.0"
          />
        </Field>
      </FormSection>

      {hasModule && (
        <FormSection
          icon={SlidersHorizontal}
          title="Bicep inputs"
          desc="Wire a static value into each of the module's parameters — baked into the template on save."
        >
          <WiringCanvas
            targets={paramNames}
            requiredTargets={requiredParams}
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
          {missingRequired.length > 0 && (
            <p className={styles.note}>
              {missingRequired.length} required input{missingRequired.length === 1 ? "" : "s"} still unset — wire a value into{" "}
              <span className="mono">{missingRequired.join(", ")}</span> before saving.
            </p>
          )}
        </FormSection>
      )}

      {depOptions.length > 0 && (
        <FormSection icon={GitBranch} title="Depends on" desc="Other infrastructure that must provision first.">
          <DependencyPicker options={depOptions} value={dependencies} onChange={setDependencies} />
        </FormSection>
      )}
    </FormShell>
  );
}
