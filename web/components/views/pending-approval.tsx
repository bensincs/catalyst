"use client";

import { Clock, LogOut } from "lucide-react";
import { signOut } from "next-auth/react";
import { ErrorState } from "@/components/ui/error-state";
import { Button } from "@/components/ui/button";

/** Shown when a signed-in tenant user's organization hasn't been enabled by a
 * platform admin yet. It replaces the whole app shell — there's nothing they can
 * do until they're approved, except sign out. */
export function PendingApproval({ tenantName, email }: { tenantName: string; email: string }) {
  return (
    <ErrorState
      variant="page"
      icon={Clock}
      title="Pending approval"
      description={`${tenantName || "Your organization"} isn't enabled yet. A Cortex administrator needs to approve it before you can use the console — you'll get in as soon as it's enabled. Signed in as ${email}.`}
      action={
        <Button icon={LogOut} onClick={() => signOut({ callbackUrl: "/signin" })}>
          Sign out
        </Button>
      }
    />
  );
}
