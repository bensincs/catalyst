import { ServerCog } from "lucide-react";
import { getApplications, getInfrastructure, getMe, getMemoryStores, getMyContext } from "@/lib/api";
import { InstallView, type InfraSummary } from "@/components/views/install-view";
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
  const [ctx, infrastructure, applications, stores] = await Promise.all([
    getMyContext(),
    getInfrastructure(),
    getApplications(),
    getMemoryStores(),
  ]);

  // Aggregate the provisioning state of the tenant's enabled infrastructure
  // (deployed by the control plane via Lighthouse).
  const withInfra = infrastructure.filter((i) => i.enabled);
  const ready = withInfra.filter((i) => i.infraState === "ready").length;
  const failed = withInfra.filter((i) => i.infraState === "failed").length;
  const infra: InfraSummary = {
    total: withInfra.length,
    ready,
    failed,
    provisioning: withInfra.length - ready - failed,
  };

  return (
    <InstallView
      tenant={ctx.tenant}
      agentCount={ctx.agents.length}
      infra={infra}
      cortexTenantId={process.env.PLATFORM_TENANT_ID ?? "<your Cortex tenant id>"}
      cortexPrincipalId={process.env.CORTEX_SP_OBJECT_ID ?? "<Cortex control-plane service principal object id>"}
      now={Date.now()}
      infrastructure={withInfra}
      applications={applications.filter((a) => a.enabled)}
      agents={ctx.agents}
      stores={stores.filter((s) => s.enabled)}
    />
  );
}
