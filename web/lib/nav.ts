import {
  Activity,
  Bot,
  Boxes,
  Gauge,
  LayoutDashboard,
  Radar,
  ServerCog,
  Settings,
  type LucideIcon,
} from "lucide-react";
import type { Role } from "./types";

export interface NavItem {
  label: string;
  href: string;
  icon: LucideIcon;
  /** Short description used in the command palette. */
  hint: string;
}

export interface NavGroup {
  id: string;
  label?: string;
  items: NavItem[];
}

const PLATFORM_NAV: NavGroup[] = [
  {
    id: "operate",
    label: "Operate",
    items: [
      {
        label: "Fleet",
        href: "/",
        icon: Radar,
        hint: "Every tenant, version, and health signal at a glance",
      },
      {
        label: "Catalogue",
        href: "/catalog",
        icon: Boxes,
        hint: "Author agents, memory stores, and deployments; entitle tenants",
      },
      {
        label: "Metering",
        href: "/metering",
        icon: Gauge,
        hint: "Fleet-wide usage and cost showback",
      },
    ],
  },
  {
    id: "system",
    items: [
      {
        label: "Settings",
        href: "/settings",
        icon: Settings,
        hint: "Platform configuration and access",
      },
    ],
  },
];

const TENANT_NAV: NavGroup[] = [
  {
    id: "manage",
    label: "Manage",
    items: [
      {
        label: "Overview",
        href: "/",
        icon: LayoutDashboard,
        hint: "Install state, agent health, and usage for your tenant",
      },
      {
        label: "Catalogue",
        href: "/catalog",
        icon: Boxes,
        hint: "Browse and enable agents, memory stores, and deployments",
      },
      {
        label: "Agents",
        href: "/agents",
        icon: Bot,
        hint: "Configure, publish, and monitor enabled agents",
      },
      {
        label: "Install",
        href: "/install",
        icon: ServerCog,
        hint: "Cortex app install and enrollment status",
      },
      {
        label: "Usage",
        href: "/usage",
        icon: Activity,
        hint: "Consumption and cost showback for your tenant",
      },
    ],
  },
  {
    id: "system",
    items: [
      {
        label: "Settings",
        href: "/settings",
        icon: Settings,
        hint: "Tenant settings, admins, and connectors",
      },
    ],
  },
];

export function navForRole(role: Role): NavGroup[] {
  return role === "platform" ? PLATFORM_NAV : TENANT_NAV;
}

export function homeForRole(role: Role): string {
  return "/";
}

export function allNavItems(role: Role): NavItem[] {
  return navForRole(role).flatMap((g) => g.items);
}
