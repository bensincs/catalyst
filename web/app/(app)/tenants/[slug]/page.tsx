import { notFound } from "next/navigation";
import { ApiError, getApplications, getCatalog, getInfrastructure, getMe, getMemoryStores, getTenantContext, getTenantsRegistry } from "@/lib/api";
import { TenantOverview } from "@/components/views/tenant-overview";
import { TenantAccessPanel } from "@/components/views/tenant-access-panel";
import { FootprintReprovisionPanel } from "@/components/views/footprint-reprovision-panel";
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
      entitlements = (
        <>
          <TenantAccessPanel slug={slug} name={ctx.tenant.name} enabled={ctx.tenant.enabled} />
          {ctx.tenant.cluster.infraDelegated ? (
            <FootprintReprovisionPanel
              slug={slug}
              name={ctx.tenant.name}
              footprintState={ctx.tenant.cluster.footprintState}
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
