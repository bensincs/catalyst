import { NextResponse, type NextRequest } from "next/server";
import { auth } from "@/auth";
import { CONNECT_COOKIE, authorizeUrl, isGuid, signState } from "@/lib/tenant-connect";

// Begin connecting another directory: redirect the signed-in user to Entra's
// tenant-scoped authorize endpoint. We stash a signed CSRF state in a
// short-lived httpOnly cookie and compare it on the way back (see ./callback).
export async function GET(req: NextRequest, { params }: { params: Promise<{ tid: string }> }) {
  const session = await auth();
  if (!session) {
    return NextResponse.redirect(new URL("/signin", req.url));
  }

  const { tid } = await params;
  const dir = (tid ?? "").trim().toLowerCase();
  if (!isGuid(dir)) {
    return NextResponse.redirect(new URL("/settings?tenant=badid", req.url));
  }

  const url = new URL(req.url);
  const name = (url.searchParams.get("name") ?? "").slice(0, 120);
  const returnTo = "/settings";
  const state = signState(dir, name, returnTo);

  const res = NextResponse.redirect(authorizeUrl(dir, state, session.user?.email ?? ""));
  res.cookies.set(CONNECT_COOKIE, state, {
    httpOnly: true,
    secure: process.env.NODE_ENV === "production",
    sameSite: "lax", // survives the top-level GET redirect back from Entra
    path: "/",
    maxAge: 600,
  });
  return res;
}
