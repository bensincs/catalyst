import { getMe, getSecretSets } from "@/lib/api";
import { SecretStoresView } from "@/components/views/secret-stores-view";

export default async function SecretStoresPage() {
  const me = await getMe();
  const sets = await getSecretSets();
  return <SecretStoresView role={me.role} sets={sets} />;
}
