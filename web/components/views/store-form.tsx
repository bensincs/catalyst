"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Brain, Cpu, Database, Lock } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Field, TextInput, Textarea, Checkbox } from "@/components/ui/form";
import { useToast } from "@/components/providers/toast-provider";
import { FormShell, FormSection } from "./form-shell";
import { createMemoryStore, updateMemoryStore, type ActionResult } from "@/lib/actions";
import type { MemoryStore, MemoryStoreDefinition } from "@/lib/types";
import styles from "./form-shell.module.css";
import ms from "./memory-stores-view.module.css";

const DEFAULT_DEFINITION: MemoryStoreDefinition = {
  chatModel: "gpt-4o",
  embeddingModel: "text-embedding-3-small",
  userProfileEnabled: true,
  userProfileDetails: "",
  chatSummaryEnabled: true,
  proceduralMemoryEnabled: true,
  ttlSeconds: 0,
};

function formatTTL(seconds: number): string {
  if (seconds <= 0) return "never";
  const units: [number, string][] = [
    [86400, "d"],
    [3600, "h"],
    [60, "m"],
  ];
  for (const [size, suffix] of units) {
    if (seconds % size === 0 || seconds >= size) return `${Math.round(seconds / size)}${suffix}`;
  }
  return `${seconds}s`;
}

export function StoreForm({ store }: { store?: MemoryStore }) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();
  const editing = store !== undefined;

  const [name, setName] = useState(store?.name ?? "");
  const [description, setDescription] = useState(store?.description ?? "");
  const [def, setDef] = useState<MemoryStoreDefinition>(store?.definition ?? DEFAULT_DEFINITION);
  const patch = (p: Partial<MemoryStoreDefinition>) => setDef((d) => ({ ...d, ...p }));

  const valid = name.trim().length >= 2;

  const submit = () =>
    start(async () => {
      const res: ActionResult = editing
        ? await updateMemoryStore(store.id, { name: name.trim(), description: description.trim() })
        : await createMemoryStore({ name: name.trim(), description: description.trim(), definition: def });
      if (res.ok) {
        toast({ title: editing ? `Updated ${name.trim()}` : `Created ${name.trim()}`, tone: "success" });
        router.push("/memory-stores");
        router.refresh();
      } else {
        toast({ title: "Couldn't save", description: res.error, tone: "danger" });
      }
    });

  return (
    <FormShell
      backHref="/memory-stores"
      backLabel="Memory stores"
      icon={Database}
      title={editing ? `Edit ${store.name}` : "New memory store"}
      subtitle={
        editing
          ? "Rename or redescribe this store. Its models and memory settings are fixed once created."
          : "A Foundry memory resource agents connect to. Pick the models that process memory and which kinds it captures."
      }
      footer={
        <div className={styles.actions} style={{ marginLeft: "auto" }}>
          <Button onClick={() => router.push("/memory-stores")}>Cancel</Button>
          <Button variant="primary" loading={pending} disabled={!valid} onClick={submit}>
            {editing ? "Save store" : "Create store"}
          </Button>
        </div>
      }
    >
      <FormSection icon={Database} title="Essentials">
        <Field label="Name" htmlFor="ms-name" hint="A short, human name — e.g. Support Memory.">
          <TextInput id="ms-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Support Memory" autoFocus />
        </Field>
        <Field label="Description" htmlFor="ms-desc">
          <Textarea id="ms-desc" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="What this memory captures, for which agents." />
        </Field>
      </FormSection>

      {editing ? (
        <FormSection icon={Lock} title="Configuration" desc="Models and memory settings are fixed once created — the Foundry memory store has no update path.">
          <ReadonlyConfig def={def} />
        </FormSection>
      ) : (
        <div className={styles.columns}>
          <div className={styles.col}>
            <FormSection icon={Cpu} title="Models" desc="Foundry deployments that process memory.">
              <datalist id="ms-chat-models">
                <option value="gpt-4o" />
                <option value="gpt-4o-mini" />
                <option value="gpt-4.1" />
                <option value="gpt-4.1-mini" />
              </datalist>
              <datalist id="ms-embed-models">
                <option value="text-embedding-3-small" />
                <option value="text-embedding-3-large" />
                <option value="text-embedding-ada-002" />
              </datalist>
              <Field label="Chat model" htmlFor="ms-chat" hint="Foundry chat deployment.">
                <TextInput id="ms-chat" list="ms-chat-models" value={def.chatModel} onChange={(e) => patch({ chatModel: e.target.value })} placeholder="gpt-4o" spellCheck={false} />
              </Field>
              <Field label="Embedding model" htmlFor="ms-embed" hint="Foundry embedding deployment.">
                <TextInput id="ms-embed" list="ms-embed-models" value={def.embeddingModel} onChange={(e) => patch({ embeddingModel: e.target.value })} placeholder="text-embedding-3-small" spellCheck={false} />
              </Field>
            </FormSection>
          </div>

          <div className={styles.col}>
            <FormSection icon={Brain} title="What it remembers" desc="Which kinds of memory this store captures.">
              <Checkbox
                checked={def.userProfileEnabled}
                onChange={(v) => patch({ userProfileEnabled: v })}
                label="User profile"
                description="Durable facts about the user — preferences, context, identity."
              />
              {def.userProfileEnabled && (
                <Field label="Profile details" htmlFor="ms-profile" hint="Optional — narrow which categories to extract.">
                  <TextInput id="ms-profile" value={def.userProfileDetails ?? ""} onChange={(e) => patch({ userProfileDetails: e.target.value })} placeholder="preferences, timezone, communication style" />
                </Field>
              )}
              <Checkbox checked={def.chatSummaryEnabled} onChange={(v) => patch({ chatSummaryEnabled: v })} label="Chat summary" description="Rolling summaries of the conversation." />
              <Checkbox checked={def.proceduralMemoryEnabled} onChange={(v) => patch({ proceduralMemoryEnabled: v })} label="Procedural memory" description="Learned procedures and how-to preferences." />
              <Field label="Retention (seconds)" htmlFor="ms-ttl" hint={def.ttlSeconds > 0 ? `Memories expire after ${formatTTL(def.ttlSeconds)}.` : "0 = memories never expire."}>
                <TextInput id="ms-ttl" type="number" min={0} value={String(def.ttlSeconds)} onChange={(e) => patch({ ttlSeconds: Math.max(0, Number(e.target.value) || 0) })} />
              </Field>
            </FormSection>
          </div>
        </div>
      )}
    </FormShell>
  );
}

function ReadonlyConfig({ def }: { def: MemoryStoreDefinition }) {
  const kinds = [
    def.userProfileEnabled && "Profile",
    def.chatSummaryEnabled && "Summary",
    def.proceduralMemoryEnabled && "Procedural",
  ].filter(Boolean) as string[];
  return (
    <div className={ms.readonlyDef}>
      <div className={ms.defFact}>
        <span className={ms.defFactKey}>Chat model</span>
        <span className={`${ms.defFactVal} mono`}>{def.chatModel}</span>
      </div>
      <div className={ms.defFact}>
        <span className={ms.defFactKey}>Embedding model</span>
        <span className={`${ms.defFactVal} mono`}>{def.embeddingModel}</span>
      </div>
      <div className={ms.defFact}>
        <span className={ms.defFactKey}>Remembers</span>
        <span className={ms.defFactVal}>{kinds.length ? kinds.join(" · ") : "nothing"}</span>
      </div>
      <div className={ms.defFact}>
        <span className={ms.defFactKey}>Retention</span>
        <span className={ms.defFactVal}>{formatTTL(def.ttlSeconds)}</span>
      </div>
    </div>
  );
}
