"use client";

import { useMemo, useState } from "react";
import { Eye, EyeOff, KeyRound, Power, ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Modal } from "@/components/ui/modal";
import { outstandingKeys, type SecretSet } from "@/lib/types";
import styles from "./secret-values-dialog.module.css";

/** Collects the values for a secret store's keys.
 *
 *  This is the only place in the console that accepts a secret, and the copy has
 *  to be honest about what that means, because the consequences are unusual:
 *
 *    - The values go to this tenant's own Azure Key Vault. They are not stored
 *      in the control plane and are not sent to anyone else.
 *    - Nothing can read them back afterwards — not this console, not a platform
 *      administrator, not support. The control plane writes them through an
 *      Azure interface that has no read operation, deliberately.
 *    - So a forgotten value cannot be recovered, only replaced.
 *
 *  Operators are used to "you won't see this again" being a soft claim that a
 *  support ticket can undo. Here it is literally true, and someone pasting their
 *  only copy of a production password deserves to know that before they hit
 *  save, not after.
 *
 *  Values already supplied are shown as set, never as content: there is no
 *  masked-but-present state, because the value genuinely is not available to
 *  put behind the mask. Leaving a field blank keeps what is already stored.
 */
export function SecretValuesDialog({
  set,
  open,
  onClose,
  onSubmit,
  pending = false,
}: {
  set: SecretSet;
  open: boolean;
  onClose: () => void;
  onSubmit: (values: Record<string, string>) => void;
  pending?: boolean;
}) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [shown, setShown] = useState<Record<string, boolean>>({});

  const already = useMemo(() => new Set(set.keysSet ?? []), [set.keysSet]);
  const outstanding = outstandingKeys(set);
  const firstRun = already.size === 0;

  // Every outstanding key needs a value before the set can do its job. Keys that
  // already have one may be left blank to keep it.
  const complete = outstanding.every((k) => (values[k] ?? "").trim() !== "");

  const close = () => {
    setValues({});
    setShown({});
    onClose();
  };

  return (
    <Modal
      open={open}
      onClose={close}
      title={firstRun ? `Set up ${set.name}` : `Update ${set.name}`}
      description={
        firstRun
          ? "These values are stored in your tenant's own Azure Key Vault and delivered to the deployments that need them."
          : "Fill in only what you want to change. Anything left blank keeps its current value."
      }
      footer={
        <>
          <Button variant="secondary" onClick={close} disabled={pending}>
            Cancel
          </Button>
          <Button
            variant="primary"
            icon={Power}
            disabled={!complete || pending}
            loading={pending}
            onClick={() => {
              const payload = Object.fromEntries(
                Object.entries(values).filter(([, v]) => v.trim() !== ""),
              );
              close();
              onSubmit(payload);
            }}
          >
            {firstRun ? "Save and enable" : "Save values"}
          </Button>
        </>
      }
    >
      <p className={styles.assurance}>
        <ShieldCheck size={15} strokeWidth={2.2} aria-hidden />
        <span>
          Once saved, <strong>nobody can read these back</strong> — not this console, not your
          platform administrator. Keep your own copy of anything you can&rsquo;t regenerate.
        </span>
      </p>

      <ul className={styles.fields} role="list">
        {set.keys.map((key) => {
          const isSet = already.has(key);
          const visible = shown[key] ?? false;
          const inputId = `secret-${set.id}-${key}`;
          return (
            <li key={key} className={styles.field}>
              <div className={styles.fieldTop}>
                <label htmlFor={inputId} className={`${styles.key} mono`}>
                  {key}
                </label>
                {isSet ? (
                  <span className={styles.stateSet}>Value stored</span>
                ) : (
                  <span className={styles.stateNeeded}>Needs a value</span>
                )}
              </div>
              <div className={styles.inputWrap}>
                <input
                  id={inputId}
                  className={`${styles.input} mono`}
                  // Not type="password": a password manager offering to fill or
                  // save a Postgres admin credential here is wrong, and the
                  // reveal toggle already covers shoulder-surfing.
                  type={visible ? "text" : "password"}
                  value={values[key] ?? ""}
                  onChange={(e) => setValues((v) => ({ ...v, [key]: e.target.value }))}
                  placeholder={isSet ? "Leave blank to keep the current value" : "Enter a value…"}
                  spellCheck={false}
                  autoComplete="off"
                  data-1p-ignore
                  data-lpignore="true"
                />
                <button
                  type="button"
                  className={styles.reveal}
                  onClick={() => setShown((s) => ({ ...s, [key]: !visible }))}
                  aria-label={visible ? `Hide ${key}` : `Show ${key}`}
                  aria-pressed={visible}
                >
                  {visible ? (
                    <EyeOff size={15} strokeWidth={2} aria-hidden />
                  ) : (
                    <Eye size={15} strokeWidth={2} aria-hidden />
                  )}
                </button>
              </div>
            </li>
          );
        })}
      </ul>

      {set.keys.length === 0 && (
        <p className={styles.none}>
          <KeyRound size={15} strokeWidth={2} aria-hidden />
          <span>This secret store declares no keys yet, so there is nothing to fill in.</span>
        </p>
      )}
    </Modal>
  );
}
