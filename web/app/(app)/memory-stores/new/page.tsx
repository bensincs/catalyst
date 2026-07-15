import { StoreForm } from "@/components/views/store-form";

export const dynamic = "force-dynamic";

// Dedicated create page (replaces the New memory store modal). The definition
// editor is self-contained, so no external data is needed.
export default function NewMemoryStorePage() {
  return <StoreForm />;
}
