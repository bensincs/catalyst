/** WCAG relative luminance and contrast, so the token ramp can be checked in
 *  tests rather than only by eye. The muted role shipped at 4.03:1 for months —
 *  under the 4.5:1 minimum, on nearly every description and label in the
 *  product — because nothing asserted it. */

/** Relative luminance of an sRGB channel triple (0-255). */
export function luminance([r, g, b]: [number, number, number]): number {
  const f = (v: number) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
}

/** Contrast ratio between two opaque colors, 1..21. */
export function contrast(
  fg: [number, number, number],
  bg: [number, number, number],
): number {
  const a = luminance(fg);
  const b = luminance(bg);
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

/** Composite a translucent ink over an opaque background — how every
 *  `rgb(... / a)` text token actually renders. */
export function over(
  ink: [number, number, number],
  alpha: number,
  bg: [number, number, number],
): [number, number, number] {
  return [
    ink[0] * alpha + bg[0] * (1 - alpha),
    ink[1] * alpha + bg[1] * (1 - alpha),
    ink[2] * alpha + bg[2] * (1 - alpha),
  ];
}
