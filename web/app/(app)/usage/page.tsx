import { Activity } from "lucide-react";
import { getMe, getMyContext } from "@/lib/api";
import { UsageView } from "@/components/views/usage-view";
import { PlaceholderPage } from "@/components/views/placeholder-page";

export default async function UsagePage() {
  const me = await getMe();
  if (me.role !== "tenant") {
    return (
      <PlaceholderPage
        title="Usage"
        description="Per-tenant consumption and cost showback."
        icon={Activity}
        emptyTitle="Usage is per tenant"
        emptyBody="This is a tenant-scoped view. For fleet-wide consumption across every tenant, use Metering."
      />
    );
  }
  const ctx = await getMyContext();
  return <UsageView agents={ctx.agents} />;
}
