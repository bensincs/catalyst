import type { ReactNode } from "react";
import { redirect } from "next/navigation";
import { ApiError, getFleet, getMe, getMyContext, type Me } from "@/lib/api";
import { ConsoleProvider, type ConsoleData } from "@/components/providers/console-provider";
import { ToastProvider } from "@/components/providers/toast-provider";
import { AppShell } from "@/components/shell/app-shell";
import type { Environment, TenantContextInfo, TenantSummary } from "@/lib/types";

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

  try {
    me = await getMe();
    if (me.role === "platform") {
      tenants = (await getFleet()).tenants;
    } else {
      activeTenant = (await getMyContext()).tenant;
    }
  } catch (e) {
    // Session alive but token missing/expired → send them back to sign in.
    if (e instanceof ApiError && e.status === 401) redirect("/signin");
    throw e;
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
  };

  return (
    <ConsoleProvider value={data}>
      <ToastProvider>
        <AppShell>{children}</AppShell>
      </ToastProvider>
    </ConsoleProvider>
  );
}
