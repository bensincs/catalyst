import type { Role } from "@/lib/types";

declare module "next-auth" {
  interface Session {
    user: {
      name?: string | null;
      email?: string | null;
      image?: string | null;
      tid: string; // the directory the user signed in from
      oid: string; // the caller's object id
      role: Role;
    };
    /** The explicitly-selected Cortex tenant slug (X-Cortex-Tenant header). Empty
     *  ⇒ the caller's primary tenant. */
    activeTenantSlug?: string;
    error?: string;
  }
}

declare module "next-auth/jwt" {
  interface JWT {
    name?: string | null;
    email?: string | null;
    tid?: string;
    oid?: string;
    /** The single Entra access token (for the API) + its refresh token, server-side
     *  only, in the encrypted JWT. */
    accessToken?: string;
    refreshToken?: string;
    expiresAt?: number; // epoch seconds
    /** The explicitly-selected Cortex tenant slug (X-Cortex-Tenant header). */
    activeTenantSlug?: string;
    error?: string;
  }
}

export {};
