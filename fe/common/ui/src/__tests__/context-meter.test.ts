import { describe, test, expect } from "vitest";
import {
  budgetText,
  contextBarClass,
  contextMeter,
  contextTextClass,
  contextTone,
  tokenBudgetText,
} from "../context-meter.js";

describe("contextMeter", () => {
  test("a known window reads as a ring with a percentage", () => {
    const m = contextMeter({ used: 163558, window: 1_000_000, pct: 16.3558 });
    expect(m.kind).toBe("ring");
    expect(m.pct).toBeCloseTo(16.3558);
    expect(m.text).toBe("164k / 1.00M · 16%");
    expect(m.note).toBe("");
  });

  /* The whole reason this module exists. The server sends window 0 when
     the CLI never reported a limit; a percentage there would be invented,
     and the one place it would be read is the place people trust. */
  test("no window size reads as tokens, never as a percentage", () => {
    const m = contextMeter({ used: 197787, window: 0 });
    expect(m.kind).toBe("tokens");
    expect(m.pct).toBe(0);
    expect(m.text).toBe("198k in context");
    expect(m.text).not.toMatch(/%/);
    expect(m.note).toMatch(/window size/i);
  });

  /* 0% reads as "the window is empty". Nothing-read-yet is a different
     fact and has to look different. */
  test("nothing read yet is its own state, not 0%", () => {
    for (const reading of [null, undefined, {}, { used: 0, window: 1_000_000 }]) {
      const m = contextMeter(reading);
      expect(m.kind).toBe("none");
      expect(m.text).toBe("no reading yet");
      expect(m.text).not.toMatch(/%/);
    }
  });

  test("the server's percentage wins over a recomputed one", () => {
    // A compaction can leave the stored pct behind the raw ratio; the
    // server stays the source of truth for the figure.
    const m = contextMeter({ used: 500, window: 1000, pct: 12 });
    expect(Math.round(m.pct)).toBe(12);
  });

  test("a percentage is computed when the server sent none", () => {
    const m = contextMeter({ used: 500, window: 1000 });
    expect(Math.round(m.pct)).toBe(50);
  });

  test("an over-full reading is clamped rather than printed past 100%", () => {
    const m = contextMeter({ used: 2000, window: 1000, pct: 200 });
    expect(m.pct).toBe(100);
    expect(m.tone).toBe("red");
  });

  test("negative or non-finite figures degrade to nothing read", () => {
    expect(contextMeter({ used: -5, window: 1000 }).kind).toBe("none");
    expect(contextMeter({ used: 500, window: -1 }).kind).toBe("tokens");
  });
});

describe("contextTone", () => {
  test("bands at 75 and 90", () => {
    expect(contextTone(0)).toBe("green");
    expect(contextTone(74.9)).toBe("green");
    expect(contextTone(75)).toBe("amber");
    expect(contextTone(89.9)).toBe("amber");
    expect(contextTone(90)).toBe("red");
    expect(contextTone(100)).toBe("red");
  });

  /* Status intent uses the pos/cau/neg ramps, never the green accent —
     the accent is brand, these are a warning channel. */
  test("classes come from the status ramps and carry a dark counterpart", () => {
    expect(contextBarClass("green")).toBe("bg-pos-400");
    expect(contextBarClass("amber")).toBe("bg-cau-400");
    expect(contextBarClass("red")).toBe("bg-neg-400");
    expect(contextTextClass("green")).toContain("dark:");
  });
});

describe("budgetText", () => {
  test("a cap is shown as a ratio", () => {
    expect(budgetText(4, 40, "turns")).toBe("4/40 turns");
  });

  /* A cap of 0 means uncapped by this delegation, not "zero allowed" —
     "4/0 turns" would read as an impossible state. */
  test("no cap is shown as a bare count", () => {
    expect(budgetText(4, 0, "turns")).toBe("4 turns");
  });
});

describe("tokenBudgetText", () => {
  test("a cap is shown as a compacted ratio", () => {
    expect(tokenBudgetText(5_248_411, 10_000_000)).toBe("5.25M / 10.0M tokens");
  });

  test("no cap is shown as a bare count", () => {
    expect(tokenBudgetText(5_248_411, 0)).toBe("5.25M tokens");
  });

  /* 0 means the provider never reported usage. Printing "0 tokens" would
     claim the run was free. */
  test("nothing reported says so instead of printing a zero", () => {
    expect(tokenBudgetText(0, 0)).toBe("not reported");
  });
});
