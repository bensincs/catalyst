"use client";

import { useFormStatus } from "react-dom";
import { Loader2 } from "lucide-react";
import styles from "./signin.module.css";

function MicrosoftLogo() {
  return (
    <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden>
      <rect x="1" y="1" width="6.5" height="6.5" fill="#F25022" />
      <rect x="8.5" y="1" width="6.5" height="6.5" fill="#7FBA00" />
      <rect x="1" y="8.5" width="6.5" height="6.5" fill="#00A4EF" />
      <rect x="8.5" y="8.5" width="6.5" height="6.5" fill="#FFB900" />
    </svg>
  );
}

export function SubmitButton() {
  const { pending } = useFormStatus();
  return (
    <button
      type="submit"
      className={styles.button}
      data-pending={pending || undefined}
      disabled={pending}
      aria-busy={pending}
    >
      {pending ? (
        <>
          <Loader2 size={16} strokeWidth={2.4} className={styles.spinner} aria-hidden />
          Redirecting to Microsoft&hellip;
        </>
      ) : (
        <>
          <MicrosoftLogo />
          Sign in with Microsoft
        </>
      )}
    </button>
  );
}
