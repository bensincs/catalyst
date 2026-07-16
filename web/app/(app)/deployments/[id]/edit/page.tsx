import { notFound } from "next/navigation";
import { getApplications, getCatalog, getInfrastructure, getMe, getMyContext } from "@/lib/api";
import { DeploymentForm, type InfraOutputs } from "@/components/views/deployment-form";
import type { ClusterInfo, DepOption } from "@/lib/types";

export const dynamic = "force-dynamic";

// Dedicated edit page (replaces the old modal). Only the app's manager may edit:
// the platform manages platform-authored apps, a tenant manages its own.
export default async function EditDeploymentPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const me = await getMe();
  const [apps, catalog, infra] = await Promise.all([getApplications(), getCatalog(), getInfrastructure()]);

  const app = apps.find((a) => a.id === id);
  const manageable = app && (me.role === "platform" ? app.owner === "" : app.owned);
  if (!app || !manageable) notFound();

  const platform = me.role === "platform";
  const usable = (x: { owner: string; owned: boolean; entitled: boolean }) =>
    platform ? x.owner === "" : x.owned || x.entitled;

  // Allowed edges out of an application: infrastructure | application | agent.
  const depOptions: DepOption[] = [
    ...infra.filter(usable).map((i) => ({ id: i.id, name: i.name, kind: "infrastructure" as const })),
    ...apps.filter((a) => a.id !== id && usable(a)).map((a) => ({ id: a.id, name: a.name, kind: "application" as const })),
    ...catalog.filter(usable).map((c) => ({ id: c.id, name: c.name, kind: "agent" as const })),
  ];
  const infraOutputs: InfraOutputs[] = infra
    .filter(usable)
    .map((i) => ({ id: i.id, name: i.name, outputs: i.bicepOutputs }));

  let cluster: ClusterInfo | undefined;
  if (me.role === "tenant") cluster = (await getMyContext()).tenant.cluster;

  return <DeploymentForm role={me.role} app={app} depOptions={depOptions} infraOutputs={infraOutputs} cluster={cluster} />;
}
