"use client";

import { useEffect } from "react";
import { TriangleAlert } from "lucide-react";
import { ErrorState } from "@/components/ui/error-state";
import { RetryButton } from "@/components/ui/retry-button";

/**
 * Catches errors thrown while rendering an authed page (e.g. a per-view API
 * call fails while the shell itself loaded fine). Renders in the content area
 * with the nav intact; the layout handles the "whole control plane is down"
 * case separately.
 */
export default function AppSegmentError({
  error,
  reset,
}: {
  error: Error & { digest?: string };
  reset: () => void;
}) {
  useEffect(() => {
    console.error(error);
  }, [error]);

  return (
    <ErrorState
      variant="panel"
      icon={TriangleAlert}
      title="Couldn't load this view"
      description="Cortex hit an error rendering this page — usually a brief hiccup reaching the control plane. Retry; the fleet keeps running regardless."
      action={<RetryButton onRetry={reset} />}
    />
  );
}
