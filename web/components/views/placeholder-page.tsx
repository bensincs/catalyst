import type { LucideIcon } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { EmptyState } from "@/components/ui/empty-state";
import { PlaceholderAction } from "./placeholder-action";
import styles from "./placeholder-page.module.css";

// Server Component: renders a gated/coming-soon surface. Kept server-side so the
// Lucide `icon` component is consumed here (never serialized across the boundary);
// the optional action's interactivity lives in the PlaceholderAction client child.
export function PlaceholderPage({
  title,
  description,
  icon,
  emptyTitle,
  emptyBody,
  actionLabel,
}: {
  title: string;
  description: string;
  icon: LucideIcon;
  emptyTitle: string;
  emptyBody: string;
  actionLabel?: string;
}) {
  return (
    <div>
      <PageHeader title={title} description={description} />
      <div className={styles.panel}>
        <EmptyState
          icon={icon}
          title={emptyTitle}
          description={emptyBody}
          action={actionLabel ? <PlaceholderAction label={actionLabel} /> : undefined}
        />
      </div>
    </div>
  );
}
