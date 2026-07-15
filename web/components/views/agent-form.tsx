"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Bot, SlidersHorizontal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Field, TextInput, Textarea, Select } from "@/components/ui/form";
import { useToast } from "@/components/providers/toast-provider";
import { FormShell, FormSection } from "./form-shell";
import { TypeToggle, DefinitionFields, MODELS } from "./catalog-view";
import { createCatalogAgent, type ActionResult } from "@/lib/actions";
import type { AgentDefinition, AgentType, MemoryStore } from "@/lib/types";
import styles from "./form-shell.module.css";

export function AgentForm({ stores }: { stores: MemoryStore[] }) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();

  const [type, setType] = useState<AgentType>("prompt");
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [model, setModel] = useState(MODELS[0]);
  const [def, setDef] = useState<AgentDefinition>({});

  const valid = name.trim().length >= 2 && (type === "hosted" ? (def.image ?? "").trim().length > 0 : true);

  const submit = () =>
    start(async () => {
      const res: ActionResult = await createCatalogAgent({
        name: name.trim(),
        description: description.trim(),
        type,
        model,
        definition: def,
      });
      if (res.ok) {
        toast({ title: `Created ${name.trim()}`, tone: "success" });
        router.push("/agents");
        router.refresh();
      } else {
        toast({ title: "Couldn't save", description: res.error, tone: "danger" });
      }
    });

  return (
    <FormShell
      backHref="/agents"
      backLabel="Agents"
      icon={Bot}
      title="New agent"
      subtitle="Pick a type and author its definition. It starts at v1.0.0; publish more versions any time."
      footer={
        <>
          <div className={styles.summary}>
            <span className={styles.sumItem}>{type === "hosted" ? "Hosted" : "Prompt"}</span>
            {type === "prompt" && (
              <span className={styles.sumItem}>
                <span className="mono">{model}</span>
              </span>
            )}
          </div>
          <div className={styles.actions}>
            <Button onClick={() => router.push("/agents")}>Cancel</Button>
            <Button variant="primary" loading={pending} disabled={!valid} onClick={submit}>
              Create agent
            </Button>
          </div>
        </>
      }
    >
      <div className={styles.columns}>
        <div className={styles.col}>
          <FormSection icon={Bot} title="Agent" desc="How it's identified and, for prompt agents, its default model.">
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
          </FormSection>
        </div>

        <div className={styles.col}>
          <FormSection icon={SlidersHorizontal} title="Definition" desc="The substance that travels with each published version.">
            <DefinitionFields type={type} value={def} onChange={setDef} stores={stores} />
          </FormSection>
        </div>
      </div>
    </FormShell>
  );
}
