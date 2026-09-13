import { describe, it, expect } from "vitest";
import { normalizeComposerUsage } from "../usage.js";

describe("normalizeComposerUsage", () => {
  it("maps a supported provider's windows and cache provenance", () => {
    const got = normalizeComposerUsage({
      provider: "claude/enginer",
      supported: true,
      account: { connected: true, email: "dev@abc.com", plan: "Claude team" },
      windows: [{ key: "five_hour", utilization: 23, resets_at: "2030-01-01T00:00:00Z" }],
      fetched_at: "2026-09-13T00:00:00Z",
      age_s: 5,
      next_s: 55,
      can_manage: true,
    });

    expect(got.supported).toBe(true);
    expect(got.account?.email).toBe("dev@abc.com");
    expect(got.windows).toHaveLength(1);
    expect(got.windows[0].resetsAt).toBe("2030-01-01T00:00:00Z");
    // The reading is cached and shared, so its age rides along.
    expect(got.ageS).toBe(5);
    expect(got.nextS).toBe(55);
  });

  // The case this command had to get right: a provider type with no usage
  // API is not an error and must not render as 0% used.
  it("carries an unsupported provider's reason through", () => {
    const got = normalizeComposerUsage({
      provider: "codex/codex",
      supported: false,
      reason: "codex does not report usage limits",
    });
    expect(got.supported).toBe(false);
    expect(got.reason).toBe("codex does not report usage limits");
    expect(got.windows).toEqual([]);
    expect(got.error).toBe("");
  });

  it("defaults every field when the payload is empty", () => {
    const got = normalizeComposerUsage({});
    expect(got.supported).toBe(false);
    expect(got.account).toBeNull();
    expect(got.windows).toEqual([]);
    expect(got.canManage).toBe(false);
  });
});
