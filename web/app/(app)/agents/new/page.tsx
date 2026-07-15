import { getMemoryStores } from "@/lib/api";
import { AgentForm } from "@/components/views/agent-form";

export const dynamic = "force-dynamic";

// Dedicated create page (replaces the New agent modal). Needs the memory stores
// so a prompt agent can bind one in its definition.
export default async function NewAgentPage() {
  const stores = await getMemoryStores();
  return <AgentForm stores={stores} />;
}
