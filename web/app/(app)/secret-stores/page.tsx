import { getMe, getMyContext, getSecretSets } from "@/lib/api";
import { SecretStoresView } from "@/components/views/secret-stores-view";

export default async function SecretStoresPage() {
  const me = await getMe();
  const sets = await getSecretSets();
  // A tenant needs its own vault before it can supply a value; the platform
  // view never fills anything in, so it does not need to ask.
  const vaultReady =
    me.role === "tenant" ? (await getMyContext()).tenant.vaultReady : true;
  return (
    <SecretStoresView role={me.role} sets={sets} vaultReady={vaultReady} />
  );
}
