"use client";

import { signOut } from "next-auth/react";
import { LogOut, Radar, Settings2, ShieldCheck, UserCog } from "lucide-react";
import { useConsole } from "@/components/providers/console-provider";
import { useToast } from "@/components/providers/toast-provider";
import { Menu, MenuItem, MenuSeparator } from "@/components/ui/menu";
import styles from "./account-menu.module.css";

export function AccountMenu() {
  const { user, role } = useConsole();
  const { toast } = useToast();
  const isPlatform = role === "platform";

  return (
    <Menu
      ariaLabel="Account"
      align="end"
      width={272}
      button={(props) => (
        <button {...props} type="button" className={styles.trigger} aria-label="Account menu">
          <span className={styles.avatar} aria-hidden>
            {user.initials}
          </span>
        </button>
      )}
    >
      {({ close }) => (
        <>
          <div className={styles.identity}>
            <span className={styles.avatarLg} aria-hidden>
              {user.initials}
            </span>
            <div className={styles.who}>
              <p className={styles.name}>{user.name}</p>
              <p className={styles.email}>{user.email}</p>
            </div>
          </div>

          <div className={styles.roleRow}>
            <span className={styles.rolePill} data-role={role}>
              {isPlatform ? (
                <Radar size={12} strokeWidth={2.4} />
              ) : (
                <ShieldCheck size={12} strokeWidth={2.4} />
              )}
              {isPlatform ? "Platform Admin" : "Tenant Admin"}
            </span>
          </div>

          <MenuSeparator />
          <MenuItem icon={<UserCog size={16} strokeWidth={2} />} onClick={() => { toast({ title: "Account", tone: "neutral" }); close(); }}>
            Account
          </MenuItem>
          <MenuItem icon={<Settings2 size={16} strokeWidth={2} />} onClick={() => { toast({ title: "Preferences", tone: "neutral" }); close(); }}>
            Preferences
          </MenuItem>
          <MenuSeparator />
          <MenuItem
            icon={<LogOut size={16} strokeWidth={2} />}
            onClick={() => {
              close();
              void signOut({ callbackUrl: "/signin" });
            }}
          >
            Sign out
          </MenuItem>
        </>
      )}
    </Menu>
  );
}
