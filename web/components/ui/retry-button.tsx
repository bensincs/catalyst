"use client";

import type { ReactNode } from "react";
import { useRouter } from "next/navigation";
import { RotateCw } from "lucide-react";
import { Button } from "./button";

/**
 * Retry control for error states. In an error boundary, pass the boundary's
 * `reset`; on a server-rendered error page (no boundary), it falls back to a
 * router refresh, which re-runs the server render (and re-tries the fetch).
 */
export function RetryButton({
  onRetry,
  children = "Try again",
}: {
  onRetry?: () => void;
  children?: ReactNode;
}) {
  const router = useRouter();
  return (
    <Button variant="primary" icon={RotateCw} onClick={() => (onRetry ? onRetry() : router.refresh())}>
      {children}
    </Button>
  );
}
