import {
  Activity,
  Bot,
  Boxes,
  Database,
  Gauge,
  LayoutDashboard,
  Radar,
  Rocket,
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
        label: "Agents",
        href: "/agents",
        icon: Bot,
        hint: "Author agents, publish versions, and entitle tenants",
      },
      {
        label: "Memory stores",
        href: "/memory-stores",
        icon: Database,
        hint: "Author shared memory stores and entitle tenants",
      },
      {
        label: "Deployments",
        href: "/deployments",
        icon: Rocket,
        hint: "Author Helm deployments and entitle tenants",
      },
      {
        label: "Infrastructure",
        href: "/infrastructure",
        icon: Boxes,
        hint: "Author Azure (Bicep) infrastructure and entitle tenants",
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
        label: "Agents",
        href: "/agents",
        icon: Bot,
        hint: "Enable, author, and monitor agents in your tenant",
      },
      {
        label: "Memory stores",
        href: "/memory-stores",
        icon: Database,
        hint: "Enable or author memory stores for your agents",
      },
      {
        label: "Deployments",
        href: "/deployments",
        icon: Rocket,
        hint: "Enable or author deployments in your cluster",
      },
      {
        label: "Infrastructure",
        href: "/infrastructure",
        icon: Boxes,
        hint: "Enable or author Azure infrastructure for your tenant",
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
