"use client";

import { useState, useTransition } from "react";
import { Boxes, Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Field, TextInput } from "@/components/ui/form";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import { saveUpstream, removeUpstream } from "@/lib/actions";
import type { Upstream } from "@/lib/api";
import styles from "./upstreams-panel.module.css";

/** Private registries mirrored into the platform registry.
 *
 *  A private chart or module is cached here rather than pulled from its upstream
 *  by each tenant, so the upstream credential stays inside the platform: it is
 *  held in Key Vault, read only by the registry, and never handed to a tenant —
 *  which pulls the cached copy with its own scoped token instead.
 *
 *  The registry is the source of truth, so this reads and writes Azure directly.
 *  Nothing is stored here to fall out of step with it. */
export function UpstreamsPanel({
  registry,
  upstreams,
}: {
  registry: string;
  upstreams: Upstream[];
}) {
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [source, setSource] = useState("");
  const [target, setTarget] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [pending, start] = useTransition();
  const { toast } = useToast();

  const reset = () => {
    setAdding(false);
    setName("");
    setSource("");
    setTarget("");
    setUsername("");
    setPassword("");
  };

  // A rule needs somewhere to cache to; deriving it from the source is right
  // almost always, so offer it rather than making it another thing to type.
  const suggestTarget = (src: string) => {
    const path = src.replace(/^oci:\/\//, "").split("/").slice(1).join("/");
    if (path === "") return "";
    return path.endsWith("/*") ? path : path.replace(/\/?\*?$/, "") + "/*";
  };

  // The registry accepts an exact source and then refuses every pull with 403,
  // so the wildcard form is the only one that works.
  const wildcards = source.trim().endsWith("/*") && target.trim().endsWith("/*");
  const valid = name.trim() !== "" && source.trim() !== "" && target.trim() !== "" && wildcards;

  const submit = () =>
    start(async () => {
      const r = await saveUpstream({
        name: name.trim(),
        source: source.trim(),
        target: target.trim(),
        username: username.trim(),
        password,
      });
      if (r.ok) {
        toast({ title: `Upstream ${name.trim()} saved`, tone: "success" });
        reset();
      } else {
        toast({ title: "Couldn't save upstream", description: r.error, tone: "danger" });
      }
    });

  const remove = (n: string) =>
    start(async () => {
      const r = await removeUpstream(n);
      if (r.ok) toast({ title: `Upstream ${n} removed`, tone: "success" });
      else toast({ title: "Couldn't remove upstream", description: r.error, tone: "danger" });
    });

  if (registry === "") {
    return (
      <p className={styles.empty}>
        No platform registry is configured, so private charts and modules cannot be mirrored.
        Cross-tenant provisioning must be on for the control plane to manage one.
      </p>
    );
  }

  return (
    <div className={styles.wrap}>
      <p className={styles.intro}>
        Authors reference <span className="mono">{registry}</span> for private charts and modules;
        public registries are still referenced directly. The upstream credential stays here — a
        tenant pulls the cached copy with its own scoped token and never sees it.
      </p>

      {upstreams.length > 0 ? (
        <ul className={styles.list}>
          {upstreams.map((u) => (
            <li key={u.name} className={styles.row}>
              <div className={styles.rowMain}>
                <span className={styles.rowName}>{u.name}</span>
                <span className={`${styles.rowPath} mono`}>
                  {u.source} <span className={styles.arrow}>→</span> {registry}/{u.target}
                </span>
              </div>
              <StatusBadge
                tone={u.credentialed ? "success" : "neutral"}
                label={u.credentialed ? "authenticated" : "anonymous"}
                variant="soft"
              />
              <button
                type="button"
                className={styles.remove}
                aria-label={`Remove upstream ${u.name}`}
                disabled={pending}
                onClick={() => remove(u.name)}
              >
                <Trash2 size={15} strokeWidth={2.2} />
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <p className={styles.empty}>
          No upstreams yet. Add one to mirror a private registry — for example{" "}
          <span className="mono">ghcr.io/acme/charts/*</span>.
        </p>
      )}

      {adding ? (
        <div className={styles.form}>
          <div className={styles.grid2}>
            <Field label="Name" htmlFor="up-name" hint="Identifies the rule on the registry.">
              <TextInput
                id="up-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="ghcr-charts"
                spellCheck={false}
              />
            </Field>
            <Field
              label="Upstream"
              htmlFor="up-src"
              hint="Repository pattern to mirror — must end with /*."
            >
              <TextInput
                id="up-src"
                value={source}
                onChange={(e) => {
                  setSource(e.target.value);
                  if (target === "" || target === suggestTarget(source)) {
                    setTarget(suggestTarget(e.target.value));
                  }
                }}
                placeholder="ghcr.io/acme/charts/*"
                spellCheck={false}
              />
            </Field>
          </div>
          <Field
            label="Cached as"
            htmlFor="up-tgt"
            hint={`Where it appears on ${registry}. Authors reference this path.`}
          >
            <TextInput
              id="up-tgt"
              value={target}
              onChange={(e) => setTarget(e.target.value)}
              placeholder="charts/*"
              spellCheck={false}
            />
          </Field>
          <div className={styles.grid2}>
            <Field label="Username" htmlFor="up-user" hint="Leave blank if the upstream is public.">
              <TextInput
                id="up-user"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="github-username"
                spellCheck={false}
                autoComplete="off"
              />
            </Field>
            <Field
              label="Token"
              htmlFor="up-pass"
              hint="Stored in Key Vault, read only by the registry."
            >
              <TextInput
                id="up-pass"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="ghp_…"
                autoComplete="new-password"
              />
            </Field>
          </div>
          <div className={styles.actions}>
            <Button variant="secondary" size="sm" onClick={reset} disabled={pending}>
              Cancel
            </Button>
            <Button size="sm" onClick={submit} disabled={!valid || pending} loading={pending}>
              Save upstream
            </Button>
          </div>
        </div>
      ) : (
        <Button
          variant="secondary"
          size="sm"
          icon={Plus}
          onClick={() => setAdding(true)}
          disabled={pending}
        >
          Add upstream
        </Button>
      )}
    </div>
  );
}

export const UpstreamsIcon = Boxes;
