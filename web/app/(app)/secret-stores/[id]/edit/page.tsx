import { notFound } from "next/navigation";
import { getMe, getSecretSets } from "@/lib/api";
import { SecretSetForm } from "@/components/views/secret-set-form";

export const dynamic = "force-dynamic";

// Editing changes the declared keys — never values. A tenant's values are not
// readable here, so there is nothing for this page to show or edit about them.
export default async function EditSecretStorePage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const me = await getMe();
  const sets = await getSecretSets();

  const set = sets.find((s) => s.id === id);
  const manageable = set && (me.role === "platform" ? set.owner === "" : set.owned);
  if (!set || !manageable) notFound();

  return <SecretSetForm set={set} />;
}
