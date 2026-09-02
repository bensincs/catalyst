import { notFound } from "next/navigation";
import { getMe, getMyContext } from "@/lib/api";
import { pinnedAppsFor } from "@/lib/apps";
import { EmbeddedApp } from "@/components/views/embedded-app";

export const dynamic = "force-dynamic";

// Opening an installed application inside the console. Resolved from the
// tenant's own context rather than from an id in the URL alone: which apps are
// installed, and where each one lives, are per-tenant facts. An id that is not
// among them is a 404 — a platform admin, or a tenant that has not enabled it,
// must not be able to frame someone else's application by guessing.
export default async function AppPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const me = await getMe();
  if (me.role !== "tenant") notFound();

  const app = pinnedAppsFor(await getMyContext()).find((a) => a.id === id);
  if (!app) notFound();

  return <EmbeddedApp app={app} />;
}
