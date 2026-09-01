import { contrast, over } from "./contrast";

// The light theme's ink and the three surfaces text sits on.
const INK: [number, number, number] = [41, 41, 41];
const SURFACES: Record<string, [number, number, number]> = {
  surface: [255, 255, 255],
  canvas: [246, 246, 247],
  sunken: [237, 237, 237],
};

// Alphas taken from web/app/globals.css. If a token changes there and not here,
// the test is the thing that noticed.
const TEXT_TOKENS: Record<string, number> = {
  text: 1,
  "text-secondary": 0.74,
  "text-muted": 0.68,
};

describe("light theme text tokens meet WCAG AA", () => {
  for (const [token, alpha] of Object.entries(TEXT_TOKENS)) {
    for (const [name, bg] of Object.entries(SURFACES)) {
      it(`${token} on ${name} is at least 4.5:1`, () => {
        const ratio = contrast(over(INK, alpha, bg), bg);
        expect(ratio).toBeGreaterThanOrEqual(4.5);
      });
    }
  }
});

describe("contrast maths", () => {
  it("is 21:1 for black on white", () => {
    expect(contrast([0, 0, 0], [255, 255, 255])).toBeCloseTo(21, 1);
  });
  it("is 1:1 for a color against itself", () => {
    expect(contrast([41, 41, 41], [41, 41, 41])).toBeCloseTo(1, 5);
  });
  it("is symmetric", () => {
    expect(contrast([41, 41, 41], [255, 255, 255])).toBeCloseTo(
      contrast([255, 255, 255], [41, 41, 41]),
      5,
    );
  });
  it("catches the value that shipped: muted at 0.6 fails on every surface", () => {
    for (const bg of Object.values(SURFACES)) {
      expect(contrast(over(INK, 0.6, bg), bg)).toBeLessThan(4.5);
    }
  });
});
