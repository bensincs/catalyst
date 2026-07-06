export type ClassValue = string | false | null | undefined;

/** Minimal class joiner — no dependency needed for this surface. */
export function cn(...values: ClassValue[]): string {
  return values.filter(Boolean).join(" ");
}
