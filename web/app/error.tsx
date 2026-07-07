"use client";

import { useEffect } from "react";
import { CloudOff } from "lucide-react";
import { ErrorState } from "@/components/ui/error-state";
import { RetryButton } from "@/components/ui/retry-button";

/**
 * Last-resort boundary for anything the authed layout and segment boundaries
 * don't catch (e.g. an error on a public route). The dominant real cause is an
 * unreachable control plane, so we lead with that and offer a retry.
 */
export default function RootError({
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
      variant="page"
      icon={CloudOff}
      title="Something went wrong"
      description="Cortex hit an unexpected error. Try again — if it persists, reload the console. Your tenants and agents are unaffected."
      action={<RetryButton onRetry={reset} />}
    />
  );
}
