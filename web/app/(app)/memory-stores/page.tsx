import { getMe, getMemoryStores } from "@/lib/api";
import { MemoryStoresView } from "@/components/views/memory-stores-view";

export default async function MemoryStoresPage() {
  const me = await getMe();
  const stores = await getMemoryStores();
  return <MemoryStoresView role={me.role} stores={stores} />;
}
