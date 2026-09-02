import { SecretSetForm } from "@/components/views/secret-set-form";

export const dynamic = "force-dynamic";

// Authoring a secret store declares KEY NAMES only, so the page needs no data:
// there is nothing about a tenant's values to load, by design.
export default function NewSecretStorePage() {
  return <SecretSetForm />;
}
