"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import type {
  Environment,
  Role,
  TenantContextInfo,
  TenantSummary,
} from "@/lib/types";

type Theme = "light" | "dark";

export interface ConsoleUser {
  name: string;
  email: string;
  initials: string;
}

/** Server-provided, authoritative context for the signed-in session. */
export interface ConsoleData {
  role: Role;
  user: ConsoleUser;
  env: Environment;
  tenants: TenantSummary[]; // platform: the fleet; tenant: empty
  activeTenant: TenantContextInfo | null; // tenant: own; platform: null
  cortexTenants: TenantSummary[]; // every Cortex tenant the caller can operate (delegated + memberships)
  activeTenantSlug: string; // the explicitly-selected tenant slug ('' ⇒ primary)
}

interface ConsoleState extends ConsoleData {
  theme: Theme;
  toggleTheme: () => void;
  railCollapsed: boolean;
  toggleRail: () => void;
  paletteOpen: boolean;
  setPaletteOpen: (open: boolean) => void;
  mobileNavOpen: boolean;
  setMobileNavOpen: (open: boolean) => void;
  mounted: boolean;
}

const ConsoleContext = createContext<ConsoleState | null>(null);

export function ConsoleProvider({
  value,
  children,
}: {
  value: ConsoleData;
  children: ReactNode;
}) {
  const [theme, setThemeState] = useState<Theme>("light");
  const [railCollapsed, setRailCollapsed] = useState(false);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    const el = document.documentElement;
    setThemeState(el.dataset.theme === "dark" ? "dark" : "light");
    setRailCollapsed(el.dataset.rail === "collapsed");
    setMounted(true);
  }, []);

  const toggleTheme = useCallback(() => {
    setThemeState((prev) => {
      const next = prev === "dark" ? "light" : "dark";
      document.documentElement.dataset.theme = next;
      try {
        localStorage.setItem("cortex-theme", next);
      } catch {}
      return next;
    });
  }, []);

  const toggleRail = useCallback(() => {
    setRailCollapsed((prev) => {
      const next = !prev;
      document.documentElement.dataset.rail = next ? "collapsed" : "expanded";
      try {
        localStorage.setItem("cortex-rail", next ? "collapsed" : "expanded");
      } catch {}
      return next;
    });
  }, []);

  const state = useMemo<ConsoleState>(
    () => ({
      ...value,
      theme,
      toggleTheme,
      railCollapsed,
      toggleRail,
      paletteOpen,
      setPaletteOpen,
      mobileNavOpen,
      setMobileNavOpen,
      mounted,
    }),
    [value, theme, toggleTheme, railCollapsed, toggleRail, paletteOpen, mobileNavOpen, mounted],
  );

  return <ConsoleContext.Provider value={state}>{children}</ConsoleContext.Provider>;
}

export function useConsole(): ConsoleState {
  const ctx = useContext(ConsoleContext);
  if (!ctx) throw new Error("useConsole must be used within ConsoleProvider");
  return ctx;
}
