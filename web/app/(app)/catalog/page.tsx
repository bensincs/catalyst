import { getCatalog, getMe, getMemoryStores } from "@/lib/api";
import { CatalogView } from "@/components/views/catalog-view";

export default async function CatalogPage() {
  const me = await getMe();
  const agents = await getCatalog();
  const memoryStores = await getMemoryStores();
  return <CatalogView role={me.role} agents={agents} memoryStores={memoryStores} />;
}
