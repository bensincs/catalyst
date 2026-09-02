import { notFound } from "next/navigation";
import { getInfrastructure, getMe } from "@/lib/api";
import { InfrastructureForm } from "@/components/views/infrastructure-form";
import type { DepOption, Infrastructure } from "@/lib/types";

export const dynamic = "force-dynamic";

// Dedicated edit page. Offers infra → infra dependency candidates (the only
// allowed edge out of infrastructure), excluding the entity being edited.
export default async function EditInfrastructurePage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;
  const me = await getMe();
  const all = await getInfrastructure();

  const infra = all.find((i) => i.id === id);
  const manageable = infra && (me.role === "platform" ? infra.owner === "" : infra.owned);
  if (!infra || !manageable) notFound();

  const platform = me.role === "platform";
  const usable = (i: Infrastructure) => (platform ? i.owner === "" : i.owned || i.entitled);
  const depOptions: DepOption[] = all
    .filter((i) => i.id !== id && usable(i))
    .map((i) => ({ id: i.id, name: i.name, kind: "infrastructure" as const }));

  return <InfrastructureForm infra={infra} depOptions={depOptions} />;
}
