import { getInfrastructure, getMe } from "@/lib/api";
import { InfrastructureForm } from "@/components/views/infrastructure-form";
import type { DepOption, Infrastructure } from "@/lib/types";

export const dynamic = "force-dynamic";

// Dedicated create page. Loads the infrastructure catalog to offer infra → infra
// dependency candidates (the only allowed edge out of infrastructure), filtered
// to what the viewer manages or is entitled to.
export default async function NewInfrastructurePage() {
  const me = await getMe();
  const all = await getInfrastructure();
  const platform = me.role === "platform";
  const usable = (i: Infrastructure) => (platform ? i.owner === "" : i.owned || i.entitled);

  const depOptions: DepOption[] = all
    .filter(usable)
    .map((i) => ({ id: i.id, name: i.name, kind: "infrastructure" as const }));

  return <InfrastructureForm depOptions={depOptions} />;
}
