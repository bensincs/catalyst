import { notFound } from "next/navigation";
import { ApiError, getApplications, getCatalog, getInfrastructure, getMe, getMemoryStores, getTenantContext, getTenantMembers, getTenantsRegistry } from "@/lib/api";
import { TenantOverview } from "@/components/views/tenant-overview";
import { TenantAccessPanel } from "@/components/views/tenant-access-panel";
import { TenantRenamePanel } from "@/components/views/tenant-rename-panel";
import { FootprintPanel } from "@/components/views/footprint-panel";
import { TenantMembersPanel } from "@/components/views/tenant-members-panel";
import { EntitlementsPanel } from "@/components/views/entitlements-panel";

export default async function TenantDrillInPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  try {
    const me = await getMe();
    const platform = me.role === "platform";
    const ctx = await getTenantContext(slug);

    let entitlements = null;
    if (platform) {
      const [registry, catalog, stores, deployments, infrastructure] = await Promise.all([
        getTenantsRegistry(),
        getCatalog(),
        getMemoryStores(),
        getApplications(),
        getInfrastructure(),
      ]);
      const row = registry.find((r) => r.id === slug);
      const platformHosted = ctx.summary.hostingMode === "platform";
      const members = platformHosted ? await getTenantMembers(slug) : [];
      entitlements = (
        <>
          <TenantRenamePanel slug={slug} name={ctx.tenant.name} />
          <TenantAccessPanel slug={slug} name={ctx.tenant.name} enabled={ctx.tenant.enabled} />
          {platformHosted ? (
            <TenantMembersPanel slug={slug} name={ctx.tenant.name} members={members} />
          ) : null}
          {ctx.tenant.cluster.infraDelegated ? (
            <FootprintPanel
              slug={slug}
              name={ctx.tenant.name}
              hostingMode={ctx.tenant.hostingMode}
              footprintState={ctx.tenant.cluster.footprintState}
              clusterMode={ctx.tenant.clusterMode}
              config={ctx.tenant.footprintConfig}
            />
          ) : null}
          <EntitlementsPanel
            slug={slug}
            name={ctx.tenant.name}
            infrastructure={infrastructure.filter((i) => i.owner === "")}
            deployments={deployments.filter((d) => d.owner === "")}
            agents={catalog.filter((a) => a.owner === "")}
            stores={stores.filter((s) => s.owner === "")}
            entitledInfrastructure={row?.entitledInfrastructure ?? []}
            entitledDeployments={row?.entitledDeployments ?? []}
            entitledAgents={row?.entitledAgents ?? []}
            entitledStores={row?.entitledStores ?? []}
          />
        </>
      );
    }

    return (
      <>
        <TenantOverview
          tenant={ctx.tenant}
          agents={ctx.agents}
          now={Date.now()}
          platformView={platform}
          infrastructure={ctx.infrastructure}
          applications={ctx.applications}
          stores={ctx.stores}
        />
        {entitlements}
      </>
    );
  } catch (e) {
    if (e instanceof ApiError && (e.status === 404 || e.status === 403)) notFound();
    throw e;
  }
}
