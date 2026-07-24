import NextAuth from "next-auth";
import MicrosoftEntraID from "next-auth/providers/microsoft-entra-id";
import type { JWT } from "next-auth/jwt";

const PLATFORM_TENANT_ID = (process.env.PLATFORM_TENANT_ID ?? "").toLowerCase();
const CLIENT_ID = process.env.AUTH_MICROSOFT_ENTRA_ID_ID ?? "";

// Scopes: OIDC + a refresh token + this API's exposed delegated scope. The
// resulting access_token is minted for the API (aud = the app registration).
const API_SCOPE = CLIENT_ID ? `api://${CLIENT_ID}/access_as_user` : "";
const SCOPES = ["openid", "profile", "email", "offline_access", API_SCOPE].filter(Boolean).join(" ");

// A session carries ONE Entra token — the directory the user signed in from. The
// tenant they operate is a Cortex tenant SLUG (X-Cortex-Tenant), selected via the
// switcher: a user is assigned to tenants (memberships), not juggling per-directory
// tokens. Tokens live only in the encrypted JWT cookie, never in the browser.

// refreshAccess renews the access token from the sign-in directory's token
// endpoint as it nears expiry, keeping the same refresh token when a new one
// isn't returned.
async function refreshAccess(token: JWT): Promise<JWT> {
  if (!token.refreshToken || !token.tid) return { ...token, error: "RequiresReauth" };
  try {
    const res = await fetch(`https://login.microsoftonline.com/${token.tid}/oauth2/v2.0/token`, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({
        client_id: CLIENT_ID,
        client_secret: process.env.AUTH_MICROSOFT_ENTRA_ID_SECRET ?? "",
        grant_type: "refresh_token",
        refresh_token: token.refreshToken,
        scope: SCOPES,
      }),
    });
    const data = (await res.json()) as { access_token?: string; expires_in?: number; refresh_token?: string };
    if (!res.ok || !data.access_token) throw new Error("refresh failed");
    return {
      ...token,
      accessToken: data.access_token,
      expiresAt: Math.floor(Date.now() / 1000) + Number(data.expires_in ?? 3600),
      refreshToken: data.refresh_token ?? token.refreshToken,
      error: undefined,
    };
  } catch {
    return { ...token, error: "RefreshFailed" };
  }
}

export const { handlers, auth, signIn, signOut, unstable_update } = NextAuth({
  trustHost: true,
  session: { strategy: "jwt", maxAge: 8 * 60 * 60 },
  pages: { signIn: "/signin" },
  providers: [
    MicrosoftEntraID({
      clientId: CLIENT_ID,
      clientSecret: process.env.AUTH_MICROSOFT_ENTRA_ID_SECRET,
      issuer:
        process.env.AUTH_MICROSOFT_ENTRA_ID_ISSUER ?? "https://login.microsoftonline.com/common/v2.0",
      authorization: { params: { scope: SCOPES } },
      profile(profile) {
        return {
          id: (profile.oid as string) ?? (profile.sub as string),
          name: (profile.name as string) ?? null,
          email: (profile.email as string) ?? (profile.preferred_username as string) ?? null,
        };
      },
    }),
  ],
  callbacks: {
    async jwt({ token, account, profile, trigger, session }) {
      // Select the active Cortex tenant (a slug sent as X-Cortex-Tenant). Empty
      // slug ⇒ the caller's primary tenant.
      if (trigger === "update" && session && typeof session === "object") {
        const s = session as { activeTenantSlug?: string };
        if (typeof s.activeTenantSlug === "string") token.activeTenantSlug = s.activeTenantSlug;
        return token;
      }

      // New sign-in: capture the directory's token + identity.
      if (account) {
        token.accessToken = account.access_token;
        token.refreshToken = account.refresh_token;
        token.expiresAt = account.expires_at;
        token.tid = ((profile?.tid as string) ?? "").toLowerCase();
        token.oid = (profile?.oid as string) ?? (profile?.sub as string) ?? "";
        token.name = (profile?.name as string) ?? token.name;
        token.email =
          (profile?.email as string) ?? (profile?.preferred_username as string) ?? token.email;
        token.activeTenantSlug = "";
        token.error = undefined;
        return token;
      }

      // Refresh the access token as it nears expiry.
      if (typeof token.expiresAt !== "number" || Date.now() >= token.expiresAt * 1000 - 60_000) {
        return refreshAccess(token);
      }
      return token;
    },

    async session({ session, token }) {
      session.user.tid = (token.tid as string) ?? "";
      session.user.oid = (token.oid as string) ?? "";
      session.user.role =
        PLATFORM_TENANT_ID && session.user.tid === PLATFORM_TENANT_ID ? "platform" : "tenant";
      if (token.name) session.user.name = token.name as string;
      if (token.email) session.user.email = token.email as string;
      session.activeTenantSlug = (token.activeTenantSlug as string) ?? "";
      session.error = token.error as string | undefined;
      return session;
    },
  },
});
