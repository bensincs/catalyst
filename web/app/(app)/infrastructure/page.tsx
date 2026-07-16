import { getInfrastructure, getMe } from "@/lib/api";
import { InfrastructureView } from "@/components/views/infrastructure-view";

export const dynamic = "force-dynamic";

export default async function InfrastructurePage() {
  const me = await getMe();
  const infrastructure = await getInfrastructure();
  return <InfrastructureView role={me.role} infrastructure={infrastructure} />;
}
