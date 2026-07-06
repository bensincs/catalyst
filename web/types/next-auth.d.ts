import type { Role } from "@/lib/types";

declare module "next-auth" {
  interface Session {
    user: {
      name?: string | null;
      email?: string | null;
      image?: string | null;
      tid: string;
      oid: string;
      role: Role;
    };
    error?: string;
  }
}

declare module "next-auth/jwt" {
  interface JWT {
    tid?: string;
    oid?: string;
    name?: string | null;
    email?: string | null;
    /** Access token minted for the control-plane API — server-side only. */
    accessToken?: string;
    refreshToken?: string;
    /** Access-token expiry, epoch seconds. */
    expiresAt?: number;
    error?: string;
  }
}

export {};
