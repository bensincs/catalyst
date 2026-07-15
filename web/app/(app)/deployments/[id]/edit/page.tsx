import { notFound } from "next/navigation";
import { getApplications, getCatalog, getMe, getMyContext } from "@/lib/api";
import { DeploymentForm } from "@/components/views/deployment-form";
import type { ClusterInfo, DepOption } from "@/lib/types";

export const dynamic = "force-dynamic";

// Dedicated edit page (replaces the old modal). Only the app's manager may edit:
// the platform manages platform-authored apps, a tenant manages its own.
export default async function EditDeploymentPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const me = await getMe();
  const [apps, catalog] = await Promise.all([getApplications(), getCatalog()]);

  const app = apps.find((a) => a.id === id);
  const manageable = app && (me.role === "platform" ? app.owner === "" : app.owned);
  if (!app || !manageable) notFound();

  const depOptions: DepOption[] = [
    ...apps.filter((a) => a.id !== id).map((a) => ({ id: a.id, name: a.name, kind: "app" as const })),
    ...catalog.map((c) => ({ id: c.id, name: c.name, kind: "agent" as const })),
  ];

  let cluster: ClusterInfo | undefined;
  if (me.role === "tenant") cluster = (await getMyContext()).tenant.cluster;

  return <DeploymentForm role={me.role} app={app} depOptions={depOptions} cluster={cluster} />;
}
