import { getApplications, getCatalog, getInfrastructure, getMe, getMyContext } from "@/lib/api";
import { DeploymentForm, APP_OUTPUTS, AGENT_OUTPUTS, type DepOutputs } from "@/components/views/deployment-form";
import type { ClusterInfo, DepOption } from "@/lib/types";

export const dynamic = "force-dynamic";

// Dedicated create page (replaces the old modal). Loads typed dependency
// candidates (infrastructure / applications / agents) filtered to what the viewer
// manages or is entitled to, the wireable outputs of each candidate, and — for a
// tenant — its cluster status.
export default async function NewDeploymentPage() {
  const me = await getMe();
  const [apps, catalog, infra] = await Promise.all([getApplications(), getCatalog(), getInfrastructure()]);
  const platform = me.role === "platform";
  const usable = (x: { owner: string; owned: boolean; entitled: boolean }) =>
    platform ? x.owner === "" : x.owned || x.entitled;

  // Allowed edges out of an application: infrastructure | application | agent.
  const depOptions: DepOption[] = [
    ...infra.filter(usable).map((i) => ({ id: i.id, name: i.name, kind: "infrastructure" as const })),
    ...apps.filter(usable).map((a) => ({ id: a.id, name: a.name, kind: "application" as const })),
    ...catalog.filter(usable).map((c) => ({ id: c.id, name: c.name, kind: "agent" as const })),
  ];
  // The wireable outputs each candidate exposes (infrastructure → Bicep outputs;
  // applications/agents → derived outputs).
  const depOutputs: DepOutputs[] = [
    ...infra.filter(usable).map((i) => ({ kind: "infrastructure" as const, id: i.id, name: i.name, outputs: i.bicepOutputs })),
    ...apps.filter(usable).map((a) => ({ kind: "application" as const, id: a.id, name: a.name, outputs: APP_OUTPUTS })),
    ...catalog.filter(usable).map((c) => ({ kind: "agent" as const, id: c.id, name: c.name, outputs: AGENT_OUTPUTS })),
  ];

  let cluster: ClusterInfo | undefined;
  if (me.role === "tenant") cluster = (await getMyContext()).tenant.cluster;

  return <DeploymentForm role={me.role} depOptions={depOptions} depOutputs={depOutputs} cluster={cluster} />;
}
