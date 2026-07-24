"use server";

import { revalidatePath } from "next/cache";
import { unstable_update } from "@/auth";

/** Select the active Cortex tenant by slug — sent as X-Cortex-Tenant on every API
 *  call. A user is assigned to tenants (memberships) and switches between them
 *  here; empty slug ⇒ their primary tenant. */
export async function setActiveTenantSlug(slug: string): Promise<void> {
  await unstable_update({ activeTenantSlug: slug } as never);
  revalidatePath("/", "layout");
}
