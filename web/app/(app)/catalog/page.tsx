import { getApplications, getCatalog, getMe, getMemoryStores, getMyContext } from "@/lib/api";
import { CatalogueView } from "@/components/views/catalogue-view";
import type { ClusterInfo, DepOption } from "@/lib/types";

export const dynamic = "force-dynamic";

export default async function CatalogPage() {
  const me = await getMe();
  const [agents, stores, applications] = await Promise.all([
    getCatalog(),
    getMemoryStores(),
    getApplications(),
  ]);

  const depOptions: DepOption[] = [
    ...applications.map((a) => ({ id: a.id, name: a.name, kind: "app" as const })),
    ...agents.map((c) => ({ id: c.id, name: c.name, kind: "agent" as const })),
  ];

  let cluster: ClusterInfo | undefined;
  if (me.role === "tenant") {
    cluster = (await getMyContext()).tenant.cluster;
  }

  return (
    <CatalogueView
      role={me.role}
      agents={agents}
      stores={stores}
      applications={applications}
      cluster={cluster}
      depOptions={depOptions}
    />
  );
}
