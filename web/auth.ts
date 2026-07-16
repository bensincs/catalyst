import NextAuth from "next-auth";
import MicrosoftEntraID from "next-auth/providers/microsoft-entra-id";
import type { JWT } from "next-auth/jwt";

const PLATFORM_TENANT_ID = (process.env.PLATFORM_TENANT_ID ?? "").toLowerCase();
const CLIENT_ID = process.env.AUTH_MICROSOFT_ENTRA_ID_ID ?? "";

// Scopes: OIDC + a refresh token + this API's exposed delegated scope. The
// resulting access_token is minted for the API (aud = the app registration).
const API_SCOPE = CLIENT_ID ? `api://${CLIENT_ID}/access_as_user` : "";
const SCOPES = ["openid", "profile", "email", "offline_access", API_SCOPE].filter(Boolean).join(" ");

// One per-directory token bundle. A single human may be a guest in several Entra
// directories (each a Cortex tenant); we hold one bundle per directory they've
// signed into and forward the *active* one to the API. Entra tokens are
// per-tenant, so this is the only correct way to operate several tenants: the
// token itself proves access to its tenant. Tokens live only in the encrypted
// JWT cookie, never in the browser or the session payload.
export type TenantToken = {
  tid: string;
  oid: string;
  name: string; // best-effort label (directory domain); the active tenant shows its real name via the API
  accessToken?: string;
  refreshToken?: string;
  expiresAt?: number; // epoch seconds
  error?: string;
};

function labelFor(profile: Record<string, unknown> | undefined): string {
  const email = (profile?.email as string) ?? (profile?.preferred_username as string) ?? "";
  const domain = email.split("@")[1] ?? "";
  return domain || "";
}

async function refreshTenant(t: TenantToken): Promise<TenantToken> {
  if (!t.refreshToken) return { ...t, error: "RequiresReauth" };
  try {
    const res = await fetch(`https://login.microsoftonline.com/${t.tid}/oauth2/v2.0/token`, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({
        client_id: CLIENT_ID,
        client_secret: process.env.AUTH_MICROSOFT_ENTRA_ID_SECRET ?? "",
        grant_type: "refresh_token",
        refresh_token: t.refreshToken,
        scope: SCOPES,
      }),
    });
    const data = (await res.json()) as { access_token?: string; expires_in?: number; refresh_token?: string };
    if (!res.ok || !data.access_token) throw new Error("refresh failed");
    return {
      ...t,
      accessToken: data.access_token,
      expiresAt: Math.floor(Date.now() / 1000) + Number(data.expires_in ?? 3600),
      refreshToken: data.refresh_token ?? t.refreshToken,
      error: undefined,
    };
  } catch {
    return { ...t, error: "RefreshFailed" };
  }
}

const tenantsOf = (token: JWT): Record<string, TenantToken> => (token.tenants as Record<string, TenantToken>) ?? {};

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
      // Session updates: switch the active tenant, or merge a tenant connected
      // via the targeted OAuth callback (/api/tenants/[tid]/connect).
      if (trigger === "update" && session && typeof session === "object") {
        const s = session as { activeTid?: string; connectTenant?: TenantToken };
        if (s.connectTenant?.tid) {
          const b = s.connectTenant;
          token.tenants = { ...tenantsOf(token), [b.tid]: b };
          token.activeTid = b.tid;
          return token;
        }
        const tid = String(s.activeTid ?? "").toLowerCase();
        if (tid && tenantsOf(token)[tid]) token.activeTid = tid;
        return token;
      }

      // New sign-in: capture the directory's tokens and MERGE it into the set
      // (rather than replace), so signing into a second tenant keeps the first.
      if (account) {
        const tid = ((profile?.tid as string) ?? "").toLowerCase();
        const bundle: TenantToken = {
          tid,
          oid: (profile?.oid as string) ?? (profile?.sub as string) ?? "",
          name: labelFor(profile),
          accessToken: account.access_token,
          refreshToken: account.refresh_token,
          expiresAt: account.expires_at,
        };
        token.tenants = { ...tenantsOf(token), [tid]: bundle };
        token.activeTid = tid;
        // Home identity anchor (first sign-in).
        token.name = (token.name as string) ?? (profile?.name as string) ?? token.name;
        token.email =
          (token.email as string) ?? (profile?.email as string) ?? (profile?.preferred_username as string) ?? token.email;
        return token;
      }

      // Refresh the ACTIVE tenant's token as it nears expiry.
      const tenants = tenantsOf(token);
      const active = tenants[token.activeTid as string];
      if (active && (typeof active.expiresAt !== "number" || Date.now() >= active.expiresAt * 1000 - 60_000)) {
        token.tenants = { ...tenants, [active.tid]: await refreshTenant(active) };
      }
      return token;
    },

    async session({ session, token }) {
      const tenants = tenantsOf(token);
      const activeTid = (token.activeTid as string) ?? "";
      const active = tenants[activeTid];
      session.user.tid = activeTid;
      session.user.oid = active?.oid ?? "";
      session.user.role = PLATFORM_TENANT_ID && activeTid === PLATFORM_TENANT_ID ? "platform" : "tenant";
      if (token.name) session.user.name = token.name as string;
      if (token.email) session.user.email = token.email as string;
      // The directories this human can operate — for the switcher. No tokens exposed.
      session.tenants = Object.values(tenants).map((t) => ({
        tid: t.tid,
        name: t.name,
        needsReauth: Boolean(t.error) || !t.accessToken,
      }));
      session.activeTid = activeTid;
      session.error = active?.error;
      return session;
    },
  },
});
