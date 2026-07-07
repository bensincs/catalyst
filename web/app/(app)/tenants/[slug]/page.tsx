import { notFound } from "next/navigation";
import { ApiError, getCatalog, getMe, getMemoryStores, getTenantContext, getTenantsRegistry } from "@/lib/api";
import { TenantOverview } from "@/components/views/tenant-overview";
import { EntitlementsPanel } from "@/components/views/entitlements-panel";
import { StoreEntitlementsPanel } from "@/components/views/store-entitlements-panel";

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
      const [registry, catalog, stores] = await Promise.all([
        getTenantsRegistry(),
        getCatalog(),
        getMemoryStores(),
      ]);
      const row = registry.find((r) => r.id === slug);
      entitlements = (
        <>
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
