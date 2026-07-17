import { ServerCog } from "lucide-react";
import { getMe, getMyContext } from "@/lib/api";
import { InstallView } from "@/components/views/install-view";
import { PlaceholderPage } from "@/components/views/placeholder-page";

export const dynamic = "force-dynamic";

export default async function InstallPage() {
  const me = await getMe();
  if (me.role !== "tenant") {
    return (
      <PlaceholderPage
        title="Install"
        description="The in-tenant Cortex app: reconciler, Foundry project, and enrollment."
        icon={ServerCog}
        emptyTitle="Install runs in a tenant"
        emptyBody="Deployment is initiated by the tenant admin and runs in the customer's own subscription. Drill into a tenant from the Fleet to see its install and reconciler state."
      />
    );
  }
  const ctx = await getMyContext();

  return (
    <InstallView
      tenant={ctx.tenant}
      cortexTenantId={process.env.PLATFORM_TENANT_ID ?? "<your Cortex tenant id>"}
      cortexPrincipalId={process.env.CORTEX_SP_OBJECT_ID ?? "<Cortex control-plane service principal object id>"}
    />
  );
}
