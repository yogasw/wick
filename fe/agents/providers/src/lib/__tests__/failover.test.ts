import { describe, it, expect } from "vitest";
import { healthLabel, healthBadgeClass, chainProblems, crossTypeNote, pickValue, fromPickValue } from "../failover.js";

describe("failover helpers", () => {
  it("labels health in local time", () => {
    const now = new Date(2026, 9, 13, 9, 0);
    const today = new Date(2026, 9, 13, 14, 30).toISOString();
    const tomorrow = new Date(2026, 9, 14, 8, 5).toISOString();
    expect(healthLabel(null)).toBe("");
    expect(healthLabel({ Kind: "limit", Since: "", Until: today, Reason: "" }, now)).toBe("Limited until 14:30");
    expect(healthLabel({ Kind: "limit", Since: "", Until: tomorrow, Reason: "" }, now)).toMatch(/^Limited until .+ 08:05$/);
    expect(healthLabel({ Kind: "limit", Since: "", Until: "", Reason: "" }, now)).toBe("Limited");
    expect(healthLabel({ Kind: "auth", Since: "", Until: "", Reason: "" }, now)).toBe("Auth failed");
    expect(healthLabel({ Kind: "unavailable", Since: "", Until: "", Reason: "" }, now)).toBe("Unavailable");
    expect(healthLabel({ Kind: "auth", Since: "", Until: "", Reason: "", Retrying: true }, now)).toBe("Retrying auth");
  });

  it("colours badges amber when they heal alone, red otherwise", () => {
    expect(healthBadgeClass({ Kind: "limit", Since: "", Until: "", Reason: "" })).toContain("amber");
    expect(healthBadgeClass({ Kind: "auth", Since: "", Until: "", Reason: "", Retrying: true })).toContain("amber");
    expect(healthBadgeClass({ Kind: "auth", Since: "", Until: "", Reason: "" })).toContain("red");
    expect(healthBadgeClass({ Kind: "unavailable", Since: "", Until: "", Reason: "" })).toContain("red");
  });

  it("finds self and duplicate targets, bare types included", () => {
    expect(chainProblems("claude", "claude", [{ instance: "claude" }, { instance: "codex/a" }, { instance: "codex/a" }, { instance: "" }])).toEqual([
      expect.stringContaining("itself"),
      "",
      expect.stringContaining("already"),
      "",
    ]);
  });

  it("notes another provider type only", () => {
    expect(crossTypeNote("claude", "claude/b")).toBe("");
    expect(crossTypeNote("claude", "")).toBe("");
    expect(crossTypeNote("claude", "omp/wick")).toContain("does not carry over to omp");
  });

  it("round-trips the picker value", () => {
    expect(pickValue({ instance: "codex/a", model_id: "gpt@high" })).toBe("codex/a::gpt@high");
    expect(pickValue({ instance: "codex" })).toBe("codex/codex");
    expect(fromPickValue("codex/a::gpt@high")).toEqual({ instance: "codex/a", model_id: "gpt@high" });
    expect(fromPickValue("omp/wick")).toEqual({ instance: "omp/wick", model_id: "" });
  });
});
