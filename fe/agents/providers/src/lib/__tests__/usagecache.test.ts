import { describe, it, expect } from "vitest";
import { cacheHint, fmtSecsShort } from "$lib/usagerings.js";

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
    expect(h.full).toContain("next refresh in 18s");
  });

  it("reads as 'just now' for a fresh reading", () => {
    expect(cacheHint(0, 60).short).toBe("just now");
    expect(cacheHint(1, 60).short).toBe("just now");
  });

  it("says the refresh is due when no countdown was sent", () => {
    expect(cacheHint(120, 0).full).toContain("next refresh due");
  });

  // An absent timestamp must collapse the chip entirely rather than
  // rendering "0s ago" for a reading that was never taken.
  it("is empty when the server sent no age", () => {
    expect(cacheHint(Number.NaN, 0)).toEqual({ short: "", full: "" });
    expect(cacheHint(-1, 0)).toEqual({ short: "", full: "" });
  });
});
