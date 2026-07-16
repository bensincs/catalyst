import "server-only";
import { createHmac, randomBytes, timingSafeEqual } from "node:crypto";

// Targeted, tenant-scoped OAuth to connect *another* directory the signed-in
// human is a guest in. The trick that makes multi-tenant work: we hit the
// authorize/token endpoints at `/{tid}/…` (not `/common`), so Entra mints a
// token for THAT specific directory instead of defaulting to the home tenant.
// (A plain `/common` sign-in with `select_account` re-logs the same directory
// for a B2B guest, since the account is identical.)

const CLIENT_ID = process.env.AUTH_MICROSOFT_ENTRA_ID_ID ?? "";
const CLIENT_SECRET = process.env.AUTH_MICROSOFT_ENTRA_ID_SECRET ?? "";
const SECRET = process.env.AUTH_SECRET ?? "";
const API_SCOPE = CLIENT_ID ? `api://${CLIENT_ID}/access_as_user` : "";

export const CONNECT_SCOPES = ["openid", "profile", "email", "offline_access", API_SCOPE]
  .filter(Boolean)
  .join(" ");

/** Short-lived, httpOnly cookie holding the signed CSRF state for a connect. */
export const CONNECT_COOKIE = "cortex-tenant-connect";

export function baseUrl(): string {
  return (process.env.AUTH_URL ?? "http://localhost:4200").replace(/\/+$/, "");
}
export function redirectUri(): string {
  return `${baseUrl()}/api/tenants/callback`;
}

export const isGuid = (s: string): boolean =>
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(s);

/* ── Signed CSRF state (HMAC over the payload with AUTH_SECRET) ────────────── */

export type ConnectState = { tid: string; name: string; returnTo: string; nonce: string };

const b64 = (v: string | Buffer): string => Buffer.from(v).toString("base64url");

export function signState(tid: string, name: string, returnTo: string): string {
  const payload = b64(JSON.stringify({ tid, name, returnTo, nonce: randomBytes(16).toString("base64url") } satisfies ConnectState));
  const sig = createHmac("sha256", SECRET).update(payload).digest("base64url");
  return `${payload}.${sig}`;
}

export function verifyState(raw: string | undefined | null): ConnectState | null {
  if (!raw) return null;
  const [payload, sig] = raw.split(".");
  if (!payload || !sig) return null;
  const expected = createHmac("sha256", SECRET).update(payload).digest("base64url");
  const a = Buffer.from(sig);
  const b = Buffer.from(expected);
  if (a.length !== b.length || !timingSafeEqual(a, b)) return null;
  try {
    return JSON.parse(Buffer.from(payload, "base64url").toString()) as ConnectState;
  } catch {
    return null;
  }
}

/* ── Entra endpoints ──────────────────────────────────────────────────────── */

export function authorizeUrl(tid: string, state: string, loginHint: string): string {
  const params = new URLSearchParams({
    client_id: CLIENT_ID,
    response_type: "code",
    redirect_uri: redirectUri(),
    response_mode: "query",
    scope: CONNECT_SCOPES,
    state,
  });
  if (loginHint) params.set("login_hint", loginHint);
  return `https://login.microsoftonline.com/${encodeURIComponent(tid)}/oauth2/v2.0/authorize?${params}`;
}

export type ExchangedToken = {
  accessToken: string;
  refreshToken?: string;
  expiresAt: number;
  tid: string;
  oid: string;
  upn: string;
};

/** Trade the authorization code for tokens at the tenant-scoped token endpoint. */
export async function exchangeCode(tid: string, code: string): Promise<ExchangedToken> {
  const res = await fetch(`https://login.microsoftonline.com/${encodeURIComponent(tid)}/oauth2/v2.0/token`, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({
      client_id: CLIENT_ID,
      client_secret: CLIENT_SECRET,
      grant_type: "authorization_code",
      code,
      redirect_uri: redirectUri(),
      scope: CONNECT_SCOPES,
    }),
  });
  const data = (await res.json().catch(() => ({}))) as {
    access_token?: string;
    refresh_token?: string;
    expires_in?: number;
    error_description?: string;
  };
  if (!res.ok || !data.access_token) {
    throw new Error(data.error_description ?? `token exchange failed (${res.status})`);
  }
  // The access token is minted for our own API (aud = app id) and arrives
  // straight from Entra over TLS, so reading its claims without re-verifying the
  // signature is safe here — we only need tid/oid to label the bundle.
  const claims = decodeJwtClaims(data.access_token);
  return {
    accessToken: data.access_token,
    refreshToken: data.refresh_token,
    expiresAt: Math.floor(Date.now() / 1000) + Number(data.expires_in ?? 3600),
    tid: String(claims.tid ?? ""),
    oid: String(claims.oid ?? claims.sub ?? ""),
    upn: String(claims.preferred_username ?? claims.upn ?? ""),
  };
}

function decodeJwtClaims(jwt: string): Record<string, unknown> {
  const part = jwt.split(".")[1];
  if (!part) return {};
  try {
    return JSON.parse(Buffer.from(part, "base64url").toString()) as Record<string, unknown>;
  } catch {
    return {};
  }
}
