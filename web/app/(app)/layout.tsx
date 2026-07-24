import type { ReactNode } from "react";
import { redirect } from "next/navigation";
import { CloudOff } from "lucide-react";
import { auth } from "@/auth";
import { ApiError, getFleet, getMe, getMyContext, type Me } from "@/lib/api";
import { ConsoleProvider, type ConsoleData } from "@/components/providers/console-provider";
import { ToastProvider } from "@/components/providers/toast-provider";
import { AppShell } from "@/components/shell/app-shell";
import { PendingApproval } from "@/components/views/pending-approval";
import { ErrorState } from "@/components/ui/error-state";
import { RetryButton } from "@/components/ui/retry-button";
import type { Environment, TenantContextInfo, TenantSummary } from "@/lib/types";
import type { SessionTenant } from "@/types/next-auth";

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

export default async function AppLayout({ children }: { children: ReactNode }) {
  let me: Me;
  let tenants: TenantSummary[] = [];
  let activeTenant: TenantContextInfo | null = null;
  let myTenants: SessionTenant[] = [];
  let activeTid = "";
  let activeTenantSlug = "";

  try {
    me = await getMe();
    const session = await auth();
    myTenants = session?.tenants ?? [];
    activeTid = session?.activeTid ?? me.tid;
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
    // The control plane is unreachable (or erroring) — render a calm, retryable
    // state instead of a raw 500. Tenants and their agents keep running; the
    // console just can't read their state until the connection is restored.
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
    myTenants,
    activeTid,
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
