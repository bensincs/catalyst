"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { KeyRound, Plus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Field, TextInput, Textarea } from "@/components/ui/form";
import { useToast } from "@/components/providers/toast-provider";
import { FormShell, FormSection } from "./form-shell";
import { createSecretSet, updateSecretSet, type ActionResult } from "@/lib/actions";
import { secretSetName, type SecretSet } from "@/lib/types";
import styles from "./form-shell.module.css";
import ss from "./secret-set-form.module.css";

/** Key names must survive being both a Kubernetes Secret data key and part of an
 *  Azure Key Vault secret name. The intersection is narrower than either alone,
 *  so it is enforced at authoring time rather than discovered at deploy time. */
const KEY_PATTERN = /^[a-zA-Z0-9._-]{1,200}$/;

export function SecretSetForm({ set }: { set?: SecretSet }) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();
  const editing = set !== undefined;

  const [name, setName] = useState(set?.name ?? "");
  const [description, setDescription] = useState(set?.description ?? "");
  const [keys, setKeys] = useState<string[]>(set?.keys ?? []);
  const [draft, setDraft] = useState("");

  const duplicate = keys.includes(draft.trim());
  const malformed = draft.trim() !== "" && !KEY_PATTERN.test(draft.trim());
  const canAdd = draft.trim() !== "" && !duplicate && !malformed;

  const addKey = () => {
    if (!canAdd) return;
    setKeys((k) => [...k, draft.trim()]);
    setDraft("");
  };

  const valid = name.trim().length >= 2 && keys.length > 0;

  const submit = () =>
    start(async () => {
      const payload = { name: name.trim(), description: description.trim(), keys };
      const res: ActionResult = editing
        ? await updateSecretSet(set.id, payload)
        : await createSecretSet(payload);
      if (res.ok) {
        toast({
          title: editing ? `Updated ${name.trim()}` : `Created ${name.trim()}`,
          tone: "success",
        });
        router.push("/secret-stores");
        router.refresh();
      } else {
        toast({ title: "Couldn't save", description: res.error, tone: "danger" });
      }
    });

  const removed = editing ? (set.keys ?? []).filter((k) => !keys.includes(k)) : [];

  return (
    <FormShell
      backHref="/secret-stores"
      backLabel="Secret stores"
      icon={KeyRound}
      title={editing ? `Edit ${set.name}` : "New secret store"}
      subtitle="Declare the keys a deployment needs. You name the keys; each tenant supplies its own values, into its own vault."
      footer={
        <div className={styles.actions} style={{ marginLeft: "auto" }}>
          <Button onClick={() => router.push("/secret-stores")}>Cancel</Button>
          <Button variant="primary" loading={pending} disabled={!valid} onClick={submit}>
            {editing ? "Save secret store" : "Create secret store"}
          </Button>
        </div>
      }
    >
      <FormSection
        icon={KeyRound}
        title="Identity"
        desc="What this set of secrets is for, so a tenant filling it in knows what it is being asked for."
      >
        <Field label="Name" htmlFor="ss-name">
          <TextInput
            id="ss-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Database credentials"
          />
        </Field>
        <Field label="Description" htmlFor="ss-desc">
          <Textarea
            id="ss-desc"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="The admin login the todo app uses to reach its Postgres server."
            rows={2}
          />
        </Field>
      </FormSection>

      <FormSection
        icon={KeyRound}
        title="Keys"
        desc="The names only — never values. A tenant supplies its own values when it enables this, and they go straight to that tenant's Azure Key Vault."
      >
        <Field
          label="Add a key"
          htmlFor="ss-key"
          hint={
            duplicate
              ? "That key is already declared."
              : malformed
                ? "Use only letters, numbers, dots, dashes and underscores."
                : "Letters, numbers, dots, dashes and underscores."
          }
        >
          <div className={ss.addRow}>
            <TextInput
              id="ss-key"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  addKey();
                }
              }}
              placeholder="password"
              spellCheck={false}
              autoComplete="off"
              className="mono"
            />
            <Button icon={Plus} onClick={addKey} disabled={!canAdd}>
              Add
            </Button>
          </div>
        </Field>

        {keys.length > 0 ? (
          <ul className={ss.keys} role="list">
            {keys.map((k) => (
              <li key={k} className={ss.keyChip}>
                <span className="mono">{k}</span>
                <button
                  type="button"
                  onClick={() => setKeys((ks) => ks.filter((x) => x !== k))}
                  aria-label={`Remove ${k}`}
                  className={ss.remove}
                >
                  <X size={13} strokeWidth={2.4} aria-hidden />
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <p className={ss.empty}>
            No keys yet. A secret store needs at least one before a deployment can use it.
          </p>
        )}

        {removed.length > 0 && (
          <p className={ss.removedNote}>
            Removing {removed.map((k) => k).join(", ")} stops{" "}
            {removed.length === 1 ? "it" : "them"} being delivered to any deployment. Values
            already stored in a tenant&rsquo;s vault are left alone — they cannot be read back
            here to be restored if this was a mistake.
          </p>
        )}
      </FormSection>

      <FormSection
        icon={KeyRound}
        title="How a chart uses it"
        desc="Values are delivered as a Kubernetes Secret, not merged into a deployment's Helm values — so they never appear in the deployment's configuration."
      >
        <p className={ss.usage}>
          Deployments that depend on this receive a Secret named{" "}
          <span className="mono">{secretSetName(set?.id ?? "«id»")}</span>, with one entry per key.
          In the deployment&rsquo;s values, bind that name to whatever the chart calls its existing
          secret option — for example <span className="mono">auth.existingSecret</span>.
        </p>
      </FormSection>
    </FormShell>
  );
}
