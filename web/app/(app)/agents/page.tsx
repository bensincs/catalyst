import { getCatalog, getMe, getMemoryStores, getMyContext } from "@/lib/api";
import { AgentsView } from "@/components/views/agents-view";

export const dynamic = "force-dynamic";

export default async function AgentsPage() {
  const me = await getMe();
  const [agents, stores] = await Promise.all([getCatalog(), getMemoryStores()]);
  // Tenants also see the agents actually running in their project (health,
  // drift, publish targets); the platform view is the catalog it authors.
  const enabled = me.role === "tenant" ? (await getMyContext()).agents : [];
  return <AgentsView role={me.role} agents={agents} enabled={enabled} memoryStores={stores} />;
}
