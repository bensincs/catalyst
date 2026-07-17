"use client";

import { useTransition } from "react";
import { useRouter } from "next/navigation";
import { Boxes, Cloud, GitBranch, Pencil, Plus, Power, Trash2 } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button, ButtonLink } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import {
  deleteInfrastructure,
  enableInfrastructure,
  disableInfrastructure,
  type ActionResult,
} from "@/lib/actions";
import { type Infrastructure, type Role } from "@/lib/types";
import { infraStatus } from "@/lib/status";
import styles from "./memory-stores-view.module.css";

export function InfrastructureView({
  role,
  infrastructure,
}: {
  role: Role;
  infrastructure: Infrastructure[];
}) {
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

  const manageable = (i: Infrastructure) => (platform ? i.owner === "" : i.owned);
  const scope = (i: Infrastructure): { label: string; tone: "success" | "info" | "neutral" } =>
    i.owned
      ? { label: "Yours", tone: "success" }
      : i.platform
        ? { label: platform ? "Platform" : "Entitled", tone: "info" }
        : { label: "Tenant", tone: "neutral" };

  return (
    <div>
      <PageHeader
        title="Infrastructure"
        description={
          platform
            ? "Author Azure (Bicep) infrastructure modules, entitle tenants to them from a tenant's page, and let each tenant provision them into its own subscription."
            : "Create your own infrastructure or enable ones you're entitled to — enabling provisions the Bicep module into your subscription and keeps it converged."
        }
        actions={
          <ButtonLink href="/infrastructure/new" variant="primary" icon={Plus}>
            New infrastructure
          </ButtonLink>
        }
      />

      {infrastructure.length === 0 ? (
        <div className={styles.panelEmpty}>
          <EmptyState
            icon={Boxes}
            title="No infrastructure yet"
            description={
              platform
                ? "Author an Azure (Bicep) module, then entitle tenants to it from their page."
                : "Create infrastructure or enable one you're entitled to — it provisions into your subscription via the control plane."
            }
            action={
              <ButtonLink href="/infrastructure/new" variant="primary" icon={Plus}>
                New infrastructure
              </ButtonLink>
            }
          />
        </div>
      ) : (
        <ul className={styles.list} role="list">
          {infrastructure.map((i) => {
            const sc = scope(i);
            return (
              <li key={i.id} className={styles.row}>
                <div className={styles.rowIcon} aria-hidden>
                  <Boxes size={17} strokeWidth={2} />
                </div>
                <div className={styles.rowMain}>
                  <div className={styles.rowTop}>
                    <span className={styles.rowName}>{i.name}</span>
                    <StatusBadge tone={sc.tone} label={sc.label} variant="soft" />
                    {!platform && i.enabled && (
                      <StatusBadge
                        tone={infraStatus(i.infraState).tone}
                        label={infraStatus(i.infraState).label}
                        variant="soft"
                        pulse={infraStatus(i.infraState).pulse}
                      />
                    )}
                    {platform && i.owner !== "" && i.ownerName && (
                      <span className={styles.count}>owned by {i.ownerName}</span>
                    )}
                  </div>
                  {i.description && <p className={styles.rowDesc}>{i.description}</p>}
                  <DefinitionChips infra={i} />
                </div>
                {(manageable(i) || (!platform && (i.owned || i.entitled))) && (
                  <div className={styles.rowActions}>
                    {!platform &&
                      (i.owned || i.entitled) &&
                      (i.enabled ? (
                        <Button
                          size="sm"
                          icon={Power}
                          loading={pending}
                          onClick={() => runAction(() => disableInfrastructure(i.id), `Disabled ${i.name}`)}
                        >
                          Disable
                        </Button>
                      ) : (
                        <Button
                          size="sm"
                          variant="primary"
                          icon={Power}
                          loading={pending}
                          onClick={() => runAction(() => enableInfrastructure(i.id), `Provisioning ${i.name}`)}
                        >
                          Enable
                        </Button>
                      ))}
                    {manageable(i) && (
                      <>
                        <ButtonLink size="sm" icon={Pencil} href={`/infrastructure/${i.id}/edit`}>
                          Edit
                        </ButtonLink>
                        <Button
                          size="sm"
                          variant="danger"
                          icon={Trash2}
                          loading={pending}
                          onClick={() => runAction(() => deleteInfrastructure(i.id), `Deleted ${i.name}`)}
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

function DefinitionChips({ infra }: { infra: Infrastructure }) {
  const module = (infra.bicepModule ?? "").trim();
  const outputs = infra.bicepOutputs.length;
  return (
    <div className={styles.chips}>
      {module ? (
        <span className={styles.chip} title={`Bicep module: ${module}`}>
          <Cloud size={12} strokeWidth={2.2} /> <span className="mono">{module}</span>
        </span>
      ) : (
        <span className={styles.chip} data-off="true">
          <Cloud size={12} strokeWidth={2.2} /> no module
        </span>
      )}
      {outputs > 0 && (
        <span className={styles.chip} title="Wireable Bicep outputs">
          {outputs} output{outputs === 1 ? "" : "s"}
        </span>
      )}
      {infra.dependencies.length > 0 && (
        <span className={styles.chip} title="Provisions after its dependencies">
          <GitBranch size={12} strokeWidth={2.2} /> {infra.dependencies.length} dep
          {infra.dependencies.length === 1 ? "" : "s"}
        </span>
      )}
    </div>
  );
}
