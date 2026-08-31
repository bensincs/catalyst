import type { ReactNode } from "react";
import { redirect } from "next/navigation";
import { CloudOff, ShieldAlert } from "lucide-react";
import { auth } from "@/auth";
import { ApiError, getFleet, getMe, getMyContext, type Me } from "@/lib/api";
import { ConsoleProvider, type ConsoleData } from "@/components/providers/console-provider";
import { ToastProvider } from "@/components/providers/toast-provider";
import { AppShell } from "@/components/shell/app-shell";
import { PendingApproval } from "@/components/views/pending-approval";
import { ErrorState } from "@/components/ui/error-state";
import { RetryButton } from "@/components/ui/retry-button";
import type { Environment, TenantContextInfo, TenantSummary } from "@/lib/types";

// Every authed page reads the signed-in session and the control-plane API per
// request — there is nothing to prerender. Force dynamic so `next build` never
// tries to render (and fetch the API from) these routes with no session.
export const dynamic = "force-dynamic";

function initialsFrom(name: string, email: string): string {
  const src = (name || email || "?").trim();
  const parts = src.split(/[\s@.]+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return src.slice(0, 2).toUpperCase();
}

/** Turn the API's error code into something the person reading it can act on.
 *  These are all "your access", not "the system is broken". */
function forbiddenReason(code?: string): { title: string; description: string } {
  switch (code) {
    case "tenant_deleted":
      return {
        title: "This organization was deleted",
        description:
          "A platform administrator deleted your organization from Cortex, so signing in no longer creates one. If this wasn't expected, ask them to restore it — deleted organizations can be brought back from the fleet view.",
      };
    case "tenant_disabled":
      return {
        title: "Waiting for approval",
        description:
          "Your organization is registered but hasn't been enabled yet. A platform administrator needs to approve it before you can use Cortex.",
      };
    case "no_membership":
      return {
        title: "No access to this organization",
        description:
          "Your account isn't a member of any enabled organization. Ask a platform administrator to add you.",
      };
    default:
      return {
        title: "Access denied",
        description:
          "Your account doesn't have access to this part of Cortex. Ask a platform administrator to check your assignment.",
      };
  }
}

export default async function AppLayout({ children }: { children: ReactNode }) {
  let me: Me;
  let tenants: TenantSummary[] = [];
  let activeTenant: TenantContextInfo | null = null;
  let activeTenantSlug = "";

  try {
    me = await getMe();
    const session = await auth();
    activeTenantSlug = session?.activeTenantSlug ?? "";
    if (me.role === "tenant" && me.tenant && !me.tenant.enabled) {
      // Signed in, but the organization isn't enabled yet — show a pending
      // screen instead of the app (all other API routes are gated anyway).
      return <PendingApproval tenantName={me.tenant.name} email={me.email} />;
    }
    if (me.role === "platform") {
      tenants = (await getFleet()).tenants;
    } else {
      activeTenant = (await getMyContext()).tenant;
    }
  } catch (e) {
    // Session alive but token missing/expired → send them back to sign in.
    if (e instanceof ApiError && e.status === 401) redirect("/signin");

    // A 403 is a permissions answer, not a connectivity failure. Saying
    // "unreachable" for one sends people looking at the control plane when the
    // actual answer is about their access — so name the real reason.
    if (e instanceof ApiError && e.status === 403) {
      const { title, description } = forbiddenReason(e.code);
      return <ErrorState variant="page" icon={ShieldAlert} title={title} description={description} />;
    }

    // Anything else — the control plane really is unreachable (or erroring).
    // Tenants and their agents keep running; the console just can't read their
    // state until the connection is restored.
    return (
      <ErrorState
        variant="page"
        icon={CloudOff}
        title="Control plane unreachable"
        description="Cortex can't reach the control-plane API right now. Your tenants and their agents keep running — the console just can't read their state until the connection returns."
        action={<RetryButton />}
      />
    );
  }

  const data: ConsoleData = {
    role: me.role,
    user: {
      name: me.name || me.email || "Signed in",
      email: me.email,
      initials: initialsFrom(me.name, me.email),
    },
    env: (process.env.NEXT_PUBLIC_CORTEX_ENV as Environment) ?? "dev",
    tenants,
    activeTenant,
    cortexTenants: me.tenants ?? [],
    activeTenantSlug,
  };

  return (
    <ConsoleProvider value={data}>
      <ToastProvider>
        <AppShell>{children}</AppShell>
      </ToastProvider>
    </ConsoleProvider>
  );
}
