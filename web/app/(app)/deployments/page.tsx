import { getApplications, getCatalog, getMe, getMyContext } from "@/lib/api";
import { DeploymentsView } from "@/components/views/deployments-view";
import type { ClusterInfo, DepOption } from "@/lib/types";

export const dynamic = "force-dynamic";

export default async function DeploymentsPage() {
  const me = await getMe();
  const [apps, catalog] = await Promise.all([getApplications(), getCatalog()]);

  // Dependencies can be on other deployments or on agents.
  const depOptions: DepOption[] = [
    ...apps.map((a) => ({ id: a.id, name: a.name, kind: "app" as const })),
    ...catalog.map((c) => ({ id: c.id, name: c.name, kind: "agent" as const })),
  ];

  // Tenants also see their own cluster status; the platform view is the catalog.
  let cluster: ClusterInfo | undefined;
  if (me.role === "tenant") {
    cluster = (await getMyContext()).tenant.cluster;
  }
  return <DeploymentsView role={me.role} applications={apps} cluster={cluster} depOptions={depOptions} />;
}
