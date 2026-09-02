import {
  AppWindow,
  BarChart3,
  Bot,
  Boxes,
  CalendarDays,
  CheckSquare,
  Database,
  FileText,
  Folder,
  Gauge,
  Globe,
  LayoutGrid,
  LifeBuoy,
  MessageSquare,
  Rocket,
  Search,
  Shield,
  Sparkles,
  Ticket,
  Wrench,
  type LucideIcon,
} from "lucide-react";

/** Icons a publisher can put on an application that appears in the sidebar.
 *
 *  A fixed set rather than free text. The name is stored in the catalog and
 *  rendered by a different codebase than the one that set it, so an arbitrary
 *  string would eventually name an icon that does not exist — and the failure
 *  would be a blank space in someone's navigation. Everything here is known to
 *  resolve, and anything unrecognised falls back rather than disappearing.
 */
export const APP_ICONS: { name: string; label: string; icon: LucideIcon }[] = [
  { name: "app-window", label: "Application", icon: AppWindow },
  { name: "check-square", label: "Tasks", icon: CheckSquare },
  { name: "layout-grid", label: "Dashboard", icon: LayoutGrid },
  { name: "bar-chart", label: "Analytics", icon: BarChart3 },
  { name: "message-square", label: "Messaging", icon: MessageSquare },
  { name: "file-text", label: "Documents", icon: FileText },
  { name: "folder", label: "Files", icon: Folder },
  { name: "calendar", label: "Calendar", icon: CalendarDays },
  { name: "ticket", label: "Tickets", icon: Ticket },
  { name: "search", label: "Search", icon: Search },
  { name: "globe", label: "Web", icon: Globe },
  { name: "database", label: "Data", icon: Database },
  { name: "bot", label: "Agent", icon: Bot },
  { name: "sparkles", label: "AI", icon: Sparkles },
  { name: "shield", label: "Security", icon: Shield },
  { name: "gauge", label: "Monitoring", icon: Gauge },
  { name: "wrench", label: "Tools", icon: Wrench },
  { name: "life-buoy", label: "Support", icon: LifeBuoy },
  { name: "boxes", label: "Platform", icon: Boxes },
  { name: "rocket", label: "Deployment", icon: Rocket },
];

const BY_NAME = new Map(APP_ICONS.map((i) => [i.name, i.icon]));

/** The icon for a stored name. Falls back to a generic application icon: a
 *  publisher who picked nothing, or an icon this build does not know, should
 *  still get a recognisable entry rather than a gap. */
export function appIcon(name?: string): LucideIcon {
  return (name && BY_NAME.get(name)) || AppWindow;
}
