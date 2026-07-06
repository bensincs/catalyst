"use client";

import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useRouter } from "next/navigation";
import {
  ArrowRight,
  CornerDownLeft,
  Moon,
  PanelLeft,
  Search,
  Sun,
} from "lucide-react";
import { useConsole } from "@/components/providers/console-provider";
import { allNavItems } from "@/lib/nav";
import { LIFECYCLE_META } from "@/lib/types";
import { StatusDot } from "@/components/ui/status";
import styles from "./command-palette.module.css";

interface Command {
  id: string;
  label: string;
  group: string;
  icon: ReactNode;
  hint?: string;
  keywords?: string;
  run: () => void;
}

const GROUP_ORDER = ["Navigate", "Tenants", "Appearance"];

function subsequenceScore(query: string, text: string): number | null {
  if (!query) return 0;
  const q = query.toLowerCase();
  const t = text.toLowerCase();
  const direct = t.indexOf(q);
  if (direct !== -1) return 1000 - direct - (t.length - q.length) * 0.1;
  let qi = 0;
  let score = 0;
  let lastIdx = -1;
  for (let ti = 0; ti < t.length && qi < q.length; ti++) {
    if (t[ti] === q[qi]) {
      score += lastIdx === ti - 1 ? 5 : 1;
      lastIdx = ti;
      qi++;
    }
  }
  return qi === q.length ? score : null;
}

export function CommandPalette() {
  const {
    role,
    tenants,
    theme,
    toggleTheme,
    railCollapsed,
    toggleRail,
    paletteOpen,
    setPaletteOpen,
  } = useConsole();
  const router = useRouter();

  const dialogRef = useRef<HTMLDialogElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const listRef = useRef<HTMLDivElement | null>(null);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);

  const close = useCallback(() => setPaletteOpen(false), [setPaletteOpen]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen(!paletteOpen);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [paletteOpen, setPaletteOpen]);

  useEffect(() => {
    const dlg = dialogRef.current;
    if (!dlg) return;
    if (paletteOpen && !dlg.open) {
      dlg.showModal();
      setQuery("");
      setActive(0);
      requestAnimationFrame(() => inputRef.current?.focus());
    } else if (!paletteOpen && dlg.open) {
      dlg.close();
    }
  }, [paletteOpen]);

  const commands = useMemo<Command[]>(() => {
    const list: Command[] = [];

    for (const item of allNavItems(role)) {
      const Icon = item.icon;
      list.push({
        id: `nav:${item.href}`,
        label: item.label,
        group: "Navigate",
        hint: item.hint,
        icon: <Icon size={16} strokeWidth={2} />,
        run: () => {
          router.push(item.href);
          close();
        },
      });
    }

    if (role === "platform") {
      for (const t of tenants) {
        list.push({
          id: `tenant:${t.id}`,
          label: t.name,
          group: "Tenants",
          hint: `${t.region} · ${LIFECYCLE_META[t.lifecycle].label}`,
          keywords: `${t.tenantId} ${t.plan}`,
          icon: <StatusDot tone={LIFECYCLE_META[t.lifecycle].tone} />,
          run: () => {
            router.push(`/tenants/${t.id}`);
            close();
          },
        });
      }
    }

    list.push({
      id: "theme",
      label: theme === "dark" ? "Switch to light theme" : "Switch to dark theme",
      group: "Appearance",
      keywords: "theme dark light mode",
      icon: theme === "dark" ? <Sun size={16} strokeWidth={2} /> : <Moon size={16} strokeWidth={2} />,
      run: () => {
        toggleTheme();
        close();
      },
    });
    list.push({
      id: "rail",
      label: railCollapsed ? "Expand sidebar" : "Collapse sidebar",
      group: "Appearance",
      keywords: "sidebar rail navigation",
      icon: <PanelLeft size={16} strokeWidth={2} />,
      run: () => {
        toggleRail();
        close();
      },
    });

    return list;
  }, [role, tenants, theme, railCollapsed, router, toggleTheme, toggleRail, close]);

  const filtered = useMemo(() => {
    const scored = commands
      .map((c) => {
        const hay = `${c.label} ${c.group} ${c.hint ?? ""} ${c.keywords ?? ""}`;
        const s = subsequenceScore(query, hay);
        return s === null ? null : { command: c, score: s };
      })
      .filter((x): x is { command: Command; score: number } => x !== null);

    if (query) scored.sort((a, b) => b.score - a.score);

    const groups = new Map<string, Command[]>();
    for (const { command } of scored) {
      if (!groups.has(command.group)) groups.set(command.group, []);
      groups.get(command.group)!.push(command);
    }
    const ordered: { group: string; items: Command[] }[] = [];
    for (const g of GROUP_ORDER) {
      if (groups.has(g)) ordered.push({ group: g, items: groups.get(g)! });
    }
    return { ordered, flat: ordered.flatMap((o) => o.items) };
  }, [commands, query]);

  useEffect(() => setActive(0), [query]);

  useEffect(() => {
    const el = listRef.current?.querySelector<HTMLElement>(`[data-index="${active}"]`);
    el?.scrollIntoView({ block: "nearest" });
  }, [active]);

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((i) => Math.min(i + 1, filtered.flat.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((i) => Math.max(i - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      filtered.flat[active]?.run();
    } else if (e.key === "Home") {
      e.preventDefault();
      setActive(0);
    } else if (e.key === "End") {
      e.preventDefault();
      setActive(filtered.flat.length - 1);
    }
  };

  let runningIndex = -1;

  return (
    <dialog
      ref={dialogRef}
      className={styles.dialog}
      aria-label="Command palette"
      onClose={close}
      onCancel={close}
      onClick={(e) => {
        if (e.target === dialogRef.current) close();
      }}
    >
      <div className={styles.panel} role="combobox" aria-expanded aria-haspopup="listbox" aria-owns="cmdk-list">
        <div className={styles.searchRow}>
          <Search size={17} strokeWidth={2} className={styles.searchIcon} aria-hidden />
          <input
            ref={inputRef}
            className={styles.input}
            placeholder="Search or jump to…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={onKeyDown}
            role="searchbox"
            aria-label="Search commands"
            aria-controls="cmdk-list"
            aria-activedescendant={filtered.flat[active] ? `cmd-${filtered.flat[active].id}` : undefined}
            autoComplete="off"
            spellCheck={false}
          />
          <kbd className={styles.escHint}>Esc</kbd>
        </div>

        <div className={styles.list} id="cmdk-list" role="listbox" ref={listRef}>
          {filtered.flat.length === 0 ? (
            <div className={styles.empty}>
              <p className={styles.emptyTitle}>No matches for &ldquo;{query}&rdquo;</p>
              <p className={styles.emptyHint}>Try a page name, tenant, or &ldquo;theme&rdquo;.</p>
            </div>
          ) : (
            filtered.ordered.map(({ group, items }) => (
              <div key={group} className={styles.group} role="group" aria-label={group}>
                <div className={styles.groupLabel}>{group}</div>
                {items.map((c) => {
                  runningIndex++;
                  const index = runningIndex;
                  return (
                    <button
                      key={c.id}
                      id={`cmd-${c.id}`}
                      type="button"
                      role="option"
                      aria-selected={index === active}
                      data-index={index}
                      data-active={index === active || undefined}
                      className={styles.item}
                      onMouseMove={() => setActive(index)}
                      onClick={() => c.run()}
                    >
                      <span className={styles.itemIcon} aria-hidden>
                        {c.icon}
                      </span>
                      <span className={styles.itemLabel}>{c.label}</span>
                      {c.hint && <span className={styles.itemHint}>{c.hint}</span>}
                      <ArrowRight size={14} strokeWidth={2} className={styles.itemArrow} aria-hidden />
                    </button>
                  );
                })}
              </div>
            ))
          )}
        </div>

        <div className={styles.footer}>
          <span className={styles.footHint}>
            <kbd className={styles.footKey}>↑</kbd>
            <kbd className={styles.footKey}>↓</kbd>
            to navigate
          </span>
          <span className={styles.footHint}>
            <kbd className={styles.footKey}>
              <CornerDownLeft size={11} strokeWidth={2.4} />
            </kbd>
            to select
          </span>
        </div>
      </div>
    </dialog>
  );
}
