import { describe, it, expect } from "vitest";
import { humanBytes, humanBps, humanPct, humanDuration, pctOf, middleTruncate } from "../format.js";

// These render the numbers an operator reads a limit decision off, so the
// boundaries matter more than the happy path.
describe("humanBytes", () => {
  it("scales through the units", () => {
    expect(humanBytes(512)).toBe("512 B");
    expect(humanBytes(1024)).toBe("1.0 KB");
    expect(humanBytes(1536 * 1024)).toBe("1.5 MB");
    expect(humanBytes(2 * 1024 ** 3)).toBe("2.0 GB");
  });

  // A missing reading must read as zero, not "NaN B" in the middle of a table.
  it("handles zero and non-finite input", () => {
    expect(humanBytes(0)).toBe("0 B");
    expect(humanBytes(NaN)).toBe("0 B");
    expect(humanBytes(-5)).toBe("0 B");
  });
});

describe("humanBps / humanPct", () => {
  // An idle agent should show a dash, not "0 B/s" — absence of activity
  // reads better than a zero that looks like a measurement.
  it("renders idle values as a dash", () => {
    expect(humanBps(0)).toBe("—");
    expect(humanPct(0)).toBe("—");
  });

  it("renders active values with units", () => {
    expect(humanBps(2048)).toBe("2.0 KB/s");
    expect(humanPct(42.6)).toBe("43%");
  });
});

describe("humanDuration", () => {
  it("picks the largest readable unit", () => {
    expect(humanDuration(45)).toBe("45s");
    expect(humanDuration(720)).toBe("12m");
    expect(humanDuration(3600)).toBe("1h");
    expect(humanDuration(12000)).toBe("3h 20m");
  });

  it("handles zero", () => {
    expect(humanDuration(0)).toBe("0s");
  });
});

describe("pctOf", () => {
  it("computes a percentage", () => {
    expect(pctOf(50, 200)).toBe(25);
  });

  // A machine whose total memory could not be read must not produce
  // Infinity and blow out the bar width.
  it("returns 0 rather than dividing by zero", () => {
    expect(pctOf(50, 0)).toBe(0);
  });

  it("clamps above 100", () => {
    expect(pctOf(300, 200)).toBe(100);
  });
});

describe("middleTruncate", () => {
  // The reason this exists rather than CSS truncate: every Chrome helper
  // shares the same long path to the same binary, and what tells a
  // renderer from a GPU process is the --type= argument at the very end.
  // Clip the tail and every row reads identically.
  it("keeps both ends so the arguments stay visible", () => {
    const cmd =
      "/data/data/com.termux/files/home/.wick-agent/plugins/playwright_browser/chrome --type=gpu-process";
    const got = middleTruncate(cmd, 60);

    expect(got.length).toBeLessThanOrEqual(60);
    expect(got.startsWith("/data/data")).toBe(true);
    expect(got.endsWith("--type=gpu-process")).toBe(true);
    expect(got).toContain("…");
  });

  // Two commands identical except for their tail must render differently —
  // this is the whole point.
  it("distinguishes commands that differ only at the end", () => {
    const base = "/very/long/path/that/repeats/for/every/helper/process/chrome";
    const a = middleTruncate(`${base} --type=renderer`, 50);
    const b = middleTruncate(`${base} --type=gpu-process`, 50);

    expect(a).not.toBe(b);
  });

  it("leaves a short command untouched", () => {
    expect(middleTruncate("node server.js", 90)).toBe("node server.js");
  });

  // Guard the arithmetic: the result must never exceed the budget, at any
  // length near the boundary.
  it("never exceeds the limit", () => {
    for (const n of [40, 41, 60, 89, 90, 91, 200]) {
      const got = middleTruncate("x".repeat(n), 50);
      expect(got.length).toBeLessThanOrEqual(50);
    }
  });
});
