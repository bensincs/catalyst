"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/modal";
import { Field, TextInput, Textarea, Select, Checkbox } from "@/components/ui/form";
import type { AgentDefinition, AgentType, CatalogAgent, MemoryStore, PublishTarget } from "@/lib/types";
import styles from "./catalog-view.module.css";

// The agent authoring surface: type tags plus the New agent / Publish version /
// Enable modals. Rendered inline on the unified Agents page (see agents-view).

const MODELS = ["gpt-4o", "gpt-4o-mini", "gpt-4.1", "jais-30b", "o3-mini"];
const PROMPT_TOOLS: { id: string; label: string; hint: string }[] = [
  { id: "file_search", label: "File search", hint: "Retrieve over attached knowledge." },
  { id: "code_interpreter", label: "Code interpreter", hint: "Run sandboxed Python." },
  { id: "function", label: "Function calling", hint: "Call registered functions." },
  { id: "web", label: "Web grounding", hint: "Ground answers with web results." },
];
const TARGETS: { id: PublishTarget; label: string; hint: string }[] = [
  { id: "api", label: "API endpoint", hint: "A stable HTTPS endpoint (automated)" },
  { id: "teams", label: "Microsoft Teams", hint: "Guided admin publish (preview)" },
  { id: "m365", label: "M365 Copilot", hint: "Guided admin publish (preview)" },
];

export function TypeTag({ type }: { type: AgentType }) {
  return (
    <span className={styles.typeTag} data-type={type}>
      {type === "hosted" ? "Hosted" : "Prompt"}
    </span>
  );
}

export function OwnershipTag({ agent }: { agent: CatalogAgent }) {
  const label = agent.owned ? "Yours" : "Platform";
  return (
    <span className={styles.typeTag} data-type={agent.owned ? "prompt" : "hosted"}>
      {label}
    </span>
  );
}

/* ── Modals ───────────────────────────────────────────────────────────────── */

function TypeToggle({ value, onChange }: { value: AgentType; onChange: (t: AgentType) => void }) {
  const opts: { id: AgentType; label: string; hint: string }[] = [
    { id: "prompt", label: "Prompt", hint: "Declarative — model, instructions, tools." },
    { id: "hosted", label: "Hosted", hint: "Bring-your-own container." },
  ];
  return (
    <div className={styles.typeToggle} role="radiogroup" aria-label="Agent type">
      {opts.map((o) => (
        <button
          key={o.id}
          type="button"
          role="radio"
          aria-checked={value === o.id}
          className={styles.typeOpt}
          data-active={value === o.id || undefined}
          onClick={() => onChange(o.id)}
        >
          <span className={styles.typeOptLabel}>{o.label}</span>
          <span className={styles.typeOptHint}>{o.hint}</span>
        </button>
      ))}
    </div>
  );
}

// DefinitionFields renders the typed definition editor — the substance that
// travels with each published version (see AGENT-MODEL.md).
function DefinitionFields({
  type,
  value,
  onChange,
  stores,
}: {
  type: AgentType;
  value: AgentDefinition;
  onChange: (d: AgentDefinition) => void;
  stores: MemoryStore[];
}) {
  const set = (patch: Partial<AgentDefinition>) => onChange({ ...value, ...patch });

  if (type === "hosted") {
    return (
      <>
        <Field label="Container image" htmlFor="def-image" hint="The published agent image the reconciler deploys.">
          <TextInput id="def-image" value={value.image ?? ""} onChange={(e) => set({ image: e.target.value })} placeholder="ghcr.io/acme/agent:1.0.0" />
        </Field>
        <Field label="Endpoint" htmlFor="def-endpoint" hint="Path the container serves on.">
          <TextInput id="def-endpoint" value={value.endpoint ?? ""} onChange={(e) => set({ endpoint: e.target.value })} placeholder="/invoke" />
        </Field>
        <div className={styles.formRow}>
          <Field label="CPU" htmlFor="def-cpu">
            <TextInput id="def-cpu" value={value.cpu ?? ""} onChange={(e) => set({ cpu: e.target.value })} placeholder="0.5" />
          </Field>
          <Field label="Memory" htmlFor="def-mem">
            <TextInput id="def-mem" value={value.memory ?? ""} onChange={(e) => set({ memory: e.target.value })} placeholder="1Gi" />
          </Field>
        </div>
      </>
    );
  }

  const tools = value.tools ?? [];
  const toggleTool = (id: string) =>
    set({ tools: tools.includes(id) ? tools.filter((t) => t !== id) : [...tools, id] });
  return (
    <>
      <Field label="Instructions" htmlFor="def-instr" hint="The system prompt — how the agent behaves.">
        <Textarea id="def-instr" value={value.instructions ?? ""} onChange={(e) => set({ instructions: e.target.value })} placeholder="You are a precise assistant that…" />
      </Field>
      <Field label="Tools">
        <div className={styles.targets}>
          {PROMPT_TOOLS.map((t) => (
            <Checkbox key={t.id} checked={tools.includes(t.id)} onChange={() => toggleTool(t.id)} label={t.label} description={t.hint} />
          ))}
        </div>
      </Field>
      <div className={styles.formRow}>
        <Field label="Temperature" htmlFor="def-temp" hint="0–2. Blank uses the model default.">
          <TextInput
            id="def-temp"
            type="number"
            min={0}
            max={2}
            step={0.1}
            inputMode="decimal"
            value={value.temperature ?? ""}
            onChange={(e) => set({ temperature: e.target.value.trim() === "" ? undefined : Number(e.target.value) })}
            placeholder="default"
          />
        </Field>
        <Field label="Top P" htmlFor="def-topp" hint="0–1. Blank uses the model default.">
          <TextInput
            id="def-topp"
            type="number"
            min={0}
            max={1}
            step={0.05}
            inputMode="decimal"
            value={value.topP ?? ""}
            onChange={(e) => set({ topP: e.target.value.trim() === "" ? undefined : Number(e.target.value) })}
            placeholder="default"
          />
        </Field>
      </div>
      <Field
        label="Memory store"
        htmlFor="def-memory"
        hint="A shared memory store this agent connects to. Tenants are auto-entitled to it when they get this agent."
      >
        <Select id="def-memory" value={value.memoryStore ?? ""} onChange={(e) => set({ memoryStore: e.target.value || undefined })}>
          <option value="">None</option>
          {stores.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </Select>
      </Field>
    </>
  );
}

export function NewAgentModal({
  open,
  pending,
  onClose,
  onSubmit,
  stores,
}: {
  open: boolean;
  pending: boolean;
  onClose: () => void;
  onSubmit: (input: { name: string; description: string; type: AgentType; model: string; definition: AgentDefinition }) => void;
  stores: MemoryStore[];
}) {
  const [type, setType] = useState<AgentType>("prompt");
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [model, setModel] = useState(MODELS[0]);
  const [def, setDef] = useState<AgentDefinition>({});
  const valid = name.trim().length >= 2 && (type === "hosted" ? (def.image ?? "").trim().length > 0 : true);

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="New agent"
      description="Pick a type and author its definition. It starts at v1.0.0; publish more versions any time."
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            variant="primary"
            loading={pending}
            disabled={!valid}
            onClick={() => onSubmit({ name: name.trim(), description: description.trim(), type, model, definition: def })}
          >
            Create agent
          </Button>
        </>
      }
    >
      <Field label="Type">
        <TypeToggle value={type} onChange={setType} />
      </Field>
      <Field label="Name" htmlFor="agent-name" hint="A short, human name — e.g. Contract Reviewer.">
        <TextInput id="agent-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Contract Reviewer" autoFocus />
      </Field>
      <Field label="Description" htmlFor="agent-desc">
        <Textarea id="agent-desc" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="What this agent does, for whom." />
      </Field>
      {type === "prompt" && (
        <Field label="Default model" htmlFor="agent-model">
          <Select id="agent-model" value={model} onChange={(e) => setModel(e.target.value)}>
            {MODELS.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </Select>
        </Field>
      )}
      <DefinitionFields type={type} value={def} onChange={setDef} stores={stores} />
    </Modal>
  );
}

export function PublishModal({
  agent,
  pending,
  onClose,
  onSubmit,
  stores,
}: {
  agent: CatalogAgent | null;
  pending: boolean;
  onClose: () => void;
  onSubmit: (
    agent: CatalogAgent,
    input: { version: string; channel: string; notes: string; rolloutPercent: number; definition: AgentDefinition },
  ) => void;
  stores: MemoryStore[];
}) {
  const latest = agent ? [...agent.versions].sort((a, b) => (a.createdAt < b.createdAt ? 1 : -1))[0] : undefined;
  const [version, setVersion] = useState("");
  const [channel, setChannel] = useState<string>(latest?.channel ?? "stable");
  const [notes, setNotes] = useState("");
  const [rollout, setRollout] = useState(100);
  const [def, setDef] = useState<AgentDefinition>(latest?.definition ?? {});
  const valid = /^\d+\.\d+\.\d+$/.test(version.trim());

  if (!agent) return null;
  return (
    <Modal
      open={!!agent}
      onClose={onClose}
      title={`Publish version — ${agent.name}`}
      description={`Current latest is v${agent.latestVersion}. Prefilled from it; edit the ${agent.type} definition. Rollout gates availability, not auto-apply.`}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            variant="primary"
            loading={pending}
            disabled={!valid}
            onClick={() =>
              onSubmit(agent, { version: version.trim(), channel, notes: notes.trim(), rolloutPercent: rollout, definition: def })
            }
          >
            Publish
          </Button>
        </>
      }
    >
      <Field label="Version" htmlFor="ver" hint="Semantic version, e.g. 1.1.0.">
        <TextInput id="ver" value={version} onChange={(e) => setVersion(e.target.value)} placeholder="1.1.0" autoFocus />
      </Field>
      <div className={styles.formRow}>
        <Field label="Channel" htmlFor="chan">
          <Select id="chan" value={channel} onChange={(e) => setChannel(e.target.value)}>
            <option value="stable">Stable</option>
            <option value="beta">Beta</option>
          </Select>
        </Field>
        <Field label={`Rollout — ${rollout}%`} htmlFor="rollout">
          <input
            id="rollout"
            type="range"
            min={5}
            max={100}
            step={5}
            value={rollout}
            onChange={(e) => setRollout(Number(e.target.value))}
            className={styles.range}
          />
        </Field>
      </div>
      <DefinitionFields type={agent.type} value={def} onChange={setDef} stores={stores} />
      <Field label="Release notes" htmlFor="notes">
        <Textarea id="notes" value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="What changed." />
      </Field>
    </Modal>
  );
}

export function EnableModal({
  agent,
  pending,
  onClose,
  onSubmit,
}: {
  agent: CatalogAgent | null;
  pending: boolean;
  onClose: () => void;
  onSubmit: (agent: CatalogAgent, publishTo: PublishTarget[]) => void;
}) {
  const [targets, setTargets] = useState<Set<PublishTarget>>(new Set(["api"]));
  if (!agent) return null;

  const toggle = (t: PublishTarget) =>
    setTargets((prev) => {
      const next = new Set(prev);
      next.has(t) ? next.delete(t) : next.add(t);
      return next;
    });

  return (
    <Modal
      open={!!agent}
      onClose={onClose}
      title={`Enable ${agent.name}`}
      description={`v${agent.latestVersion} · ${agent.model}. Choose where it publishes; the reconciler brings it live.`}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            variant="primary"
            loading={pending}
            disabled={targets.size === 0}
            onClick={() => onSubmit(agent, [...targets])}
          >
            Enable agent
          </Button>
        </>
      }
    >
      <Field label="Publish targets">
        <div className={styles.targets}>
          {TARGETS.map((t) => (
            <Checkbox
              key={t.id}
              checked={targets.has(t.id)}
              onChange={() => toggle(t.id)}
              label={t.label}
              description={t.hint}
            />
          ))}
        </div>
      </Field>
    </Modal>
  );
}
