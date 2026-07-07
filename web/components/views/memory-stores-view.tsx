"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Database, Pencil, Plus, Trash2 } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/modal";
import { Field, TextInput, Textarea } from "@/components/ui/form";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import {
  createMemoryStore,
  deleteMemoryStore,
  updateMemoryStore,
  type ActionResult,
} from "@/lib/actions";
import type { MemoryStore, Role } from "@/lib/types";
import styles from "./memory-stores-view.module.css";

type Modal2 = { mode: "new" } | { mode: "edit"; store: MemoryStore } | null;

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
                  <div className={styles.rowMeta}>
                    <span className={styles.metaKey}>config</span>
                    <span className="mono">{configPreview(s.config)}</span>
                  </div>
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
              ? runAction(() => updateMemoryStore(modal.store.id, input), `Updated ${input.name}`, () => setModal(null))
              : runAction(() => createMemoryStore(input), `Created ${input.name}`, () => setModal(null))
          }
        />
      )}
    </div>
  );
}

function configPreview(config: unknown): string {
  try {
    const s = JSON.stringify(config);
    if (!s || s === "{}") return "—";
    return s.length > 60 ? s.slice(0, 60) + "…" : s;
  } catch {
    return "—";
  }
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
  onSubmit: (input: { name: string; description: string; config: unknown }) => void;
}) {
  const [name, setName] = useState(store?.name ?? "");
  const [description, setDescription] = useState(store?.description ?? "");
  const [configText, setConfigText] = useState(
    store && store.config && JSON.stringify(store.config) !== "{}"
      ? JSON.stringify(store.config, null, 2)
      : "",
  );
  const [err, setErr] = useState<string | null>(null);

  const submit = () => {
    let config: unknown = {};
    if (configText.trim() !== "") {
      try {
        config = JSON.parse(configText);
      } catch (e) {
        setErr((e as Error).message);
        return;
      }
    }
    onSubmit({ name: name.trim(), description: description.trim(), config });
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={store ? "Edit memory store" : "New memory store"}
      description="A memory store is a Foundry memory configuration agents share. Author its config as JSON; the reconciler forwards it to each connected agent's memory."
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" loading={pending} disabled={name.trim().length < 2} onClick={submit}>
            {store ? "Save" : "Create store"}
          </Button>
        </>
      }
    >
      <Field label="Name" htmlFor="ms-name" hint="A short, human name — e.g. Support Memory.">
        <TextInput id="ms-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Support Memory" autoFocus />
      </Field>
      <Field label="Description" htmlFor="ms-desc">
        <Textarea id="ms-desc" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="What this memory captures, for which agents." />
      </Field>
      <Field
        label="Config (JSON)"
        htmlFor="ms-config"
        hint={err ? `Invalid JSON: ${err}` : "The Foundry memory definition, forwarded verbatim. Blank = empty config."}
      >
        <Textarea
          id="ms-config"
          value={configText}
          spellCheck={false}
          onChange={(e) => {
            setConfigText(e.target.value);
            setErr(null);
          }}
          placeholder='{ "scope": "user" }'
        />
      </Field>
    </Modal>
  );
}
