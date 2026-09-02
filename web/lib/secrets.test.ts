import { contrast, over } from "./contrast";
import { outstandingKeys, secretSetName, type SecretSet } from "./types";

const base = (over_: Partial<SecretSet> = {}): SecretSet => ({
  id: "db-creds",
  name: "Database credentials",
  description: "",
  owner: "",
  keys: ["username", "password"],
  createdAt: "",
  platform: true,
  owned: false,
  entitled: true,
  ...over_,
});

describe("outstandingKeys", () => {
  it("reports the keys with no value yet", () => {
    expect(outstandingKeys(base({ keysSet: ["username"] }))).toEqual([
      "password",
    ]);
  });

  it("treats an absent keysSet as nothing supplied", () => {
    // A set the tenant has not enabled has no keysSet at all. Reading that as
    // "everything is filled in" would show a deployment as ready to go when it
    // is missing every credential it needs.
    expect(outstandingKeys(base())).toEqual(["username", "password"]);
  });

  it("is empty once every key has a value", () => {
    expect(
      outstandingKeys(base({ keysSet: ["username", "password"] })),
    ).toEqual([]);
  });

  it("ignores a stored key the author has since removed", () => {
    // Editing a set can drop a key while a tenant's value for it survives in the
    // vault. The dropped key is no longer declared, so it must not count either
    // way — it is neither outstanding nor a reason to call the set complete.
    expect(
      outstandingKeys(
        base({ keys: ["username"], keysSet: ["username", "legacy"] }),
      ),
    ).toEqual([]);
  });
});

describe("secretSetName", () => {
  it("is the documented, fixed name a chart author writes by hand", () => {
    expect(secretSetName("db-creds")).toBe("cortex-secret-db-creds");
  });
});

// A status badge renders its ink on its own tint (StatusBadge's soft variant),
// and those tints sit on all three surfaces. That combination was never checked:
// warning failed AA on every surface, info failed on sunken, and neutral failed
// on two — so every "Blocked" badge in the console was below the bar the design
// system claims to hold. Checking the whole vocabulary rather than the one pair
// this feature happens to use is the point.
describe("status ink on its own tint meets WCAG AA", () => {
  const SURFACES: Record<string, [number, number, number]> = {
    surface: [255, 255, 255],
    canvas: [246, 246, 247],
    sunken: [237, 237, 237],
  };
  // Values mirror web/app/globals.css. If a token changes there and not here,
  // this test is the thing that notices.
  const PAIRS: Record<
    string,
    {
      ink: [number, number, number];
      tint: [number, number, number];
      alpha: number;
    }
  > = {
    success: { ink: [18, 110, 64], tint: [18, 110, 64], alpha: 0.1 },
    info: { ink: [120, 54, 233], tint: [169, 100, 247], alpha: 0.12 },
    warning: { ink: [167, 70, 0], tint: [217, 119, 6], alpha: 0.12 },
    danger: { ink: [181, 35, 27], tint: [181, 35, 27], alpha: 0.1 },
  };

  for (const [name, p] of Object.entries(PAIRS)) {
    for (const [sn, bg] of Object.entries(SURFACES)) {
      it(`${name}-ink on ${name}-bg over ${sn}`, () => {
        expect(
          contrast(p.ink, over(p.tint, p.alpha, bg)),
        ).toBeGreaterThanOrEqual(4.5);
      });
    }
  }

  // neutral is an alpha of the ink rather than a hex, on an alpha tint.
  for (const [sn, bg] of Object.entries(SURFACES)) {
    it(`neutral-ink on neutral-bg over ${sn}`, () => {
      const ink = over([41, 41, 41], 0.72, bg);
      expect(
        contrast(ink, over([41, 41, 41], 0.08, bg)),
      ).toBeGreaterThanOrEqual(4.5);
    });
  }
});
