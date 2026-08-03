import { describe, test, expect } from "vitest";
import { shortDuration, timeAgo, exactTime } from "../timeFormat.js";

const NOW = Date.parse("2026-08-03T12:00:00Z");
const ago = (ms: number) => new Date(NOW - ms).toISOString();

describe("shortDuration", () => {
  test("rounds to a single coarse unit", () => {
    expect(shortDuration(5_000)).toBe("just now");
    expect(shortDuration(44_000)).toBe("just now");
    expect(shortDuration(90_000)).toBe("2m");
    expect(shortDuration(59 * 60_000)).toBe("59m");
    expect(shortDuration(3 * 3_600_000)).toBe("3h");
    expect(shortDuration(50 * 3_600_000)).toBe("2d");
  });

  // A clock skew between server and browser can put a timestamp in the
  // future; "-3m ago" is worse than saying nothing.
  test("a negative span renders nothing", () => {
    expect(shortDuration(-1000)).toBe("");
  });
});

describe("timeAgo", () => {
  test("appends 'ago' except to 'just now'", () => {
    expect(timeAgo(ago(10_000), NOW)).toBe("just now");
    expect(timeAgo(ago(4 * 60_000), NOW)).toBe("4m ago");
    expect(timeAgo(ago(26 * 3_600_000), NOW)).toBe("1d ago");
  });

  // Go's zero time.Time marshals as 0001-01-01, which Date parses fine —
  // rendering it as "739000d ago" would be worse than an empty label.
  test("absent, malformed and zero timestamps render nothing", () => {
    expect(timeAgo(undefined, NOW)).toBe("");
    expect(timeAgo("", NOW)).toBe("");
    expect(timeAgo("not a date", NOW)).toBe("");
    expect(timeAgo("0001-01-01T00:00:00Z", NOW)).toBe("");
  });
});

describe("exactTime", () => {
  test("renders a full local timestamp for the tooltip", () => {
    const got = exactTime("2026-08-03T12:00:00Z");
    expect(got).toContain("2026");
    // Local rendering, so both the hour (timezone) and the separator
    // (locale — id-ID uses "19.00.00", en-US "19:00:00") vary. Assert
    // that a full h/m/s is there at all, not how it is punctuated.
    expect(got).toMatch(/\d{2}[:.]\d{2}[:.]\d{2}/);
  });

  test("gives back nothing to hang a tooltip on when unparseable", () => {
    expect(exactTime(undefined)).toBe("");
    expect(exactTime("0001-01-01T00:00:00Z")).toBe("");
  });
});
