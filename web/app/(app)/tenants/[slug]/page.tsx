import { notFound } from "next/navigation";
import { ApiError, getCatalog, getMe, getTenantContext, getTenantsRegistry } from "@/lib/api";
import { TenantOverview } from "@/components/views/tenant-overview";
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
      const [registry, catalog] = await Promise.all([getTenantsRegistry(), getCatalog()]);
      const row = registry.find((r) => r.id === slug);
      entitlements = (
        <EntitlementsPanel
          slug={slug}
          name={ctx.tenant.name}
          entitled={row?.entitledAgents ?? []}
          catalog={catalog}
        />
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
