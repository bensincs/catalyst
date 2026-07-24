import { getMe, getMyContext } from "@/lib/api";
import { SettingsView } from "@/components/views/settings-view";

export default async function SettingsPage() {
  const me = await getMe();
  const tenant = me.role === "tenant" ? (await getMyContext()).tenant : null;
  return (
    <SettingsView
      identity={{ name: me.name, email: me.email, role: me.role, oid: me.oid, tid: me.tid }}
      tenant={tenant}
    />
  );
}
