"use client";

import type { LucideIcon } from "lucide-react";
import { PageHeader } from "@/components/ui/page-header";
import { EmptyState } from "@/components/ui/empty-state";
import { Button } from "@/components/ui/button";
import { useToast } from "@/components/providers/toast-provider";
import styles from "./placeholder-page.module.css";

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
  const { toast } = useToast();
  return (
    <div>
      <PageHeader title={title} description={description} />
      <div className={styles.panel}>
        <EmptyState
          icon={icon}
          title={emptyTitle}
          description={emptyBody}
          action={
            actionLabel ? (
              <Button
                variant="primary"
                onClick={() =>
                  toast({
                    title: `${actionLabel}`,
                    description: "This surface is part of the shell milestone; wiring lands next.",
                    tone: "info",
                  })
                }
              >
                {actionLabel}
              </Button>
            ) : undefined
          }
        />
      </div>
    </div>
  );
}
