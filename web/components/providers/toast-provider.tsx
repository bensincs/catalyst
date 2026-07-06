"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { Check, Info, TriangleAlert, X, CircleX } from "lucide-react";
import type { HealthMeta } from "@/lib/types";
import styles from "./toast-provider.module.css";

type Tone = HealthMeta["tone"];

interface ToastInput {
  title: string;
  description?: string;
  tone?: Tone;
  duration?: number;
}

interface Toast extends Required<Omit<ToastInput, "description">> {
  id: number;
  description?: string;
  leaving: boolean;
}

interface ToastApi {
  toast: (input: ToastInput) => number;
  dismiss: (id: number) => void;
}

const ToastContext = createContext<ToastApi | null>(null);

const ICONS: Record<Tone, typeof Check> = {
  success: Check,
  info: Info,
  warning: TriangleAlert,
  danger: CircleX,
  neutral: Info,
};

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [mounted, setMounted] = useState(false);
  const timers = useRef<Map<number, ReturnType<typeof setTimeout>>>(new Map());
  const idRef = useRef(0);

  useEffect(() => setMounted(true), []);

  const remove = useCallback((id: number) => {
    setToasts((list) => list.filter((t) => t.id !== id));
    const timer = timers.current.get(id);
    if (timer) {
      clearTimeout(timer);
      timers.current.delete(id);
    }
  }, []);

  const dismiss = useCallback(
    (id: number) => {
      setToasts((list) =>
        list.map((t) => (t.id === id ? { ...t, leaving: true } : t)),
      );
      window.setTimeout(() => remove(id), 160);
    },
    [remove],
  );

  const toast = useCallback(
    (input: ToastInput) => {
      const id = ++idRef.current;
      const next: Toast = {
        id,
        title: input.title,
        description: input.description,
        tone: input.tone ?? "neutral",
        duration: input.duration ?? 4200,
        leaving: false,
      };
      setToasts((list) => [...list, next]);
      const timer = setTimeout(() => dismiss(id), next.duration);
      timers.current.set(id, timer);
      return id;
    },
    [dismiss],
  );

  useEffect(() => {
    const map = timers.current;
    return () => {
      map.forEach((t) => clearTimeout(t));
      map.clear();
    };
  }, []);

  const api = useMemo<ToastApi>(() => ({ toast, dismiss }), [toast, dismiss]);

  return (
    <ToastContext.Provider value={api}>
      {children}
      {mounted &&
        createPortal(
          <div className={styles.viewport} role="region" aria-label="Notifications">
            <ol className={styles.stack} aria-live="polite">
              {toasts.map((t) => {
                const Icon = ICONS[t.tone];
                return (
                  <li
                    key={t.id}
                    className={styles.toast}
                    data-tone={t.tone}
                    data-leaving={t.leaving || undefined}
                  >
                    <span className={styles.icon} aria-hidden>
                      <Icon size={15} strokeWidth={2.4} />
                    </span>
                    <div className={styles.body}>
                      <p className={styles.title}>{t.title}</p>
                      {t.description && (
                        <p className={styles.description}>{t.description}</p>
                      )}
                    </div>
                    <button
                      type="button"
                      className={styles.close}
                      onClick={() => dismiss(t.id)}
                      aria-label="Dismiss notification"
                    >
                      <X size={14} strokeWidth={2.4} />
                    </button>
                  </li>
                );
              })}
            </ol>
          </div>,
          document.body,
        )}
    </ToastContext.Provider>
  );
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("useToast must be used within ToastProvider");
  return ctx;
}
