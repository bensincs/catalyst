"use client";

import { useState } from "react";
import { ChevronDown, Lock } from "lucide-react";
import { TextInput, Textarea, Select, Checkbox } from "@/components/ui/form";
import type { BicepParamSpec } from "@/lib/types";
import styles from "./module-inputs-form.module.css";

/** A generated authoring form for a Bicep module's inputs — one typed control
 *  per parameter (dropdowns for allowed values, toggles for bools, JSON for
 *  objects), required first and optional ones tucked behind an expander. Emits a
 *  params object; a cleared field drops its key so the module's default applies. */
export function ModuleInputsForm({
  params,
  value,
  onChange,
}: {
  params: BicepParamSpec[];
  value: Record<string, unknown>;
  onChange: (v: Record<string, unknown>) => void;
}) {
  const [showOptional, setShowOptional] = useState(false);
  const required = params.filter((p) => p.required);
  const optional = params.filter((p) => !p.required);

  const set = (name: string, v: unknown) => {
    const next = { ...value };
    if (v === undefined) delete next[name];
    else next[name] = v;
    onChange(next);
  };

  return (
    <div className={styles.form}>
      {required.map((p) => (
        <ParamField key={p.name} spec={p} value={value[p.name]} onChange={(v) => set(p.name, v)} />
      ))}

      {optional.length > 0 && (
        <>
          <button
            type="button"
            className={styles.expander}
            data-open={showOptional || undefined}
            onClick={() => setShowOptional((s) => !s)}
          >
            <ChevronDown size={15} strokeWidth={2.4} />
            {showOptional ? "Hide" : "Show"} {optional.length} optional parameter{optional.length === 1 ? "" : "s"}
          </button>
          {showOptional &&
            optional.map((p) => (
              <ParamField key={p.name} spec={p} value={value[p.name]} onChange={(v) => set(p.name, v)} />
            ))}
        </>
      )}
    </div>
  );
}

function ParamField({
  spec,
  value,
  onChange,
}: {
  spec: BicepParamSpec;
  value: unknown;
  onChange: (v: unknown) => void;
}) {
  const id = `param-${spec.name}`;
  const isExpr = typeof spec.default === "string" && spec.default.startsWith("[");
  const placeholder = spec.default !== undefined && !isExpr ? String(spec.default) : "";

  // Booleans read best as a labelled toggle — the name + description live on it.
  if (spec.type === "bool") {
    const checked = value === undefined ? Boolean(spec.default) : Boolean(value);
    return (
      <div className={styles.param}>
        <Checkbox checked={checked} onChange={(v) => onChange(v)} label={spec.name} description={spec.description} />
      </div>
    );
  }

  let control: React.ReactNode;
  if (spec.allowed && spec.allowed.length > 0) {
    control = (
      <Select
        id={id}
        value={value === undefined ? "" : String(value)}
        onChange={(e) => onChange(e.target.value === "" ? undefined : spec.allowed!.find((a) => String(a) === e.target.value))}
      >
        <option value="">{spec.required ? "Select…" : placeholder ? `Default — ${placeholder}` : "Default"}</option>
        {spec.allowed.map((a) => (
          <option key={String(a)} value={String(a)}>
            {String(a)}
          </option>
        ))}
      </Select>
    );
  } else if (spec.type === "int") {
    control = (
      <TextInput
        id={id}
        type="number"
        value={value === undefined ? "" : String(value)}
        onChange={(e) => {
          const t = e.target.value.trim();
          onChange(t === "" ? undefined : Number(t));
        }}
        placeholder={placeholder}
      />
    );
  } else if (spec.type === "object" || spec.type === "array" || spec.type === "secureobject") {
    control = <JsonParam id={id} value={value} onChange={onChange} array={spec.type === "array"} />;
  } else {
    control = (
      <TextInput
        id={id}
        type={spec.secure ? "password" : "text"}
        value={value === undefined ? "" : String(value)}
        onChange={(e) => onChange(e.target.value === "" ? undefined : e.target.value)}
        placeholder={placeholder}
        spellCheck={false}
        autoComplete={spec.secure ? "new-password" : undefined}
      />
    );
  }

  return (
    <div className={styles.param}>
      <div className={styles.head}>
        <label htmlFor={id} className={styles.name}>
          {spec.name}
        </label>
        <span className={styles.type}>{spec.secure ? "secure" : spec.type}</span>
        {spec.required ? (
          <span className={styles.req}>required</span>
        ) : (
          <span className={styles.opt}>optional</span>
        )}
        {spec.secure && <Lock size={12} strokeWidth={2.2} className={styles.lock} aria-hidden />}
      </div>
      {spec.description && <p className={styles.desc}>{spec.description}</p>}
      {control}
      {spec.secure && (
        <p className={styles.secureNote}>Baked into the template — avoid real secrets until Key Vault wiring lands.</p>
      )}
    </div>
  );
}

function JsonParam({
  id,
  value,
  onChange,
  array,
}: {
  id: string;
  value: unknown;
  onChange: (v: unknown) => void;
  array: boolean;
}) {
  const [text, setText] = useState(value !== undefined ? JSON.stringify(value, null, 2) : "");
  const [invalid, setInvalid] = useState(false);
  return (
    <>
      <Textarea
        id={id}
        value={text}
        spellCheck={false}
        className={styles.json}
        placeholder={array ? "[]" : "{}"}
        onChange={(e) => {
          const t = e.target.value;
          setText(t);
          if (t.trim() === "") {
            setInvalid(false);
            onChange(undefined);
            return;
          }
          try {
            onChange(JSON.parse(t));
            setInvalid(false);
          } catch {
            setInvalid(true);
          }
        }}
      />
      {invalid && <p className={styles.err}>Invalid JSON — last valid value kept.</p>}
    </>
  );
}
