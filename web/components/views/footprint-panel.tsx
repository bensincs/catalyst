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

// An Azure VM size, e.g. Standard_D2s_v5. Caught client-side because a bad size
// is otherwise only rejected by ARM preflight — the provisioner then retries the
// same rejection every 30s with no route back to the person who typed it.
const VM_SIZE_RE = /^Standard_[A-Za-z]+\d+[A-Za-z-]*(_v\d+)?$/;

/** The tenant footprint: configure the AKS node shape, then stamp it. For a
 *  platform-hosted tenant the footprint isn't auto-provisioned — a platform admin
 *  sets this up and presses Stamp. The stamp and the re-provision button are the
 *  same action. */
export function FootprintPanel({
  slug,
  name,
  footprintState,
  config,
}: {
  slug: string;
  name: string;
  hostingMode: "delegated" | "platform";
  footprintState?: string;
  config: Cfg;
}) {
  const router = useRouter();
  const { toast } = useToast();
  const [pending, start] = useTransition();

  const [nodeCount, setNodeCount] = useState(String(config.nodeCount ?? ""));
  const [nodeVmSize, setNodeVmSize] = useState(String(config.nodeVmSize ?? ""));

  const state = (footprintState || "draft") as keyof typeof LABEL;
  const provisioning = footprintState === "provisioning";
  const stampLabel = !footprintState || footprintState === "draft" ? "Stamp footprint" : "Re-provision";

  const vmSizeError = nodeVmSize.trim() !== "" && !VM_SIZE_RE.test(nodeVmSize.trim());

  const save = () =>
    start(async () => {
      if (vmSizeError) {
        toast({
          title: "Check the VM size",
          description: `"${nodeVmSize.trim()}" doesn't look like an Azure VM size. Expected something like Standard_D2s_v5.`,
          tone: "danger",
        });
        return;
      }
      const cfg: Cfg = {
        ...(nodeVmSize.trim() ? { nodeVmSize: nodeVmSize.trim() } : {}),
        ...(nodeCount.trim() ? { nodeCount: Number(nodeCount) } : {}),
      };
      const res = await saveFootprintConfig(slug, cfg);
      if (res.ok) {
        toast({ title: "Footprint saved", description: "AKS node shape updated.", tone: "success" });
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
            The reconciler, Foundry, and the AKS cluster for {name}. Set the node shape, then stamp it.
          </p>
        </div>
        <Button variant="secondary" icon={provisioning ? RefreshCw : Stamp} loading={pending || provisioning} onClick={stamp}>
          {stampLabel}
        </Button>
      </div>

      <div style={{ marginTop: 14, display: "flex", flexDirection: "column", gap: 12 }}>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit,minmax(160px,1fr))", gap: 8 }}>
          <Field
            label="Node VM size"
            value={nodeVmSize}
            onChange={setNodeVmSize}
            placeholder="e.g. Standard_D2s_v5"
            error={vmSizeError ? "Expected a size like Standard_D2s_v5." : undefined}
          />
          <Field label="Node count" value={nodeCount} onChange={setNodeCount} placeholder="e.g. 2" type="number" />
        </div>

        <div>
          <Button variant="ghost" icon={Save} loading={pending} onClick={save}>
            Save config
          </Button>
        </div>
      </div>
    </section>
  );
}

function Field({
  label,
  value,
  onChange,
  placeholder,
  type = "text",
  error,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  type?: string;
  error?: string;
}) {
  return (
    <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
      <span style={{ fontSize: 12, color: "var(--text-secondary)" }}>{label}</span>
      <input
        className={styles.input}
        style={{ paddingLeft: 12, ...(error ? { borderColor: "var(--danger, #d33)" } : {}) }}
        type={type}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        aria-invalid={error ? true : undefined}
      />
      {error ? <span style={{ fontSize: 12, color: "var(--danger, #d33)" }}>{error}</span> : null}
    </label>
  );
}
