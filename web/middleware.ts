import { auth } from "@/auth";
import { NextResponse } from "next/server";

// Protect the whole console; allow the sign-in page, auth routes, and assets.
export default auth((req) => {
  const { pathname } = req.nextUrl;

  // Public onboarding assets (the Lighthouse "Deploy to Azure" template) are
  // fetched cross-origin, client-side, by the Azure portal — which refuses the
  // template unless the endpoint answers with CORS. Serve them permissively
  // (the template is public, non-secret, and read-only).
  if (pathname.startsWith("/onboarding/")) {
    const res = req.method === "OPTIONS" ? new NextResponse(null, { status: 204 }) : NextResponse.next();
    res.headers.set("Access-Control-Allow-Origin", "*");
    res.headers.set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS");
    res.headers.set("Access-Control-Allow-Headers", "*");
    res.headers.set("Access-Control-Max-Age", "3600");
    return res;
  }

  const isPublic =
    pathname === "/signin" ||
    pathname.startsWith("/api/auth") ||
    pathname.startsWith("/_next") ||
    pathname === "/favicon.ico";

  if (isPublic) return;

  if (!req.auth) {
    const url = new URL("/signin", req.nextUrl.origin);
    url.searchParams.set("callbackUrl", pathname + req.nextUrl.search);
    return Response.redirect(url);
  }
});

export const config = {
  matcher: ["/((?!_next/static|_next/image|favicon.ico).*)"],
};
