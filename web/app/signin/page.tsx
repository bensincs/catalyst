import { redirect } from "next/navigation";
import { ShieldAlert } from "lucide-react";
import { auth, signIn } from "@/auth";
import { BrandGlyph } from "@/components/shell/brand-mark";
import { SubmitButton } from "./submit-button";
import styles from "./signin.module.css";

// Auth.js / Entra failure codes → calm, actionable copy. Anything unmapped falls
// back to a generic retry message rather than leaking a raw error code.
const ERROR_COPY: Record<string, string> = {
  AccessDenied:
    "Your account doesn't have access to Cortex yet. Ask your administrator to grant it, then sign in again.",
  Configuration: "Sign-in is temporarily misconfigured. Contact your Cortex administrator.",
  Verification: "That sign-in request expired. Please try again.",
  OAuthAccountNotLinked: "This identity is already linked to a different account.",
  SessionRequired: "Your session ended. Please sign in again.",
};

function errorMessage(code?: string): string | null {
  if (!code) return null;
  return ERROR_COPY[code] ?? "Sign-in didn't complete. Please try again.";
}

export default async function SignInPage({
  searchParams,
}: {
  searchParams: Promise<{ callbackUrl?: string; error?: string }>;
}) {
  const session = await auth();
  if (session?.user) redirect("/");
  const { callbackUrl, error } = await searchParams;
  const dest = callbackUrl && callbackUrl.startsWith("/") ? callbackUrl : "/";
  const errMsg = errorMessage(error);

  return (
    <main className={styles.page}>
      <div className={styles.grid} aria-hidden />
      <div className={styles.glow} aria-hidden />

      <div className={styles.card}>
        <div className={styles.brand}>
          <BrandGlyph size={28} />
          <div className={styles.wordmark}>
            Cortex
            <span className={styles.by}>by Inception</span>
          </div>
        </div>

        <h1 className={styles.title}>
          Operate your agent fleet
          <br />
          with certainty.
        </h1>
        <p className={styles.lede}>
          The control plane for AI agents on Microsoft Foundry. Agents run in your own tenant,
          under your own identity &mdash; Cortex governs the fleet, never your data.
        </p>

        {errMsg && (
          <div className={styles.alert} role="alert">
            <ShieldAlert size={16} strokeWidth={2.2} aria-hidden />
            <span>{errMsg}</span>
          </div>
        )}

        <form
          action={async () => {
            "use server";
            await signIn("microsoft-entra-id", { redirectTo: dest });
          }}
        >
          <SubmitButton />
        </form>

        <p className={styles.foot}>
          <span className={styles.dot} aria-hidden />
          Multi-tenant sign-in via Microsoft Entra ID. Your directory decides what you can access.
        </p>
      </div>
    </main>
  );
}
