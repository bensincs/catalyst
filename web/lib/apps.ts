import type { Application, PinnedApp } from "@/lib/types";

/** The enabled applications a tenant can open from inside the console.
 *
 *  An app opts in (embed) and the publisher names an icon; where it actually
 *  lives is a per-tenant fact — <hostname>.<the tenant's own domain> — so this
 *  can only be resolved with the tenant's context in hand. A tenant with no
 *  domain configured publishes nothing, so there is nothing to pin.
 */
export function pinnedAppsFor(ctx: {
  tenant: { ingress: { appsDomain?: string } };
  applications: Application[];
}): PinnedApp[] {
  const domain = (ctx.tenant.ingress.appsDomain ?? "").trim();
  if (!domain) return [];
  return ctx.applications
    .filter((a) => a.embed && a.enabled && a.exposeService)
    .map((a) => ({
      id: a.id,
      name: a.name,
      icon: a.icon ?? "",
      url: `https://${(a.hostname || a.id).toLowerCase()}.${domain}`,
    }));
}
