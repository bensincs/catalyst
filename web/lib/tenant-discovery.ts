import "server-only";
import { cookies } from "next/headers";
import { decode } from "next-auth/jwt";

// Auto-discover the directories this human can act in. We take the ACTIVE
// tenant's refresh token, trade it for an ARM-scoped access token, then ask
// Azure Resource Manager which tenants the user can reach. This is a
// convenience for the connect UI — the real gate is still the per-tenant token
// exchanged at connect time and validated by the control-plane API.
//
// Prerequisite (app registration): the delegated permission "Azure Service
// Management → user_impersonation" must be granted (admin consent) so the
// refresh token can be redeemed for the ARM resource silently.

const ARM = "https://management.azure.com";
const ARM_SCOPE = `${ARM}/user_impersonation offline_access`;
const CLIENT_ID = process.env.AUTH_MICROSOFT_ENTRA_ID_ID ?? "";
const CLIENT_SECRET = process.env.AUTH_MICROSOFT_ENTRA_ID_SECRET ?? "";
const SESSION_COOKIES = ["authjs.session-token", "__Secure-authjs.session-token"];

export type DiscoveredTenant = { tid: string; displayName: string; defaultDomain: string };
export type DiscoveryResult = { tenants: DiscoveredTenant[]; error?: string };

/** Read the active tenant's refresh token straight from the encrypted session
 *  cookie (server-side only) — it is never exposed to the browser or session. */
async function activeRefreshToken(): Promise<{ tid: string; refreshToken: string } | null> {
  const store = await cookies();
  for (const base of SESSION_COOKIES) {
    let raw = store.get(base)?.value;
    if (!raw) {
      const parts: string[] = [];
      for (let i = 0; ; i++) {
        const chunk = store.get(`${base}.${i}`)?.value;
        if (!chunk) break;
        parts.push(chunk);
      }
      if (parts.length) raw = parts.join("");
    }
    if (!raw) continue;
    const decoded = await decode({ token: raw, secret: process.env.AUTH_SECRET!, salt: base });
    if (!decoded) continue;
    const tenants = decoded.tenants as Record<string, { tid: string; refreshToken?: string }> | undefined;
    const activeTid = decoded.activeTid as string | undefined;
    const active = tenants && activeTid ? tenants[activeTid] : undefined;
    if (active?.refreshToken) return { tid: active.tid, refreshToken: active.refreshToken };
  }
  return null;
}

export async function discoverTenants(): Promise<DiscoveryResult> {
  const active = await activeRefreshToken();
  if (!active) return { tenants: [], error: "no-refresh-token" };

  // Refresh token → ARM access token. Confidential clients generally don't
  // rotate the refresh token, so we intentionally don't persist a new one here.
  let armToken = "";
  try {
    const res = await fetch(`https://login.microsoftonline.com/${active.tid}/oauth2/v2.0/token`, {
      method: "POST",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({
        client_id: CLIENT_ID,
        client_secret: CLIENT_SECRET,
        grant_type: "refresh_token",
        refresh_token: active.refreshToken,
        scope: ARM_SCOPE,
      }),
      cache: "no-store",
    });
    const data = (await res.json().catch(() => ({}))) as {
      access_token?: string;
      error?: string;
      error_description?: string;
    };
    if (!res.ok || !data.access_token) {
      // AADSTS65001 / invalid_grant here means the ARM delegated permission
      // hasn't been consented for this app in the active tenant.
      const blob = `${data.error ?? ""} ${data.error_description ?? ""}`;
      const needsConsent = /AADSTS65001|consent_required|interaction_required|invalid_grant/i.test(blob);
      return { tenants: [], error: needsConsent ? "consent" : data.error ?? "arm-token" };
    }
    armToken = data.access_token;
  } catch {
    return { tenants: [], error: "arm-token" };
  }

  try {
    const res = await fetch(`${ARM}/tenants?api-version=2022-12-01`, {
      headers: { Authorization: `Bearer ${armToken}` },
      cache: "no-store",
    });
    if (!res.ok) {
      return { tenants: [], error: `arm-${res.status}` };
    }
    const data = (await res.json()) as {
      value?: { tenantId?: string; displayName?: string; defaultDomain?: string }[];
    };
    const tenants = (data.value ?? [])
      .map((t) => ({
        tid: (t.tenantId ?? "").toLowerCase(),
        displayName: t.displayName ?? "",
        defaultDomain: t.defaultDomain ?? "",
      }))
      .filter((t) => t.tid);
    return { tenants };
  } catch {
    return { tenants: [], error: "arm-list" };
  }
}
