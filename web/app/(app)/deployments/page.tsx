import { Rocket } from "lucide-react";
import { getApplications, getMe, getMyContext } from "@/lib/api";
import { DeploymentsView } from "@/components/views/deployments-view";
import { PlaceholderPage } from "@/components/views/placeholder-page";

export const dynamic = "force-dynamic";

export default async function DeploymentsPage() {
  const me = await getMe();
  if (me.role !== "tenant") {
    return (
      <PlaceholderPage
        title="Deployments"
        description="Helm deployments run in each tenant's own cluster."
        icon={Rocket}
        emptyTitle="Deployments are per-tenant"
        emptyBody="As a platform admin, drill into a tenant from the Fleet to see its cluster and deployments."
      />
    );
  }
  const [apps, ctx] = await Promise.all([getApplications(), getMyContext()]);
  return <DeploymentsView cluster={ctx.tenant.cluster} applications={apps} />;
}
