import { Gauge } from "lucide-react";
import { getFleet, getMe } from "@/lib/api";
import { MeteringView } from "@/components/views/metering-view";
import { PlaceholderPage } from "@/components/views/placeholder-page";

// Platform-only: fleet-wide call volume rolled up from reconciler heartbeats.
export default async function MeteringPage() {
  const me = await getMe();
  if (me.role !== "platform") {
    return (
      <PlaceholderPage
        title="Metering"
        description="Fleet-wide usage and cost showback."
        icon={Gauge}
        emptyTitle="Platform admins only"
        emptyBody="Fleet-wide metering is a publisher view. For your own tenant's consumption, see Usage."
      />
    );
  }
  const fleet = await getFleet();
  return <MeteringView stats={fleet.stats} tenants={fleet.tenants} />;
}
