"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Database, Pencil, Plus, Trash2 } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/modal";
import { Field, TextInput, Textarea, Checkbox } from "@/components/ui/form";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import {
  createMemoryStore,
  deleteMemoryStore,
  updateMemoryStore,
  type ActionResult,
} from "@/lib/actions";
import type { MemoryStore, MemoryStoreDefinition, Role } from "@/lib/types";
import styles from "./memory-stores-view.module.css";

type Modal2 = { mode: "new" } | { mode: "edit"; store: MemoryStore } | null;

const DEFAULT_DEFINITION: MemoryStoreDefinition = {
  chatModel: "gpt-4o",
  embeddingModel: "text-embedding-3-small",
  userProfileEnabled: true,
  userProfileDetails: "",
  chatSummaryEnabled: true,
  proceduralMemoryEnabled: true,
  ttlSeconds: 0,
};

export function MemoryStoresView({ role, stores }: { role: Role; stores: MemoryStore[] }) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();
  const platform = role === "platform";
  const [modal, setModal] = useState<Modal2>(null);

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

  const manageable = (s: MemoryStore) => (platform ? s.owner === "" : s.owned);
  const scope = (s: MemoryStore): { label: string; tone: "success" | "info" | "neutral" } =>
    s.owned
      ? { label: "Yours", tone: "success" }
      : s.platform
        ? { label: platform ? "Platform" : "Entitled", tone: "info" }
        : { label: "Tenant", tone: "neutral" };

  return (
    <div>
      <PageHeader
        title="Memory stores"
        description={
          platform
            ? "Author shared memory stores, entitle tenants to them from a tenant's page, and reference them from catalog agents."
            : "Create memory stores for your tenant and connect your agents to them. Platform stores you're entitled to appear here too."
        }
        actions={
          <Button variant="primary" icon={Plus} onClick={() => setModal({ mode: "new" })}>
            New store
          </Button>
        }
      />

      {stores.length === 0 ? (
        <div className={styles.panelEmpty}>
          <EmptyState
            icon={Database}
            title="No memory stores yet"
            description={
              platform
                ? "Create a memory store, then entitle tenants to it and reference it from an agent's definition."
                : "Create a memory store, then connect your enabled agents to it from the Agents tab."
            }
            action={
              <Button variant="primary" icon={Plus} onClick={() => setModal({ mode: "new" })}>
                New store
              </Button>
            }
          />
        </div>
      ) : (
        <ul className={styles.list} role="list">
          {stores.map((s) => {
            const sc = scope(s);
            return (
              <li key={s.id} className={styles.row}>
                <div className={styles.rowIcon} aria-hidden>
                  <Database size={17} strokeWidth={2} />
                </div>
                <div className={styles.rowMain}>
                  <div className={styles.rowTop}>
                    <span className={styles.rowName}>{s.name}</span>
                    <StatusBadge tone={sc.tone} label={sc.label} variant="soft" />
                    {platform && s.owner !== "" && s.ownerName && (
                      <span className={styles.count}>owned by {s.ownerName}</span>
                    )}
                  </div>
                  {s.description && <p className={styles.rowDesc}>{s.description}</p>}
                  <DefinitionChips def={s.definition} />
                </div>
                {manageable(s) && (
                  <div className={styles.rowActions}>
                    <Button size="sm" icon={Pencil} onClick={() => setModal({ mode: "edit", store: s })}>
                      Edit
                    </Button>
                    <Button
                      size="sm"
                      variant="danger"
                      icon={Trash2}
                      loading={pending}
                      onClick={() => runAction(() => deleteMemoryStore(s.id), `Deleted ${s.name}`)}
                    >
                      Delete
                    </Button>
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}

      {modal && (
        <StoreModal
          key={modal.mode === "edit" ? modal.store.id : "new"}
          store={modal.mode === "edit" ? modal.store : null}
          pending={pending}
          onClose={() => setModal(null)}
          onSubmit={(input) =>
            modal.mode === "edit"
              ? runAction(
                  () => updateMemoryStore(modal.store.id, { name: input.name, description: input.description }),
                  `Updated ${input.name}`,
                  () => setModal(null),
                )
              : runAction(
                  () =>
                    createMemoryStore({
                      name: input.name,
                      description: input.description,
                      definition: input.definition,
                    }),
                  `Created ${input.name}`,
                  () => setModal(null),
                )
          }
        />
      )}
    </div>
  );
}

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

function DefinitionChips({ def }: { def: MemoryStoreDefinition }) {
  return (
    <div className={styles.chips}>
      <span className={styles.chip} title="Chat model deployment">
        <span className="mono">{def.chatModel}</span>
      </span>
      <span className={styles.chip} title="Embedding model deployment">
        <span className="mono">{def.embeddingModel}</span>
      </span>
      <span className={styles.chip} data-off={!def.userProfileEnabled}>
        Profile
      </span>
      <span className={styles.chip} data-off={!def.chatSummaryEnabled}>
        Summary
      </span>
      <span className={styles.chip} data-off={!def.proceduralMemoryEnabled}>
        Procedural
      </span>
      {def.ttlSeconds > 0 && <span className={styles.chip}>TTL {formatTTL(def.ttlSeconds)}</span>}
    </div>
  );
}

function StoreModal({
  store,
  pending,
  onClose,
  onSubmit,
}: {
  store: MemoryStore | null;
  pending: boolean;
  onClose: () => void;
  onSubmit: (input: { name: string; description: string; definition: MemoryStoreDefinition }) => void;
}) {
  const editing = store !== null;
  const [name, setName] = useState(store?.name ?? "");
  const [description, setDescription] = useState(store?.description ?? "");
  const [def, setDef] = useState<MemoryStoreDefinition>(store?.definition ?? DEFAULT_DEFINITION);

  const patch = (p: Partial<MemoryStoreDefinition>) => setDef((d) => ({ ...d, ...p }));

  const submit = () => onSubmit({ name: name.trim(), description: description.trim(), definition: def });

  return (
    <Modal
      open
      onClose={onClose}
      title={editing ? "Edit memory store" : "New memory store"}
      description={
        editing
          ? "Rename or redescribe this store. Its models and memory settings are fixed once created."
          : "A memory store is a Foundry memory resource agents connect to. Pick the models that process memory and which kinds it captures."
      }
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={pending} disabled={name.trim().length < 2} onClick={submit}>
            {editing ? "Save" : "Create store"}
          </Button>
        </>
      }
    >
      <Field label="Name" htmlFor="ms-name" hint="A short, human name — e.g. Support Memory.">
        <TextInput
          id="ms-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Support Memory"
          autoFocus
        />
      </Field>
      <Field label="Description" htmlFor="ms-desc">
        <Textarea
          id="ms-desc"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="What this memory captures, for which agents."
        />
      </Field>

      {editing ? (
        <ReadonlyDefinition def={def} />
      ) : (
        <>
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
          <div className={styles.grid2}>
            <Field label="Chat model" htmlFor="ms-chat" hint="Foundry chat deployment.">
              <TextInput
                id="ms-chat"
                list="ms-chat-models"
                value={def.chatModel}
                onChange={(e) => patch({ chatModel: e.target.value })}
                placeholder="gpt-4o"
                spellCheck={false}
              />
            </Field>
            <Field label="Embedding model" htmlFor="ms-embed" hint="Foundry embedding deployment.">
              <TextInput
                id="ms-embed"
                list="ms-embed-models"
                value={def.embeddingModel}
                onChange={(e) => patch({ embeddingModel: e.target.value })}
                placeholder="text-embedding-3-small"
                spellCheck={false}
              />
            </Field>
          </div>

          <p className={styles.groupLabel}>What this store remembers</p>
          <Checkbox
            checked={def.userProfileEnabled}
            onChange={(v) => patch({ userProfileEnabled: v })}
            label="User profile"
            description="Durable facts about the user — preferences, context, identity."
          />
          {def.userProfileEnabled && (
            <Field
              label="Profile details"
              htmlFor="ms-profile"
              hint="Optional — narrow which categories to extract."
            >
              <TextInput
                id="ms-profile"
                value={def.userProfileDetails ?? ""}
                onChange={(e) => patch({ userProfileDetails: e.target.value })}
                placeholder="preferences, timezone, communication style"
              />
            </Field>
          )}
          <Checkbox
            checked={def.chatSummaryEnabled}
            onChange={(v) => patch({ chatSummaryEnabled: v })}
            label="Chat summary"
            description="Rolling summaries of the conversation."
          />
          <Checkbox
            checked={def.proceduralMemoryEnabled}
            onChange={(v) => patch({ proceduralMemoryEnabled: v })}
            label="Procedural memory"
            description="Learned procedures and how-to preferences."
          />

          <Field
            label="Retention (seconds)"
            htmlFor="ms-ttl"
            hint={
              def.ttlSeconds > 0
                ? `Memories expire after ${formatTTL(def.ttlSeconds)}.`
                : "0 = memories never expire."
            }
          >
            <TextInput
              id="ms-ttl"
              type="number"
              min={0}
              value={String(def.ttlSeconds)}
              onChange={(e) => patch({ ttlSeconds: Math.max(0, Number(e.target.value) || 0) })}
            />
          </Field>
        </>
      )}
    </Modal>
  );
}

function ReadonlyDefinition({ def }: { def: MemoryStoreDefinition }) {
  const kinds = [
    def.userProfileEnabled && "Profile",
    def.chatSummaryEnabled && "Summary",
    def.proceduralMemoryEnabled && "Procedural",
  ].filter(Boolean) as string[];
  return (
    <Field label="Definition">
      <div className={styles.readonlyDef}>
        <div className={styles.defFact}>
          <span className={styles.defFactKey}>Chat model</span>
          <span className={`${styles.defFactVal} mono`}>{def.chatModel}</span>
        </div>
        <div className={styles.defFact}>
          <span className={styles.defFactKey}>Embedding model</span>
          <span className={`${styles.defFactVal} mono`}>{def.embeddingModel}</span>
        </div>
        <div className={styles.defFact}>
          <span className={styles.defFactKey}>Remembers</span>
          <span className={styles.defFactVal}>{kinds.length ? kinds.join(" · ") : "nothing"}</span>
        </div>
        <div className={styles.defFact}>
          <span className={styles.defFactKey}>Retention</span>
          <span className={styles.defFactVal}>{formatTTL(def.ttlSeconds)}</span>
        </div>
      </div>
      <p className={styles.readonlyNote}>
        Models and memory settings are fixed once created — the Foundry memory store has no update path. To
        change them, create a new store.
      </p>
    </Field>
  );
}
