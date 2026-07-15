import { ServerCog } from "lucide-react";
import { getApplications, getMe, getMyContext } from "@/lib/api";
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
  const [ctx, apps] = await Promise.all([getMyContext(), getApplications()]);

  // Aggregate the provisioning state of the tenant's enabled deployments that
  // carry Azure infra (deployed by the control plane via Lighthouse).
  const withInfra = apps.filter((a) => a.enabled && (a.bicepModule ?? "").trim() !== "");
  const ready = withInfra.filter((a) => a.infraState === "ready").length;
  const failed = withInfra.filter((a) => a.infraState === "failed").length;
  const infra: InfraSummary = {
    total: withInfra.length,
    ready,
    failed,
    provisioning: withInfra.length - ready - failed,
  };

  return (
    <InstallView tenant={ctx.tenant} agentCount={ctx.agents.length} infra={infra} now={Date.now()} />
  );
}
