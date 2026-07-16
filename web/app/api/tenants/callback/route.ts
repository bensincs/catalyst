import { NextResponse, type NextRequest } from "next/server";
import { auth, unstable_update, type TenantToken } from "@/auth";
import { CONNECT_COOKIE, exchangeCode, verifyState } from "@/lib/tenant-connect";

// Return leg of the targeted connect flow. Verify the CSRF state, trade the
// code for that directory's tokens, and merge the resulting bundle into the
// session (the jwt callback in auth.ts handles `connectTenant`, and makes the
// newly connected directory active).
export async function GET(req: NextRequest) {
  const session = await auth();
  if (!session) {
    return NextResponse.redirect(new URL("/signin", req.url));
  }

  const url = new URL(req.url);
  const cookie = req.cookies.get(CONNECT_COOKIE)?.value;

  const fail = (reason: string) => {
    const r = NextResponse.redirect(new URL(`/settings?tenant=${reason}`, req.url));
    r.cookies.delete(CONNECT_COOKIE);
    return r;
  };

  if (url.searchParams.get("error")) return fail("denied");

  const code = url.searchParams.get("code");
  const stateParam = url.searchParams.get("state");
  // CSRF: the returned state must exactly match the cookie we set, and the
  // cookie's HMAC must verify (recovering the target tid + label).
  if (!code || !stateParam || !cookie || stateParam !== cookie) return fail("state");
  const state = verifyState(cookie);
  if (!state) return fail("state");

  let tok;
  try {
    tok = await exchangeCode(state.tid, code);
  } catch {
    return fail("exchange");
  }
  // Guard against a directory mix-up: the token's tid must be the one we asked
  // for. (Also skips the degenerate "connected my own home tenant again".)
  if (tok.tid.toLowerCase() !== state.tid.toLowerCase()) return fail("mismatch");

  const bundle: TenantToken = {
    tid: state.tid,
    oid: tok.oid,
    name: state.name || (tok.upn.split("@")[1] ?? ""),
    accessToken: tok.accessToken,
    refreshToken: tok.refreshToken,
    expiresAt: tok.expiresAt,
  };
  await unstable_update({ connectTenant: bundle } as never);

  const r = NextResponse.redirect(new URL(state.returnTo || "/settings", req.url));
  r.cookies.delete(CONNECT_COOKIE);
  return r;
}
