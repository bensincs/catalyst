import { Bot } from "lucide-react";
import { getMe, getMyContext } from "@/lib/api";
import { AgentsView } from "@/components/views/agents-view";
import { PlaceholderPage } from "@/components/views/placeholder-page";

export default async function AgentsPage() {
  const me = await getMe();
  if (me.role !== "tenant") {
    return (
      <PlaceholderPage
        title="Agents"
        description="Enabled agents in a tenant."
        icon={Bot}
        emptyTitle="Open a tenant to see its agents"
        emptyBody="As a platform admin, drill into a tenant from the Fleet to see the agents running in it."
      />
    );
  }
  const ctx = await getMyContext();
  return <AgentsView agents={ctx.agents} />;
}
