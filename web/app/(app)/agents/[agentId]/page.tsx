import { notFound } from "next/navigation";
import { Bot } from "lucide-react";
import { getMe, getMemoryStores, getMyContext } from "@/lib/api";
import { AgentDetailView } from "@/components/views/agent-detail-view";
import { PlaceholderPage } from "@/components/views/placeholder-page";

export default async function AgentDetailPage({ params }: { params: Promise<{ agentId: string }> }) {
  const me = await getMe();
  if (me.role !== "tenant") {
    return (
      <PlaceholderPage
        title="Agent"
        description="Enabled agents run inside a tenant."
        icon={Bot}
        emptyTitle="Open a tenant to see its agents"
        emptyBody="As a platform admin, drill into a tenant from the Fleet to inspect the agents running in it."
      />
    );
  }
  const { agentId } = await params;
  const [ctx, stores] = await Promise.all([getMyContext(), getMemoryStores()]);
  const agent = ctx.agents.find((a) => a.id === agentId);
  if (!agent) notFound();
  return (
    <AgentDetailView
      agent={agent}
      live={ctx.tenant.lifecycle === "live"}
      lastHeartbeatMs={ctx.tenant.lastHeartbeatMs}
      now={Date.now()}
      stores={stores}
    />
  );
}
