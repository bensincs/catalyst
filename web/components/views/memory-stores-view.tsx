"use client";

import { useTransition } from "react";
import { useRouter } from "next/navigation";
import { Database, Pencil, Plus, Power, Trash2 } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button, ButtonLink } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import { deleteMemoryStore, enableStore, disableStore, type ActionResult } from "@/lib/actions";
import { HEALTH_META, type MemoryStore, type MemoryStoreDefinition, type Role } from "@/lib/types";
import styles from "./memory-stores-view.module.css";

export function MemoryStoresView({ role, stores }: { role: Role; stores: MemoryStore[] }) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();
  const platform = role === "platform";

  const runAction = (fn: () => Promise<ActionResult>, success: string) => {
    start(async () => {
      const res = await fn();
      if (res.ok) {
        toast({ title: success, tone: "success" });
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
            : "Create your own memory stores or enable ones you're entitled to — enabling reconciles a store into your project. Connect agents to them from the Agents tab."
        }
        actions={
          <ButtonLink href="/memory-stores/new" variant="primary" icon={Plus}>
            New store
          </ButtonLink>
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
              <ButtonLink href="/memory-stores/new" variant="primary" icon={Plus}>
                New store
              </ButtonLink>
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
                    {!platform && s.enabled && s.health && (
                      <StatusBadge
                        tone={HEALTH_META[s.health].tone}
                        label={HEALTH_META[s.health].label}
                        variant="soft"
                        pulse={s.health === "reconciling"}
                      />
                    )}
                    {platform && s.owner !== "" && s.ownerName && (
                      <span className={styles.count}>owned by {s.ownerName}</span>
                    )}
                  </div>
                  {s.description && <p className={styles.rowDesc}>{s.description}</p>}
                  <DefinitionChips def={s.definition} />
                </div>
                {(manageable(s) || (!platform && (s.owned || s.entitled))) && (
                  <div className={styles.rowActions}>
                    {!platform &&
                      (s.owned || s.entitled) &&
                      (s.enabled ? (
                        <Button
                          size="sm"
                          icon={Power}
                          loading={pending}
                          onClick={() => runAction(() => disableStore(s.id), `Disabled ${s.name}`)}
                        >
                          Disable
                        </Button>
                      ) : (
                        <Button
                          size="sm"
                          variant="primary"
                          icon={Power}
                          loading={pending}
                          onClick={() => runAction(() => enableStore(s.id), `Enabling ${s.name}`)}
                        >
                          Enable
                        </Button>
                      ))}
                    {manageable(s) && (
                      <>
                        <ButtonLink size="sm" icon={Pencil} href={`/memory-stores/${s.id}/edit`}>
                          Edit
                        </ButtonLink>
                        <Button
                          size="sm"
                          variant="danger"
                          icon={Trash2}
                          loading={pending}
                          onClick={() => runAction(() => deleteMemoryStore(s.id), `Deleted ${s.name}`)}
                        >
                          Delete
                        </Button>
                      </>
                    )}
                  </div>
                )}
              </li>
            );
          })}
        </ul>
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
