import { notFound } from "next/navigation";
import { ApiError, getApplications, getCatalog, getInfrastructure, getMe, getMemoryStores, getTenantContext, getTenantsRegistry } from "@/lib/api";
import { TenantOverview } from "@/components/views/tenant-overview";
import { TenantAccessPanel } from "@/components/views/tenant-access-panel";
import { EntitlementsPanel } from "@/components/views/entitlements-panel";
import { StoreEntitlementsPanel } from "@/components/views/store-entitlements-panel";
import { DeploymentEntitlementsPanel } from "@/components/views/deployment-entitlements-panel";
import { InfrastructureEntitlementsPanel } from "@/components/views/infrastructure-entitlements-panel";

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
          <EntitlementsPanel
            slug={slug}
            name={ctx.tenant.name}
            entitled={row?.entitledAgents ?? []}
            catalog={catalog}
          />
          <StoreEntitlementsPanel
            slug={slug}
            name={ctx.tenant.name}
            entitled={row?.entitledStores ?? []}
            stores={stores.filter((s) => s.owner === "")}
          />
          <DeploymentEntitlementsPanel
            slug={slug}
            name={ctx.tenant.name}
            entitled={row?.entitledDeployments ?? []}
            deployments={deployments.filter((d) => d.owner === "")}
          />
          <InfrastructureEntitlementsPanel
            slug={slug}
            name={ctx.tenant.name}
            entitled={row?.entitledInfrastructure ?? []}
            infrastructure={infrastructure.filter((i) => i.owner === "")}
          />
        </>
      );
    }

    return (
      <>
        <TenantOverview tenant={ctx.tenant} agents={ctx.agents} now={Date.now()} platformView={platform} />
        {entitlements}
      </>
    );
  } catch (e) {
    if (e instanceof ApiError && (e.status === 404 || e.status === 403)) notFound();
    throw e;
  }
}
