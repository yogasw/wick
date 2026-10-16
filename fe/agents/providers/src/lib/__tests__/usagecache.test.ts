import { describe, it, expect } from "vitest";
import { clock24 } from "@wick-fe/common-ui";
import { cacheHint, fmtSecsShort, staleNotice } from "$lib/usagerings.js";

describe("fmtSecsShort", () => {
  it("scales the unit with the magnitude", () => {
    expect(fmtSecsShort(0)).toBe("0s");
    expect(fmtSecsShort(42)).toBe("42s");
    expect(fmtSecsShort(90)).toBe("2m");
    expect(fmtSecsShort(3600)).toBe("1h");
    expect(fmtSecsShort(172800)).toBe("2d");
  });

  it("clamps junk rather than rendering NaN", () => {
    expect(fmtSecsShort(-5)).toBe("0s");
    expect(fmtSecsShort(Number.NaN)).toBe("0s");
  });
});

describe("cacheHint", () => {
  it("says how old the reading is and when the next probe runs", () => {
    const h = cacheHint(42, 18);
    expect(h.short).toBe("42s ago");
    expect(h.full).toContain("Cached reading");
    expect(h.full).toContain("42s ago");
    expect(h.full).toContain("re-check available in 18s");
  });

  it("reads as 'just now' for a fresh reading", () => {
    expect(cacheHint(0, 60).short).toBe("just now");
    expect(cacheHint(1, 60).short).toBe("just now");
  });

  it("points at Re-check when no cooldown is pending", () => {
    // Nothing refreshes on its own, so there is no "due" to report —
    // the only thing that produces a new reading is the button.
    expect(cacheHint(120, 0).full).toContain("press Re-check");
  });

  // An absent timestamp must collapse the chip entirely rather than
  // rendering "0s ago" for a reading that was never taken.
  it("is empty when the server sent no age", () => {
    expect(cacheHint(Number.NaN, 0)).toEqual({ short: "", full: "" });
    expect(cacheHint(-1, 0)).toEqual({ short: "", full: "" });
  });
});

describe("staleNotice", () => {
  const now = Date.parse("2026-09-12T10:00:00Z");

  it("is null when the latest check succeeded", () => {
    expect(staleNotice(172800, 600, "", false, now)).toBeNull();
    expect(staleNotice(172800, 600, "   ", true, now)).toBeNull();
  });

  it("names a rate limit and when a retry is accepted", () => {
    const raw = "usage endpoint: 429 Too Many Requests";
    const n = staleNotice(172800, 600, raw, true, now);
    const at = clock24(new Date(now + 600_000));
    expect(n?.text).toBe(`Cached reading from 2d ago — last check rate-limited (429), retry after ${at}.`);
    expect(n?.title).toBe(raw);
  });

  it("drops the retry clause when no wait is known", () => {
    expect(staleNotice(120, 0, "429", true, now)?.text).toBe("Cached reading from 2m ago — last check rate-limited (429).");
  });

  it("quotes a short form of any other failure", () => {
    const n = staleNotice(3600, 0, "usage endpoint: 401 Unauthorized", false, now);
    expect(n?.text).toBe("Cached reading from 1h ago — last check failed: 401 Unauthorized.");
    expect(n?.title).toBe("usage endpoint: 401 Unauthorized");
    const long = staleNotice(3600, 0, "x".repeat(200), false, now);
    expect(long?.text.length).toBeLessThan(140);
  });
});
