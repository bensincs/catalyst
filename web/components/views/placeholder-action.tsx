"use client";

import { Button } from "@/components/ui/button";
import { useToast } from "@/components/providers/toast-provider";

// The interactive slice of PlaceholderPage, split out so the page itself can stay
// a Server Component (and pass a Lucide icon straight to EmptyState without
// tripping the server→client function-prop boundary).
export function PlaceholderAction({ label }: { label: string }) {
  const { toast } = useToast();
  return (
    <Button
      variant="primary"
      onClick={() =>
        toast({
          title: label,
          description: "This surface is part of the shell milestone; wiring lands next.",
          tone: "info",
        })
      }
    >
      {label}
    </Button>
  );
}
