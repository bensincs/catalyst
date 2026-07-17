"use client";

import Link from "next/link";
import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import {
  AppWindow,
  Bot,
  ChevronRight,
  Globe,
  MessageSquare,
  Plus,
  Power,
} from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button, ButtonLink } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/status";
import { EmptyState } from "@/components/ui/empty-state";
import { useToast } from "@/components/providers/toast-provider";
import { disableAgent, enableAgent, type ActionResult } from "@/lib/actions";
import { EnableModal, OwnershipTag, TypeTag } from "./catalog-view";
import {
  type CatalogAgent,
  type EnabledAgent,
  type MemoryStore,
  type PublishTarget,
  type Role,
} from "@/lib/types";
import { agentStatus } from "@/lib/status";
import styles from "./agents-view.module.css";

const PUBLISH: Record<PublishTarget, { label: string; icon: typeof Globe }> = {
  api: { label: "API", icon: Globe },
  teams: { label: "Teams", icon: MessageSquare },
  m365: { label: "M365", icon: AppWindow },
};

/** One page for everything about agents: the catalog you can author + enable
 *  (available) and the instances running in your tenant (installed) — a single
 *  list where enabled rows carry live health and a link to their detail.
 *  Mirrors the Memory stores and Deployments pages. */
export function AgentsView({
  role,
  agents,
  enabled,
}: {
  role: Role;
  agents: CatalogAgent[];
  enabled: EnabledAgent[];
  memoryStores?: MemoryStore[];
}) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();
  const platform = role === "platform";

  const [enableFor, setEnableFor] = useState<CatalogAgent | null>(null);

  const enabledById = new Map(enabled.map((a) => [a.id, a]));

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

  return (
    <div>
      <PageHeader
        title="Agents"
        description={
          platform
            ? "Author agents and entitle tenants from a tenant's page; each enables what it needs and its reconciler brings it live."
            : "Agents you can enable — entitled by your platform team or authored yourself — and the ones running in your tenant. Enabling reconciles one into your Foundry project."
        }
        actions={
          <ButtonLink href="/agents/new" variant="primary" icon={Plus}>
            New agent
          </ButtonLink>
        }
      />

      {agents.length === 0 ? (
        <div className={styles.panelEmpty}>
          <EmptyState
            icon={Bot}
            title={platform ? "Author your first agent" : "No agents yet"}
            description={
              platform
                ? "Define an agent and its model, then entitle tenants to enable it. The reconciler brings it live in each tenant's own Foundry project."
                : "Author your own agent, or ask your platform team to entitle your tenant to one. Enabled agents are provisioned into your own Foundry project."
            }
            action={
              <ButtonLink href="/agents/new" variant="primary" icon={Plus}>
                New agent
              </ButtonLink>
            }
          />
        </div>
      ) : (
        <ul className={styles.list} role="list">
          {agents.map((a) => {
            const ea = enabledById.get(a.id);
            const isEnabled = a.enabled || Boolean(ea);
            const detailHref = `/agents/${encodeURIComponent(a.id)}`;
            return (
              <li key={a.id} className={styles.row}>
                <div className={styles.rowIcon} aria-hidden>
                  <Bot size={17} strokeWidth={2} />
                </div>
                <div className={styles.rowMain}>
                  <div className={styles.rowTop}>
                    {isEnabled && !platform ? (
                      <Link href={detailHref} className={styles.rowNameLink}>
                        {a.name}
                      </Link>
                    ) : (
                      <span className={styles.rowName}>{a.name}</span>
                    )}
                    <TypeTag type={a.type} />
                    {!platform && <OwnershipTag agent={a} />}
                    {isEnabled &&
                      (ea ? (
                        <StatusBadge
                          tone={agentStatus(ea).tone}
                          label={agentStatus(ea).label}
                          variant="soft"
                          pulse={agentStatus(ea).pulse}
                        />
                      ) : (
                        <StatusBadge tone="success" label="Enabled" variant="soft" />
                      ))}
                    {platform && a.owner !== "" && a.ownerName && (
                      <span className={styles.count}>owned by {a.ownerName}</span>
                    )}
                  </div>
                  {a.description && <p className={styles.rowDesc}>{a.description}</p>}
                  <div className={styles.chips}>
                    <span className={styles.chip}>
                      <span className={styles.metaKey}>model</span> <span className="mono">{a.model}</span>
                    </span>
                    {ea?.publishTo.map((p) => {
                      const Icon = PUBLISH[p].icon;
                      return (
                        <span key={p} className={styles.chip} title={`Published to ${PUBLISH[p].label}`}>
                          <Icon size={12} strokeWidth={2.2} aria-hidden /> {PUBLISH[p].label}
                        </span>
                      );
                    })}
                  </div>
                </div>
                <div className={styles.rowActions}>
                  {!platform &&
                    (isEnabled ? (
                      <Button
                        size="sm"
                        icon={Power}
                        loading={pending}
                        onClick={() => runAction(() => disableAgent(a.id), `Disabled ${a.name}`)}
                      >
                        Disable
                      </Button>
                    ) : (
                      <Button
                        size="sm"
                        variant="primary"
                        icon={Power}
                        loading={pending}
                        onClick={() => setEnableFor(a)}
                      >
                        Enable
                      </Button>
                    ))}
                  {isEnabled && !platform && (
                    <Link href={detailHref} className={styles.manage} aria-label={`Open ${a.name}`}>
                      <ChevronRight size={16} strokeWidth={2} />
                    </Link>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      )}

      <EnableModal
        agent={enableFor}
        pending={pending}
        onClose={() => setEnableFor(null)}
        onSubmit={(agent, publishTo) =>
          runAction(
            () => enableAgent({ catalogAgentId: agent.id, publishTo }),
            `Enabled ${agent.name}`,
            () => setEnableFor(null),
          )
        }
      />
    </div>
  );
}
