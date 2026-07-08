"use client";

import { useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import {
  ArrowDownUp,
  ArrowUpRight,
  GitBranch,
  Plus,
  Radar,
  SearchX,
  SlidersHorizontal,
} from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/status";
import { EmptyState } from "@/components/ui/empty-state";
import { useToast } from "@/components/providers/toast-provider";
import { formatCount, formatInt, formatRelative } from "@/lib/format";
import {
  LIFECYCLE_META,
  type FleetStats,
  type Lifecycle,
  type TenantSummary,
} from "@/lib/types";
import styles from "./fleet-view.module.css";

type SortKey = "name" | "agents" | "calls" | "heartbeat";

const LIFECYCLE_ORDER: Lifecycle[] = ["live", "enrolling", "degraded", "suspended"];

const FILTERS: { id: "all" | Lifecycle; label: string }[] = [
  { id: "all", label: "All" },
  { id: "live", label: "Live" },
  { id: "degraded", label: "Degraded" },
  { id: "enrolling", label: "Enrolling" },
];

export function FleetView({
  stats,
  tenants,
  now,
}: {
  stats: FleetStats;
  tenants: TenantSummary[];
  now: number;
}) {
  const { toast } = useToast();
  const router = useRouter();
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<"all" | Lifecycle>("all");
  const [sort, setSort] = useState<{ key: SortKey; dir: "asc" | "desc" }>({
    key: "heartbeat",
    dir: "asc",
  });

  const rows = useMemo(() => {
    let list = tenants.filter((t) => {
      const q = query.trim().toLowerCase();
      const matchesQuery =
        !q ||
        t.name.toLowerCase().includes(q) ||
        t.tenantId.toLowerCase().includes(q) ||
        t.region.toLowerCase().includes(q);
      return matchesQuery && (filter === "all" || t.lifecycle === filter);
    });
    const dir = sort.dir === "asc" ? 1 : -1;
    list = [...list].sort((a, b) => {
      switch (sort.key) {
        case "name":
          return a.name.localeCompare(b.name) * dir;
        case "agents":
          return (a.agentCount - b.agentCount) * dir;
        case "calls":
          return (a.monthlyCalls - b.monthlyCalls) * dir;
        case "heartbeat":
          return (b.lastHeartbeatMs - a.lastHeartbeatMs) * dir;
      }
    });
    return list;
  }, [tenants, query, filter, sort]);

  const lifecycleCounts = useMemo(() => {
    const map = new Map<Lifecycle, number>();
    for (const t of tenants) map.set(t.lifecycle, (map.get(t.lifecycle) ?? 0) + 1);
    return map;
  }, [tenants]);

  const toggleSort = (key: SortKey) =>
    setSort((s) =>
      s.key === key ? { key, dir: s.dir === "asc" ? "desc" : "asc" } : { key, dir: "asc" },
    );

  if (tenants.length === 0) {
    return (
      <div>
        <PageHeader
          title="Fleet"
          description="Every enrolled tenant, the version it runs, and the gap between desired and actual state — live from reconciler heartbeats."
          actions={
            <Button
              variant="primary"
              icon={Plus}
              onClick={() => toast({ title: "New agent version", description: "Opening the catalog authoring flow.", tone: "info" })}
            >
              Publish version
            </Button>
          }
        />
        <div className={styles.emptyFleet}>
          <EmptyState
            icon={Radar}
            title="No tenants enrolled yet"
            description="As organizations install the Cortex app, their reconciler enrolls and they appear here — with live version, health, and drift. Publish a version and entitle a tenant to get them started."
            action={
              <Button variant="primary" icon={Plus} onClick={() => toast({ title: "Publish a version", tone: "info" })}>
                Publish version
              </Button>
            }
          />
        </div>
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        title="Fleet"
        description="Every enrolled tenant, the version it runs, and the gap between desired and actual state — live from reconciler heartbeats."
        actions={
          <>
            <Button icon={GitBranch} onClick={() => toast({ title: "Releases", description: "Version authoring lands in Catalog.", tone: "neutral" })}>
              Releases
            </Button>
            <Button
              variant="primary"
              icon={Plus}
              onClick={() => toast({ title: "New agent version", description: "Opening the catalog authoring flow.", tone: "info" })}
            >
              Publish version
            </Button>
          </>
        }
      />

      <section className={styles.summary} aria-label="Fleet summary">
        <div className={styles.stats}>
          <Stat label="Tenants" value={formatInt(stats.tenants)} sub={`${stats.bound} enrolled`} />
          <Stat label="Agents running" value={formatInt(stats.agents)} sub="across the fleet" />
          <Stat label="Calls · 30d" value={formatCount(stats.callsMonth)} sub="all tenants" />
          <Stat
            label="On latest"
            value={`${stats.onLatest}/${stats.tenants}`}
            sub={<span className="mono">v{stats.latestVersion}</span>}
          />
        </div>

        <div className={styles.health}>
          <div className={styles.healthHead}>
            <span className={styles.healthTitle}>Fleet status</span>
            <span className={styles.healthMeta}>
              {`${lifecycleCounts.get("live") ?? 0} of ${stats.tenants} live`}
            </span>
          </div>
          <div className={styles.healthBar} role="img" aria-label="Fleet status distribution">
            {LIFECYCLE_ORDER.map((lc) => {
              const count = lifecycleCounts.get(lc) ?? 0;
              if (!count) return null;
              return (
                <span
                  key={lc}
                  className={styles.healthSeg}
                  data-tone={LIFECYCLE_META[lc].tone}
                  style={{ flexGrow: count }}
                  title={`${LIFECYCLE_META[lc].label}: ${count}`}
                />
              );
            })}
          </div>
          <ul className={styles.legend} role="list">
            {LIFECYCLE_ORDER.map((lc) => {
              const count = lifecycleCounts.get(lc) ?? 0;
              if (!count) return null;
              return (
                <li key={lc} className={styles.legendItem}>
                  <span className={styles.legendDot} data-tone={LIFECYCLE_META[lc].tone} />
                  {LIFECYCLE_META[lc].label}
                  <span className={styles.legendCount}>{count}</span>
                </li>
              );
            })}
          </ul>
        </div>
      </section>

      <div className={styles.toolbar}>
        <div className={styles.search}>
          <input
            type="search"
            className={styles.searchInput}
            placeholder="Filter by name, tenant ID, or region"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label="Filter tenants"
          />
        </div>
        <div className={styles.filters} role="group" aria-label="Filter by status">
          {FILTERS.map((f) => (
            <button
              key={f.id}
              type="button"
              className={styles.filterChip}
              data-active={filter === f.id || undefined}
              onClick={() => setFilter(f.id)}
            >
              {f.label}
              {f.id !== "all" && (
                <span className={styles.filterCount}>{lifecycleCounts.get(f.id as Lifecycle) ?? 0}</span>
              )}
            </button>
          ))}
          <Button size="sm" variant="ghost" icon={SlidersHorizontal} iconOnly aria-label="Table settings" title="Table settings" />
        </div>
      </div>

      <div className={styles.tableWrap}>
        <table className={styles.table}>
          <thead>
            <tr>
              <th scope="col" className={styles.thTenant}>
                <SortButton label="Tenant" active={sort.key === "name"} dir={sort.dir} onClick={() => toggleSort("name")} />
              </th>
              <th scope="col" className={styles.colPlan}>Plan</th>
              <th scope="col" className={styles.colRegion}>Region</th>
              <th scope="col" className={styles.num + " " + styles.colAgents}>
                <SortButton label="Agents" active={sort.key === "agents"} dir={sort.dir} onClick={() => toggleSort("agents")} align="end" />
              </th>
              <th scope="col" className={styles.colVersion}>Version</th>
              <th scope="col">Status</th>
              <th scope="col" className={styles.colCalls + " " + styles.num}>
                <SortButton label="Calls · 30d" active={sort.key === "calls"} dir={sort.dir} onClick={() => toggleSort("calls")} align="end" />
              </th>
              <th scope="col" className={styles.colSeen}>
                <SortButton label="Last seen" active={sort.key === "heartbeat"} dir={sort.dir} onClick={() => toggleSort("heartbeat")} />
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.length === 0 ? (
              <tr>
                <td colSpan={8}>
                  <EmptyState
                    icon={SearchX}
                    title="No tenants match"
                    description="No tenants match that filter. Clear the search or switch health filters to see the rest of the fleet."
                    action={
                      <Button
                        size="sm"
                        onClick={() => {
                          setQuery("");
                          setFilter("all");
                        }}
                      >
                        Clear filters
                      </Button>
                    }
                    compact
                  />
                </td>
              </tr>
            ) : (
              rows.map((t) => (
                <FleetRow
                  key={t.id}
                  tenant={t}
                  now={now}
                  onOpen={() => router.push(`/tenants/${encodeURIComponent(t.id)}`)}
                  latest={stats.latestVersion}
                />
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function Stat({
  label,
  value,
  sub,
}: {
  label: string;
  value: string;
  sub?: React.ReactNode;
}) {
  return (
    <div className={styles.stat}>
      <span className={styles.statLabel}>{label}</span>
      <span className={styles.statValue + " tnum"}>{value}</span>
      {sub && <span className={styles.statSub}>{sub}</span>}
    </div>
  );
}

function SortButton({
  label,
  active,
  dir,
  onClick,
  align = "start",
}: {
  label: string;
  active: boolean;
  dir: "asc" | "desc";
  onClick: () => void;
  align?: "start" | "end";
}) {
  return (
    <button
      type="button"
      className={styles.sortBtn}
      data-active={active || undefined}
      data-align={align}
      onClick={onClick}
      aria-label={`Sort by ${label}`}
    >
      <span>{label}</span>
      <ArrowDownUp size={12} strokeWidth={2.2} className={styles.sortIcon} data-dir={active ? dir : undefined} aria-hidden />
    </button>
  );
}

function PlanTag({ plan }: { plan: TenantSummary["plan"] }) {
  return (
    <span className={styles.plan} data-plan={plan}>
      {plan}
    </span>
  );
}

function FleetRow({
  tenant,
  now,
  onOpen,
  latest,
}: {
  tenant: TenantSummary;
  now: number;
  onOpen: () => void;
  latest: string;
}) {
  const lc = LIFECYCLE_META[tenant.lifecycle];
  const behind = tenant.version !== "" && tenant.version !== latest && tenant.lifecycle !== "enrolling";
  const stale = tenant.lifecycle === "suspended";
  return (
    <tr
      className={styles.row}
      onClick={onOpen}
      tabIndex={0}
      role="link"
      aria-label={`Open ${tenant.name}`}
      onKeyDown={(ev) => {
        if (ev.key === "Enter" || ev.key === " ") {
          ev.preventDefault();
          onOpen();
        }
      }}
    >
      <td className={styles.thTenant}>
        <div className={styles.tenantCell}>
          <span className={styles.tenantName}>{tenant.name}</span>
          <span className={styles.tenantId + " mono"}>{tenant.tenantId}</span>
          {/* Fleet's core signal (version + drift) is a column on desktop; surface
              it in the cell once that column is dropped on narrow screens. */}
          <span className={styles.tenantMeta}>
            <span className={styles.version + " mono"} data-behind={behind || undefined}>
              {tenant.version ? `v${tenant.version}` : "—"}
            </span>
            {!tenant.enabled && (
              <span className={styles.metaFlag} data-tone="warning">
                pending
              </span>
            )}
            {behind && (
              <span className={styles.metaFlag} data-tone="warning">
                behind
              </span>
            )}
            {tenant.drift ? (
              <span className={styles.metaFlag} data-tone="info">
                {tenant.drift} drift
              </span>
            ) : null}
          </span>
        </div>
      </td>
      <td className={styles.colPlan}>
        <PlanTag plan={tenant.plan} />
      </td>
      <td className={styles.colRegion}>
        <span className={styles.region}>{tenant.region}</span>
      </td>
      <td className={styles.num + " " + styles.colAgents}>
        <span className="tnum">{tenant.agentCount || <span className={styles.zero}>0</span>}</span>
        {tenant.reconcilingCount > 0 && (
          <span className={styles.reconcilingHint} title={`${tenant.reconcilingCount} reconciling`}>
            +{tenant.reconcilingCount}
          </span>
        )}
      </td>
      <td className={styles.colVersion}>
        <span className={styles.version + " mono"} data-behind={behind || undefined}>
          {tenant.version || "—"}
        </span>
      </td>
      <td>
        <StatusBadge tone={lc.tone} label={lc.label} pulse={tenant.lifecycle === "live"} variant={tenant.lifecycle === "live" ? "plain" : "soft"} />
      </td>
      <td className={styles.colCalls + " " + styles.num}>
        <span className="tnum">{formatInt(tenant.monthlyCalls)}</span>
      </td>
      <td className={styles.colSeen}>
        <span className={styles.seen} data-stale={stale || undefined}>
          {formatRelative(tenant.lastHeartbeatMs, now)}
        </span>
        <ArrowUpRight size={14} strokeWidth={2} className={styles.rowArrow} aria-hidden />
      </td>
    </tr>
  );
}
