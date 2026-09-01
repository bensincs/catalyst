import { formatCount, formatInt, formatRelative } from "./format";

// Deterministic formatting, rendered on the server and re-rendered on the
// client — a mismatch is a hydration error, so these must not depend on the
// machine's clock or locale.

describe("formatRelative", () => {
  const now = Date.parse("2026-09-01T12:00:00Z");
  it("renders an em dash for a missing timestamp", () => {
    // A tenant that has never checked in has no time to show, and "0s ago"
    // would claim it just did.
    expect(formatRelative(null, now)).toBe("—");
    expect(formatRelative(0, now)).toBe("—");
  });
  it("steps through the units", () => {
    expect(formatRelative(now - 5_000, now)).toBe("5s ago");
    expect(formatRelative(now - 90_000, now)).toBe("2m ago");
    expect(formatRelative(now - 3 * 3_600_000, now)).toBe("3h ago");
    expect(formatRelative(now - 50 * 3_600_000, now)).toBe("2d ago");
  });
  it("clamps a future timestamp to zero rather than going negative", () => {
    // Clock skew between the reconciler and the control plane is normal.
    expect(formatRelative(now + 30_000, now)).toBe("0s ago");
  });
});

describe("formatCount", () => {
  it("keeps small numbers exact", () => {
    expect(formatCount(0)).toBe("0");
    expect(formatCount(999)).toBe("999");
  });
  it("abbreviates thousands and millions", () => {
    expect(formatCount(1000)).toBe("1.0K");
    expect(formatCount(12_400)).toBe("12K");
    expect(formatCount(1_284_000)).toBe("1.28M");
  });
  it("never renders a bare zero as an abbreviation", () => {
    expect(formatCount(0)).not.toContain("K");
  });
});

describe("formatInt", () => {
  it("groups thousands in a fixed locale, not the machine's", () => {
    // Server and client must agree, so the locale is pinned.
    expect(formatInt(1284000)).toBe("1,284,000");
    expect(formatInt(0)).toBe("0");
  });
});
