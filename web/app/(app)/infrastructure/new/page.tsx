import { getInfrastructure, getMe, getSecretSets } from "@/lib/api";
import { InfrastructureForm } from "@/components/views/infrastructure-form";
import type { DepOption, Infrastructure } from "@/lib/types";

export const dynamic = "force-dynamic";

// Dedicated create page. Loads the dependency candidates an infrastructure entity
// may point at, filtered to what the viewer manages or is entitled to.
export default async function NewInfrastructurePage() {
  const me = await getMe();
  const all = await getInfrastructure();
  const sets = await getSecretSets();
  const platform = me.role === "platform";
  const usable = (i: Infrastructure) => (platform ? i.owner === "" : i.owned || i.entitled);

  const secretSets = sets.filter((s) => (platform ? s.owner === "" : s.owned || s.entitled));

  // Allowed edges out of infrastructure: other infrastructure, and secret stores
  // (a @secure() parameter binds to one). Secret stores were reachable
  // server-side but absent from this picker, so the edge could not be authored.
  const depOptions: DepOption[] = [
    ...all.filter(usable).map((i) => ({ id: i.id, name: i.name, kind: "infrastructure" as const })),
    ...secretSets.map((s) => ({ id: s.id, name: s.name, kind: "secret_set" as const })),
  ];

  return <InfrastructureForm depOptions={depOptions} secretSets={secretSets} />;
}
