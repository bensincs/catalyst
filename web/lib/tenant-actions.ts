"use server";

import { revalidatePath } from "next/cache";
import { unstable_update } from "@/auth";
import { discoverTenants as discover, type DiscoveryResult } from "@/lib/tenant-discovery";

/** Switch the active tenant to one the user has already connected (its token is
 *  already in the encrypted JWT, so this is instant — no re-auth). */
export async function switchTenant(tid: string): Promise<void> {
  await unstable_update({ activeTid: tid } as never);
  revalidatePath("/", "layout");
}

/** Enumerate the directories this human can reach (ARM `/tenants`), so the
 *  Settings switcher can offer them for connection. Best-effort: returns an
 *  `error` code the UI degrades on rather than throwing. Connecting a directory
 *  itself is a plain navigation to `/api/tenants/{tid}/connect`. */
export async function discoverTenants(): Promise<DiscoveryResult> {
  return discover();
}
