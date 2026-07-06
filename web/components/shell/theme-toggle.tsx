"use client";

import { Moon, Sun } from "lucide-react";
import { useConsole } from "@/components/providers/console-provider";
import styles from "./theme-toggle.module.css";

export function ThemeToggle() {
  const { theme, toggleTheme, mounted } = useConsole();
  const isDark = theme === "dark";

  return (
    <button
      type="button"
      className={styles.toggle}
      onClick={toggleTheme}
      aria-label={isDark ? "Switch to light theme" : "Switch to dark theme"}
      title={isDark ? "Switch to light theme" : "Switch to dark theme"}
    >
      <span className={styles.iconWrap} data-dark={isDark || undefined} suppressHydrationWarning>
        {mounted ? (
          isDark ? (
            <Sun size={17} strokeWidth={2} />
          ) : (
            <Moon size={17} strokeWidth={2} />
          )
        ) : (
          <Moon size={17} strokeWidth={2} />
        )}
      </span>
    </button>
  );
}
