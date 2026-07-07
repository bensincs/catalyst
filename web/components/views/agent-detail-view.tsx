"use client";

import { useTransition } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { AppWindow, ArrowLeft, ArrowRight, Ban, Globe, MessageSquare, RefreshCw } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button } from "@/components/ui/button";
import { StatusBadge, StatusDot } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import { disableAgent } from "@/lib/actions";
import { formatInt, formatRelative } from "@/lib/format";
import { HEALTH_META, type EnabledAgent, type PublishTarget } from "@/lib/types";
import styles from "./agent-detail-view.module.css";

const PUBLISH: Record<PublishTarget, { label: string; icon: typeof Globe }> = {
  api: { label: "API", icon: Globe },
  teams: { label: "Teams", icon: MessageSquare },
  m365: { label: "M365", icon: AppWindow },
};

export function AgentDetailView({
  agent,
  live,
  lastHeartbeatMs,
  now,
}: {
  agent: EnabledAgent;
  live: boolean;
  lastHeartbeatMs: number;
  now: number;
}) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, startTransition] = useTransition();
  const h = HEALTH_META[agent.health];

  const disable = () =>
    startTransition(async () => {
      const res = await disableAgent(agent.id);
      if (res.ok) {
        toast({ title: `Disabled ${agent.name}`, tone: "success" });
        router.push("/agents");
      } else {
        toast({ title: "Couldn't disable", description: res.error, tone: "danger" });
      }
    });

  return (
    <div>
      <Link href="/agents" className={styles.back}>
        <ArrowLeft size={14} strokeWidth={2.2} aria-hidden />
        Agents
      </Link>

      <PageHeader
        title={agent.name}
        description={`Running under your tenant's identity in your own Foundry project · ${agent.model}`}
        meta={<StatusBadge tone={h.tone} label={h.label} pulse={agent.health === "reconciling"} />}
        actions={
          <Button variant="secondary" icon={Ban} loading={pending} onClick={disable}>
            Disable
          </Button>
        }
      />

      {/* Reconciliation — desired vs. actual, made legible */}
      <section className={styles.reconcile} aria-label="Reconciliation">
        <div className={styles.reconHead}>
          <span className={styles.reconTitle}>Reconciliation</span>
          {agent.drift ? (
            <span className={styles.driftTag} data-tone="info">
              <RefreshCw size={12} strokeWidth={2.4} aria-hidden />
              Converging
            </span>
          ) : (
            <span className={styles.driftTag} data-tone="success">
              <StatusDot tone="success" />
              In sync
            </span>
          )}
        </div>

        {/* Version — the axis reconciliation actually moves along */}
        <div className={styles.versionRow}>
          <div className={styles.versionCol} data-role="actual">
            <span className={styles.versionLabel}>Actual</span>
            <span className={"mono " + styles.versionValue}>v{agent.version}</span>
            <span className={styles.versionSub}>{live ? "reported by reconciler" : "last reported"}</span>
          </div>
          <span className={styles.versionArrow} data-drift={agent.drift || undefined} aria-hidden>
            <ArrowRight size={18} strokeWidth={2.2} />
          </span>
          <div className={styles.versionCol} data-role="desired" data-drift={agent.drift || undefined}>
            <span className={styles.versionLabel}>Desired</span>
            <span className={"mono " + styles.versionValue}>v{agent.desiredVersion}</span>
            <span className={styles.versionSub}>latest on {agent.channel}</span>
          </div>
        </div>

        <p className={styles.reconNote}>
          {agent.drift
            ? `A newer version is published. The reconciler is converging this agent to v${agent.desiredVersion} on its next poll.`
            : live
              ? `Desired and actual match. The reconciler confirmed v${agent.version} on its last heartbeat.`
              : `Desired and actual match as last reported. The reconciler isn't live, so this state is unconfirmed.`}
        </p>

        <dl className={styles.facts}>
          <Fact label="Channel" value={agent.channel === "beta" ? "Beta" : "Stable"} />
          <Fact label="Model" value={agent.model} mono />
          <Fact
            label="Health"
            valueNode={<StatusBadge tone={h.tone} label={h.label} pulse={agent.health === "reconciling"} variant="soft" />}
          />
          <Fact label="Calls · 30d" value={formatInt(agent.calls30d)} />
          <Fact label="Last heartbeat" value={live || lastHeartbeatMs ? formatRelative(lastHeartbeatMs, now) : "—"} />
        </dl>
      </section>

      {/* Definition — the agent's substance (prompt or hosted) */}
      <section className={styles.defSection} aria-label="Definition">
        <div className={styles.defHead}>
          <h2 className={styles.sectionTitle}>Definition</h2>
          <span className={styles.typeTag} data-type={agent.type}>
            {agent.type === "hosted" ? "Hosted" : "Prompt"}
          </span>
        </div>
        {agent.type === "hosted" ? (
          <dl className={styles.facts}>
            <Fact label="Image" value={agent.definition.image || "—"} mono />
            <Fact label="Endpoint" value={agent.definition.endpoint || "—"} mono />
            <Fact label="CPU" value={agent.definition.cpu || "—"} />
            <Fact label="Memory" value={agent.definition.memory || "—"} />
          </dl>
        ) : (
          <>
            <div className={styles.defBlock}>
              <span className={styles.defLabel}>Instructions</span>
              <p className={styles.instructions}>{agent.definition.instructions || "—"}</p>
            </div>
            <div className={styles.defBlock}>
              <span className={styles.defLabel}>Tools</span>
              {agent.definition.tools && agent.definition.tools.length > 0 ? (
                <div className={styles.toolChips}>
                  {agent.definition.tools.map((t) => (
                    <span key={t} className={"mono " + styles.toolChip}>{t}</span>
                  ))}
                </div>
              ) : (
                <span className={styles.defNone}>None</span>
              )}
            </div>
            <div className={styles.defBlock}>
              <span className={styles.defLabel}>Sampling</span>
              <dl className={styles.facts}>
                <Fact label="Temperature" value={agent.definition.temperature != null ? String(agent.definition.temperature) : "Model default"} />
                <Fact label="Top P" value={agent.definition.topP != null ? String(agent.definition.topP) : "Model default"} />
              </dl>
            </div>
          </>
        )}
      </section>

      {/* Publish targets */}
      <section className={styles.publishSection} aria-label="Publish targets">
        <h2 className={styles.sectionTitle}>Published to</h2>
        <div className={styles.publishRow}>
          {(["api", "teams", "m365"] as PublishTarget[]).map((p) => {
            const on = agent.publishTo.includes(p);
            const Icon = PUBLISH[p].icon;
            return (
              <span key={p} className={styles.pubChip} data-on={on || undefined}>
                <Icon size={14} strokeWidth={2.2} aria-hidden />
                {PUBLISH[p].label}
                {!on && <span className={styles.pubOff}>off</span>}
              </span>
            );
          })}
        </div>
      </section>
    </div>
  );
}

function Fact({
  label,
  value,
  valueNode,
  mono = false,
}: {
  label: string;
  value?: string;
  valueNode?: React.ReactNode;
  mono?: boolean;
}) {
  return (
    <div className={styles.fact}>
      <dt className={styles.factLabel}>{label}</dt>
      <dd className={styles.factValue + (mono ? " mono" : "")}>{valueNode ?? value}</dd>
    </div>
  );
}
