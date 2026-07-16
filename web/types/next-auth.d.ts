import type { Role } from "@/lib/types";
import type { TenantToken } from "@/auth";

/** A directory the signed-in human can operate, surfaced to the switcher (no
 *  tokens — those stay in the encrypted JWT). */
export type SessionTenant = { tid: string; name: string; needsReauth: boolean };

declare module "next-auth" {
  interface Session {
    user: {
      name?: string | null;
      email?: string | null;
      image?: string | null;
      tid: string; // the ACTIVE tenant's Entra directory id
      oid: string; // the caller's object id in the active tenant
      role: Role;
    };
    /** Every directory this human has connected — the tenant switcher's list. */
    tenants: SessionTenant[];
    activeTid: string;
    error?: string;
  }
}

declare module "next-auth/jwt" {
  interface JWT {
    name?: string | null;
    email?: string | null;
    /** Per-directory token bundles (server-side only, in the encrypted JWT). */
    tenants?: Record<string, TenantToken>;
    /** Which directory's token is currently forwarded to the API. */
    activeTid?: string;
    error?: string;
  }
}

export {};
