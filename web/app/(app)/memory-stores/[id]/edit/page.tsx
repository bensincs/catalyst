import { notFound } from "next/navigation";
import { getMe, getMemoryStores } from "@/lib/api";
import { StoreForm } from "@/components/views/store-form";

export const dynamic = "force-dynamic";

// Dedicated edit page (replaces the edit modal). Only the store's manager may
// edit; the definition is immutable, so the page shows it read-only.
export default async function EditMemoryStorePage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const me = await getMe();
  const stores = await getMemoryStores();

  const store = stores.find((s) => s.id === id);
  const manageable = store && (me.role === "platform" ? store.owner === "" : store.owned);
  if (!store || !manageable) notFound();

  return <StoreForm store={store} />;
}
