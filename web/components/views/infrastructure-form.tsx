"use client";

import { useEffect, useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Boxes, Cloud, GitBranch, KeyRound, Package, SlidersHorizontal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Field, Select, TextInput, Textarea } from "@/components/ui/form";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import { FormShell, FormSection } from "./form-shell";
import { ValuesEditor } from "./values-editor";
import { DependencyPicker } from "./dependency-picker";
import {
  createInfrastructure,
  updateInfrastructure,
  inspectInfraModule,
  type ActionResult,
} from "@/lib/actions";
import { coerce, toText } from "@/lib/values";
import type {
  BicepOutputSpec,
  BicepParamSpec,
  Dependency,
  DepOption,
  Infrastructure,
  SecretSet,
} from "@/lib/types";
import styles from "./form-shell.module.css";
import inf from "./infrastructure-form.module.css";

type Obj = Record<string, unknown>;

/** A Bicep param bound to a secret store key, as stored in bicepParams. */
type SecretRef = { $secret: { setId: string; key: string } };

const isSecretRef = (v: unknown): v is SecretRef =>
  typeof v === "object" &&
  v !== null &&
  "$secret" in v &&
  typeof (v as SecretRef).$secret?.setId === "string";

export function InfrastructureForm({
  infra,
  depOptions = [],
  secretSets = [],
}: {
  infra?: Infrastructure;
  depOptions?: DepOption[];
  secretSets?: SecretSet[];
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

  // A @secure() parameter is not typed in — it is bound to a secret store key,
  // and the value is fetched by Azure at deploy time. Keeping it out of the
  // ordinary inputs board is the point: anything entered there is baked into the
  // template as a literal and preserved in the deployment history forever, which
  // is precisely where a password must not go.
  const secureParams = inspect.params.filter((p) => p.secure);
  const secureNames = new Set(secureParams.map((p) => p.name));
  const paramNames = inspect.params.filter((p) => !p.secure).map((p) => p.name);

  const [bindings, setBindings] = useState<Record<string, { setId: string; key: string }>>(() => {
    const out: Record<string, { setId: string; key: string }> = {};
    for (const [k, v] of Object.entries(infra?.bicepParams ?? {})) {
      if (isSecretRef(v)) out[k] = { setId: v.$secret.setId, key: v.$secret.key };
    }
    return out;
  });
  // Seed the Bicep inputs board from the entity being edited (once; the board
  // owns its state after mount).
  const bicepInitialStatic = Object.fromEntries(
    Object.entries(infra?.bicepParams ?? {})
      .filter(([, v]) => !isSecretRef(v))
      .map(([k, v]) => [k, toText(v)]),
  );

  const hasModule = bicepModule.trim() !== "";
  // A resolved module's required inputs must be set, or the save-time `bicep build`
  // fails (BCP035). paramValues only holds inputs that were given a value.
  const requiredParams = inspect.params
    .filter((p) => p.required && !p.secure)
    .map((p) => p.name);
  const missingRequired = requiredParams.filter((n) => !(n in paramValues));
  // A required secure param with no binding would fail the deployment with an
  // opaque ARM error about a missing parameter, so it is caught here instead.
  const missingSecrets = secureParams
    .filter((p) => p.required && !bindings[p.name]?.key)
    .map((p) => p.name);
  const valid =
    name.trim().length >= 2 && hasModule && missingRequired.length === 0 && missingSecrets.length === 0;

  const submit = () => {
    const input = {
      name: name.trim(),
      description: description.trim(),
      bicepModule: bicepModule.trim(),
      bicepParams: {
        ...paramValues,
        ...Object.fromEntries(
          Object.entries(bindings)
            .filter(([, b]) => b.setId && b.key)
            .map(([param, b]) => [param, { $secret: { setId: b.setId, key: b.key } }]),
        ),
      },
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
            placeholder="br:cortexcpacr6hy6uurw.azurecr.io/bicep/postgres:1.2.0"
          />
        </Field>
      </FormSection>

      {hasModule && (
        <FormSection
          icon={SlidersHorizontal}
          title="Bicep inputs"
          desc="Set each of the module's parameters — baked into the template on save. Put {{tenantHash}} (or {{tenant}} / {{region}}) inside a value for a per-tenant-unique result — e.g. a globally-unique Key Vault name across tenants."
        >
          <ValuesEditor
            targets={paramNames}
            requiredTargets={requiredParams}
            allowAddTarget
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
              {missingRequired.length} required input{missingRequired.length === 1 ? "" : "s"} still unset — set{" "}
              <span className="mono">{missingRequired.join(", ")}</span> before saving.
            </p>
          )}
        </FormSection>
      )}

      {hasModule && secureParams.length > 0 && (
        <FormSection
          icon={KeyRound}
          title="Secrets"
          desc="These parameters are declared @secure() by the module. Bind each one to a secret store key — Azure reads the value from the tenant's vault at deploy time, so it is never written into the template."
        >
          <ul className={inf.bindings} role="list">
            {secureParams.map((p) => {
              const b = bindings[p.name] ?? { setId: "", key: "" };
              const chosen = secretSets.find((s) => s.id === b.setId);
              return (
                <li key={p.name} className={inf.binding}>
                  <div className={inf.bindingHead}>
                    <span className={`${inf.param} mono`}>{p.name}</span>
                    {p.required && <span className={inf.required}>required</span>}
                  </div>
                  {p.description && <p className={inf.paramDesc}>{p.description}</p>}
                  <div className={inf.bindingRow}>
                    <Select
                      aria-label={`Secret store for ${p.name}`}
                      value={b.setId}
                      onChange={(e) =>
                        setBindings((v) => ({
                          ...v,
                          [p.name]: { setId: e.target.value, key: "" },
                        }))
                      }
                    >
                      <option value="">Choose a secret store…</option>
                      {secretSets.map((s) => (
                        <option key={s.id} value={s.id}>
                          {s.name}
                        </option>
                      ))}
                    </Select>
                    <Select
                      aria-label={`Key for ${p.name}`}
                      value={b.key}
                      disabled={!chosen}
                      onChange={(e) =>
                        setBindings((v) => ({
                          ...v,
                          [p.name]: { setId: b.setId, key: e.target.value },
                        }))
                      }
                    >
                      <option value="">{chosen ? "Choose a key…" : "Choose a store first"}</option>
                      {(chosen?.keys ?? []).map((k) => (
                        <option key={k} value={k}>
                          {k}
                        </option>
                      ))}
                    </Select>
                  </div>
                </li>
              );
            })}
          </ul>
          {secretSets.length === 0 && (
            <p className={styles.note}>
              No secret stores available yet. Create one first, then come back to bind these
              parameters.
            </p>
          )}
          {missingSecrets.length > 0 && secretSets.length > 0 && (
            <p className={styles.note}>
              <span className="mono">{missingSecrets.join(", ")}</span>{" "}
              {missingSecrets.length === 1 ? "is" : "are"} required and still unbound.
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
