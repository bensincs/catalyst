import { getApplications, getCatalog, getMe, getMyContext } from "@/lib/api";
import { DeploymentForm } from "@/components/views/deployment-form";
import type { ClusterInfo, DepOption } from "@/lib/types";

export const dynamic = "force-dynamic";

// Dedicated create page (replaces the old modal). Loads the same supporting data
// the list page fed the modal: dependency candidates (other deployments + agents)
// and, for a tenant, its cluster status.
export default async function NewDeploymentPage() {
  const me = await getMe();
  const [apps, catalog] = await Promise.all([getApplications(), getCatalog()]);

  const depOptions: DepOption[] = [
    ...apps.map((a) => ({ id: a.id, name: a.name, kind: "app" as const })),
    ...catalog.map((c) => ({ id: c.id, name: c.name, kind: "agent" as const })),
  ];

  let cluster: ClusterInfo | undefined;
  if (me.role === "tenant") cluster = (await getMyContext()).tenant.cluster;

  return <DeploymentForm role={me.role} depOptions={depOptions} cluster={cluster} />;
}
