import { getApplications, getFleet, getInfrastructure, getMe, getMemoryStores, getMyContext } from "@/lib/api";
import { FleetView } from "@/components/views/fleet-view";
import { TenantOverview } from "@/components/views/tenant-overview";
import type { InfraSummary } from "@/components/views/install-status";

export default async function HomePage() {
  const me = await getMe();

  if (me.role === "platform") {
    const fleet = await getFleet();
    return <FleetView stats={fleet.stats} tenants={fleet.tenants} now={Date.now()} />;
  }

  const [ctx, infrastructure, applications, stores] = await Promise.all([
    getMyContext(),
    getInfrastructure(),
    getApplications(),
    getMemoryStores(),
  ]);

  // Aggregate the provisioning state of the tenant's enabled infrastructure
  // (deployed by the control plane via Lighthouse) for the staged install checks.
  const withInfra = infrastructure.filter((i) => i.enabled);
  const ready = withInfra.filter((i) => i.infraState === "ready").length;
  const failed = withInfra.filter((i) => i.infraState === "failed").length;
  const infra: InfraSummary = {
    total: withInfra.length,
    ready,
    failed,
    provisioning: withInfra.length - ready - failed,
  };

  return (
    <TenantOverview
      tenant={ctx.tenant}
      agents={ctx.agents}
      now={Date.now()}
      infrastructure={withInfra}
      applications={applications.filter((a) => a.enabled)}
      stores={stores.filter((s) => s.enabled)}
      infra={infra}
    />
  );
}
