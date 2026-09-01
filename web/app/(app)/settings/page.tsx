import { getMe, getMyContext, getUpstreams } from "@/lib/api";
import { SettingsView } from "@/components/views/settings-view";

export default async function SettingsPage() {
  const me = await getMe();
  const tenant = me.role === "tenant" ? (await getMyContext()).tenant : null;
  // Platform only, and non-fatal: the registry being unreachable must not take
  // the whole settings page down with it.
  const registry =
    me.role === "platform"
      ? await getUpstreams().catch(() => ({ registry: "", upstreams: [] }))
      : { registry: "", upstreams: [] };
  return (
    <SettingsView
      identity={{ name: me.name, email: me.email, role: me.role, oid: me.oid, tid: me.tid }}
      tenant={tenant}
      registry={registry.registry}
      upstreams={registry.upstreams}
    />
  );
}
