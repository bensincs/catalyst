"use client";

import { useState, useTransition } from "react";
import { useRouter } from "next/navigation";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useToast } from "@/components/providers/toast-provider";
import { createPlatformTenant } from "@/lib/actions";
import styles from "./create-tenant-button.module.css";

/** Platform control to create a platform-hosted tenant — one that lives in the
 *  platform's own subscription (a dedicated resource group per tenant) instead of
 *  a customer's delegated one. Users are assigned to it afterwards (memberships). */
export function CreateTenantButton() {
  const router = useRouter();
  const { toast } = useToast();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [region, setRegion] = useState("");
  const [pending, start] = useTransition();

  const create = () =>
    start(async () => {
      const value = name.trim();
      if (!value) return;
      const res = await createPlatformTenant({ name: value, region: region.trim() });
      if (res.ok) {
        toast({
          title: `Creating ${value}`,
          description: "Provisioning its footprint in the platform subscription.",
          tone: "success",
        });
        setOpen(false);
        setName("");
        setRegion("");
        router.refresh();
      } else {
        toast({ title: "Couldn't create tenant", description: res.error, tone: "danger" });
      }
    });

  if (!open) {
    return (
      <Button icon={Plus} onClick={() => setOpen(true)}>
        New tenant
      </Button>
    );
  }

  return (
    <div className={styles.form} role="group" aria-label="Create platform-hosted tenant">
      <input
        autoFocus
        value={name}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && create()}
        placeholder="Tenant name"
        className={styles.input}
      />
      <input
        value={region}
        onChange={(e) => setRegion(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && create()}
        placeholder="Region (e.g. uksouth)"
        className={styles.input}
      />
      <Button icon={Plus} loading={pending} onClick={create}>
        Create
      </Button>
      <Button variant="ghost" onClick={() => setOpen(false)}>
        Cancel
      </Button>
    </div>
  );
}
