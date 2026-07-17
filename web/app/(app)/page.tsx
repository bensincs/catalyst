import { getFleet, getMe, getMyContext } from "@/lib/api";
import { FleetView } from "@/components/views/fleet-view";
import { TenantOverview } from "@/components/views/tenant-overview";

export const dynamic = "force-dynamic";

export default async function HomePage() {
  const me = await getMe();

  if (me.role === "platform") {
    const fleet = await getFleet();
    return <FleetView stats={fleet.stats} tenants={fleet.tenants} now={Date.now()} />;
  }

  // One context call carries the tenant's enabled resources — enough to draw the
  // topology. The staged install checks moved to /install.
  const ctx = await getMyContext();

  return (
    <TenantOverview
      tenant={ctx.tenant}
      agents={ctx.agents}
      now={Date.now()}
      infrastructure={ctx.infrastructure}
      applications={ctx.applications}
      stores={ctx.stores}
    />
  );
}
