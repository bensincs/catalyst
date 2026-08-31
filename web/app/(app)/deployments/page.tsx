import { getApplications, getMe, getMyContext } from "@/lib/api";
import { DeploymentsView } from "@/components/views/deployments-view";
import type { ClusterInfo } from "@/lib/types";

export const dynamic = "force-dynamic";

export default async function DeploymentsPage() {
  const me = await getMe();
  const apps = await getApplications();

  // Tenants also see their own cluster status; the platform view is the catalog.
  let cluster: ClusterInfo | undefined;
  let appsDomain: string | undefined;
  if (me.role === "tenant") {
    const ctx = await getMyContext();
    cluster = ctx.tenant.cluster;
    appsDomain = ctx.tenant.ingress?.appsDomain;
  }
  return (
    <DeploymentsView role={me.role} applications={apps} cluster={cluster} appsDomain={appsDomain} />
  );
}
