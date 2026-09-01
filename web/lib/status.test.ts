import { agentStatus, applicationStatus, infraStatus, storeStatus } from "./status";

// The single status vocabulary. Its whole point is that one resource resolves to
// exactly one word, and that the word means the same thing in the topology, the
// lists and the checklist. The dangerous failure is the opposite of a crash: a
// resource that is broken resolving to a calm tone, so nobody looks at it.

describe("nothing unknown is ever reported as healthy", () => {
  const unknowns = [undefined, "", "disabled", "absent", "nonsense", "LIVE"];
  it("infrastructure", () => {
    for (const s of unknowns) {
      expect(infraStatus(s as string).tone).not.toBe("success");
    }
  });
  it("applications, agents and stores", () => {
    for (const h of unknowns) {
      expect(applicationStatus({ health: h as string }).tone).not.toBe("success");
      expect(agentStatus({ health: h as string }).tone).not.toBe("success");
      expect(storeStatus({ health: h as string }).tone).not.toBe("success");
    }
  });
  it("is case-sensitive, so a mis-cased value degrades rather than passes", () => {
    expect(infraStatus("Ready").key).toBe("queued");
    expect(agentStatus({ health: "Live" }).key).toBe("queued");
  });
});

describe("failure is never softened", () => {
  it("blocked and failed are danger", () => {
    expect(infraStatus("failed").tone).toBe("danger");
    expect(agentStatus({ health: "blocked" }).tone).toBe("danger");
    expect(applicationStatus({ health: "blocked" }).tone).toBe("danger");
    expect(storeStatus({ health: "blocked" }).tone).toBe("danger");
  });
  it("drift is warning, not success", () => {
    expect(agentStatus({ health: "drift" }).tone).toBe("warning");
  });
});

describe("waiting on dependencies outranks health", () => {
  it("a waiting application reads as waiting even when health says live", () => {
    // Otherwise an app held for an unconverged dependency claims to be live.
    expect(applicationStatus({ health: "live", waiting: true }).key).toBe("waiting");
    expect(applicationStatus({ health: "live", waiting: true }).tone).toBe("warning");
  });
  it("and reads its own health when not waiting", () => {
    expect(applicationStatus({ health: "live", waiting: false }).key).toBe("live");
  });
});

describe("the happy path still resolves", () => {
  it("maps the states each kind actually reports", () => {
    expect(infraStatus("ready").key).toBe("live");
    expect(infraStatus("provisioning").key).toBe("deploying");
    expect(infraStatus("deprovisioning").key).toBe("deprovisioning");
    expect(agentStatus({ health: "reconciling" }).key).toBe("deploying");
    expect(agentStatus({ health: "live" }).key).toBe("live");
  });
  it("only ever pulses something in motion or live", () => {
    // A pulsing dot means "this is moving"; a failed thing must sit still.
    expect(infraStatus("failed").pulse).toBe(false);
    expect(agentStatus({ health: "drift" }).pulse).toBe(false);
    expect(infraStatus("provisioning").pulse).toBe(true);
  });
});
