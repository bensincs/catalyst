"use client";

import { useState } from "react";
import { Bot, Database, Rocket } from "lucide-react";
import { CatalogView } from "./catalog-view";
import { MemoryStoresView } from "./memory-stores-view";
import { DeploymentsView } from "./deployments-view";
import type {
  Application,
  CatalogAgent,
  ClusterInfo,
  DepOption,
  MemoryStore,
  Role,
} from "@/lib/types";
import styles from "./catalogue-view.module.css";

type Tab = "agents" | "stores" | "deployments";

/** One catalogue for everything a tenant can run: agents, memory stores, and
 *  deployments — each browsed + managed through its own view under a tab. */
export function CatalogueView({
  role,
  agents,
  stores,
  applications,
  cluster,
  depOptions,
}: {
  role: Role;
  agents: CatalogAgent[];
  stores: MemoryStore[];
  applications: Application[];
  cluster?: ClusterInfo;
  depOptions: DepOption[];
}) {
  const [tab, setTab] = useState<Tab>("agents");

  const tabs: { id: Tab; label: string; icon: typeof Bot; count: number }[] = [
    { id: "agents", label: "Agents", icon: Bot, count: agents.length },
    { id: "stores", label: "Memory stores", icon: Database, count: stores.length },
    { id: "deployments", label: "Deployments", icon: Rocket, count: applications.length },
  ];

  return (
    <div>
      <div className={styles.tabs} role="tablist" aria-label="Catalogue">
        {tabs.map((t) => {
          const Icon = t.icon;
          const active = tab === t.id;
          return (
            <button
              key={t.id}
              type="button"
              role="tab"
              aria-selected={active}
              className={styles.tab}
              data-active={active || undefined}
              onClick={() => setTab(t.id)}
            >
              <Icon size={16} strokeWidth={2} />
              <span>{t.label}</span>
              <span className={styles.count}>{t.count}</span>
            </button>
          );
        })}
      </div>

      {tab === "agents" && <CatalogView role={role} agents={agents} memoryStores={stores} />}
      {tab === "stores" && <MemoryStoresView role={role} stores={stores} />}
      {tab === "deployments" && (
        <DeploymentsView role={role} applications={applications} cluster={cluster} depOptions={depOptions} />
      )}
    </div>
  );
}
