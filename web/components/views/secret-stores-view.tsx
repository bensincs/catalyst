"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { KeyRound, Pencil, Plus, Power, Settings2 } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { ConfirmDelete } from "./confirm-delete";
import { SecretValuesDialog } from "./secret-values-dialog";
import { Button, ButtonLink } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import {
  deleteSecretSet,
  disableSecretSet,
  enableSecretSet,
  type ActionResult,
} from "@/lib/actions";
import {
  outstandingKeys,
  secretSetName,
  type Role,
  type SecretSet,
} from "@/lib/types";
import { secretSetStatus } from "@/lib/status";
import shared from "./memory-stores-view.module.css";
import styles from "./secret-stores-view.module.css";

export function SecretStoresView({
  role,
  sets,
}: {
  role: Role;
  sets: SecretSet[];
}) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();
  const [editing, setEditing] = useState<SecretSet | null>(null);
  const platform = role === "platform";

  const runAction = (fn: () => Promise<ActionResult>, success: string) => {
    start(async () => {
      const res = await fn();
      if (res.ok) {
        toast({ title: success, tone: "success" });
        router.refresh();
      } else {
        toast({
          title: "Couldn't complete that",
          description: res.error,
          tone: "danger",
        });
      }
    });
  };

  const manageable = (s: SecretSet) => (platform ? s.owner === "" : s.owned);
  const usable = (s: SecretSet) => !platform && (s.owned || s.entitled);
  const scope = (s: SecretSet): { label: string; tone: "info" | "neutral" } =>
    s.owned
      ? { label: "Yours", tone: "neutral" }
      : s.platform
        ? { label: platform ? "Platform" : "Entitled", tone: "info" }
        : { label: "Tenant", tone: "neutral" };

  return (
    <div>
      <PageHeader
        title="Secret stores"
        description={
          platform
            ? "Declare the secrets a deployment needs — the key names only. Each tenant supplies its own values, into its own Azure Key Vault, where nobody here can read them."
            : "Supply the secrets your deployments need. Values go to your tenant's own Azure Key Vault and cannot be read back by anyone, including your platform administrator."
        }
        actions={
          <ButtonLink href="/secret-stores/new" variant="primary" icon={Plus}>
            New secret store
          </ButtonLink>
        }
      />

      {sets.length === 0 ? (
        <div className={shared.panelEmpty}>
          <EmptyState
            icon={KeyRound}
            title="No secret stores yet"
            description={
              platform
                ? "Declare the keys a deployment needs — a database password, an API key — then entitle tenants to fill them in."
                : "A secret store holds the credentials your deployments need. Create one, or ask your platform administrator to entitle you to theirs."
            }
            action={
              <ButtonLink
                href="/secret-stores/new"
                variant="primary"
                icon={Plus}
              >
                New secret store
              </ButtonLink>
            }
          />
        </div>
      ) : (
        <ul className={shared.list} role="list">
          {sets.map((s) => {
            const sc = scope(s);
            const missing = outstandingKeys(s);
            const status = s.enabled ? secretSetStatus(s) : null;
            return (
              <li key={s.id} className={shared.row}>
                <div className={shared.rowIcon} aria-hidden>
                  <KeyRound size={17} strokeWidth={2} />
                </div>
                <div className={shared.rowMain}>
                  <div className={shared.rowTop}>
                    <span className={shared.rowName}>{s.name}</span>
                    <StatusBadge
                      tone={sc.tone}
                      label={sc.label}
                      variant="soft"
                    />
                    {status && (
                      <StatusBadge
                        tone={status.tone}
                        label={status.label}
                        variant="soft"
                        pulse={status.pulse}
                      />
                    )}
                    {platform && s.owner !== "" && s.ownerName && (
                      <span className={shared.count}>
                        owned by {s.ownerName}
                      </span>
                    )}
                  </div>
                  {s.description && (
                    <p className={shared.rowDesc}>{s.description}</p>
                  )}

                  {/* Keys are the whole substance of a secret store, so they are
                      the row's content rather than a detail behind a click. For
                      a tenant each key also carries whether it has a value —
                      the one fact that decides whether a deployment can run. */}
                  <div className={shared.chips}>
                    {s.keys.map((k) => {
                      const filled = (s.keysSet ?? []).includes(k);
                      return (
                        <span
                          key={k}
                          className={styles.key}
                          data-filled={
                            !platform && s.enabled ? filled : undefined
                          }
                          title={
                            platform || !s.enabled
                              ? undefined
                              : filled
                                ? "A value is stored for this key"
                                : "This key still needs a value"
                          }
                        >
                          <span className="mono">{k}</span>
                        </span>
                      );
                    })}
                    {s.keys.length === 0 && (
                      <span className={shared.chip}>no keys declared</span>
                    )}
                  </div>

                  {usable(s) && s.enabled && missing.length > 0 && (
                    <p className={styles.needs}>
                      {missing.length} key{missing.length === 1 ? "" : "s"}{" "}
                      still need
                      {missing.length === 1 ? "s" : ""} a value. Deployments
                      that depend on this wait until then.
                    </p>
                  )}
                  {usable(s) && s.enabled && missing.length === 0 && (
                    <p className={styles.mounted}>
                      Delivered as{" "}
                      <span className="mono">{secretSetName(s.id)}</span> to the
                      deployments that depend on it.
                    </p>
                  )}
                  {usable(s) && !s.enabled && (s.keysSet?.length ?? 0) > 0 && (
                    // Disabling stops delivery but leaves the values in the
                    // tenant's own vault — Cortex cannot delete them, for the
                    // same reason it cannot read them. Saying so is what stops
                    // "disabled" being mistaken for "destroyed".
                    <p className={styles.mounted}>
                      Values you supplied earlier are still in your vault, so
                      enabling this again won&rsquo;t ask for them twice.
                    </p>
                  )}
                </div>

                {(manageable(s) || usable(s)) && (
                  <div className={shared.rowActions}>
                    {usable(s) &&
                      (s.enabled ? (
                        <>
                          <Button
                            size="sm"
                            variant="secondary"
                            icon={Settings2}
                            loading={pending}
                            onClick={() => setEditing(s)}
                          >
                            {missing.length > 0
                              ? "Add values"
                              : "Update values"}
                          </Button>
                          <Button
                            size="sm"
                            icon={Power}
                            loading={pending}
                            onClick={() =>
                              runAction(
                                () => disableSecretSet(s.id),
                                `Disabled ${s.name}`,
                              )
                            }
                          >
                            Disable
                          </Button>
                        </>
                      ) : (
                        <Button
                          size="sm"
                          variant="primary"
                          icon={Power}
                          loading={pending}
                          onClick={() => setEditing(s)}
                        >
                          Enable
                        </Button>
                      ))}
                    {manageable(s) && (
                      <>
                        <ButtonLink
                          size="sm"
                          variant="ghost"
                          icon={Pencil}
                          href={`/secret-stores/${s.id}/edit`}
                        >
                          Edit
                        </ButtonLink>
                        <ConfirmDelete
                          kind="secret_set"
                          id={s.id}
                          name={s.name}
                          noun="secret store"
                          pending={pending}
                          onConfirm={() =>
                            runAction(
                              () => deleteSecretSet(s.id),
                              `Deleted ${s.name}`,
                            )
                          }
                        />
                      </>
                    )}
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}

      {editing && (
        <SecretValuesDialog
          set={editing}
          open
          pending={pending}
          onClose={() => setEditing(null)}
          onSubmit={(values) =>
            runAction(
              () => enableSecretSet(editing.id, values),
              editing.enabled
                ? `Updated ${editing.name}`
                : `Enabled ${editing.name}`,
            )
          }
        />
      )}
    </div>
  );
}
