"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Stamp, RefreshCw, Save } from "lucide-react";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/status";
import { useToast } from "@/components/providers/toast-provider";
import { saveFootprintConfig, stampFootprint } from "@/lib/actions";
import panel from "./entitlements-panel.module.css";
import styles from "./tenant-members-panel.module.css";

const TONE = { ready: "success", provisioning: "info", failed: "danger", draft: "neutral" } as const;
const LABEL = { ready: "Provisioned", provisioning: "Provisioning", failed: "Failed", draft: "Draft" } as const;

type Cfg = Record<string, unknown>;

/** The tenant footprint: configure its cluster shape (AKS-managed or bring your
 *  own), then stamp it. For a platform-hosted tenant the footprint isn't
 *  auto-provisioned — a platform admin sets this up and presses Stamp. The stamp
 *  and the re-provision button are the same action. */
export function FootprintPanel({
  slug,
  name,
  hostingMode,
  footprintState,
  clusterMode,
  config,
}: {
  slug: string;
  name: string;
  hostingMode: "delegated" | "platform";
  footprintState?: string;
  clusterMode: "aks" | "byo";
  config: Cfg;
}) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();

  const [mode, setMode] = useState<"aks" | "byo">(clusterMode);
  const [region, setRegion] = useState(String(config.region ?? ""));
  const [nodeCount, setNodeCount] = useState(String(config.nodeCount ?? ""));
  const [nodeVmSize, setNodeVmSize] = useState(String(config.nodeVmSize ?? ""));
  const [byoNote, setByoNote] = useState(String(config.note ?? ""));

  const state = (footprintState || "draft") as keyof typeof LABEL;
  const provisioning = footprintState === "provisioning";
  // Cluster shape is configurable for every tenant — delegated tenants can bring
  // their own (Arc) cluster too, not just platform-hosted ones.
  const stampLabel = !footprintState || footprintState === "draft" ? "Stamp footprint" : "Re-provision";

  const save = () =>
    start(async () => {
      const cfg: Cfg =
        mode === "aks"
          ? {
              ...(region.trim() ? { region: region.trim() } : {}),
              ...(nodeVmSize.trim() ? { nodeVmSize: nodeVmSize.trim() } : {}),
              ...(nodeCount.trim() ? { nodeCount: Number(nodeCount) } : {}),
            }
          : { ...(byoNote.trim() ? { note: byoNote.trim() } : {}) };
      const res = await saveFootprintConfig(slug, mode, cfg);
      if (res.ok) {
        toast({ title: "Footprint saved", description: `${mode === "aks" ? "AKS" : "Bring your own"} cluster.`, tone: "success" });
        router.refresh();
      } else {
        toast({ title: "Couldn't save", description: res.error, tone: "danger" });
      }
    });

  const stamp = () =>
    start(async () => {
      const res = await stampFootprint(slug);
      if (res.ok) {
        toast({ title: `Stamping ${name}`, description: "Provisioning the footprint…", tone: "success" });
        router.refresh();
      } else {
        toast({ title: "Couldn't stamp", description: res.error, tone: "danger" });
      }
    });

  return (
    <section className={panel.panel} aria-label="Tenant footprint">
      <div className={panel.head}>
        <div className={panel.headText}>
          <h2 className={panel.title}>
            Footprint{" "}
            <StatusBadge tone={TONE[state] ?? "neutral"} label={LABEL[state] ?? footprintState} variant="soft" />
          </h2>
          <p className={panel.sub}>
            The reconciler, Foundry, and the Kubernetes cluster for {name}. Choose an AKS-managed
            cluster (Cortex provisions it) or bring your own (connect an existing Arc cluster —
            Cortex deploys only the reconciler + Foundry). Configure the shape, then stamp it.
          </p>
        </div>
        <Button variant="secondary" icon={provisioning ? RefreshCw : Stamp} loading={pending || provisioning} onClick={stamp}>
          {stampLabel}
        </Button>
      </div>

      <div style={{ marginTop: 14, display: "flex", flexDirection: "column", gap: 12 }}>
          <div className={styles.field} style={{ gap: 8 }}>
            <ModeChip active={mode === "aks"} onClick={() => setMode("aks")} title="AKS-managed" desc="Cortex provisions an AKS cluster" />
            <ModeChip active={mode === "byo"} onClick={() => setMode("byo")} title="Bring your own" desc="Connect an existing (Arc) cluster" />
          </div>

          {mode === "aks" ? (
            <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit,minmax(160px,1fr))", gap: 8 }}>
              <Field label="Region" value={region} onChange={setRegion} placeholder="e.g. uksouth" />
              <Field label="Node VM size" value={nodeVmSize} onChange={setNodeVmSize} placeholder="e.g. Standard_D2s_v5" />
              <Field label="Node count" value={nodeCount} onChange={setNodeCount} placeholder="e.g. 2" type="number" />
            </div>
          ) : (
            <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
              <Field label="Note" value={byoNote} onChange={setByoNote} placeholder="Arc cluster reference / notes" />
              <p className={panel.sub} style={{ margin: 0 }}>
                The footprint deploys the reconciler + Foundry only; connect it to your Arc cluster. (Reconciler
                Arc connection is being finished.)
              </p>
            </div>
          )}

          <div>
            <Button variant="ghost" icon={Save} loading={pending} onClick={save}>
              Save config
            </Button>
          </div>
        </div>
    </section>
  );
}

function ModeChip({ active, onClick, title, desc }: { active: boolean; onClick: () => void; title: string; desc: string }) {
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        flex: "1 1 180px",
        textAlign: "left",
        padding: "10px 12px",
        borderRadius: "var(--radius-md)",
        border: `1px solid ${active ? "var(--border-strong)" : "var(--border)"}`,
        background: active ? "var(--surface-hover)" : "transparent",
        boxShadow: active ? "0 0 0 1px var(--border-strong)" : "none",
        cursor: "pointer",
      }}
      aria-pressed={active}
    >
      <div style={{ fontWeight: 600, fontSize: "var(--text-body-sm)" }}>{title}</div>
      <div style={{ fontSize: 12, color: "var(--text-secondary)" }}>{desc}</div>
    </button>
  );
}

function Field({
  label,
  value,
  onChange,
  placeholder,
  type = "text",
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  type?: string;
}) {
  return (
    <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
      <span style={{ fontSize: 12, color: "var(--text-secondary)" }}>{label}</span>
      <input
        className={styles.input}
        style={{ paddingLeft: 12 }}
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
      />
    </label>
  );
}
