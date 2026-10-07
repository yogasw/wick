import { describe, test, expect } from "vitest";
import { rosterTime } from "../timeFormat.js";

// Local-time constructors so the cases hold in any TZ the runner uses.
const NOW = new Date(2026, 9, 3, 14, 5).getTime();
const local = (y: number, mo: number, d: number, h = 0, mi = 0) => new Date(y, mo, d, h, mi).toISOString();

describe("rosterTime", () => {
  test("today is the clock, zero-padded", () => {
    expect(rosterTime(local(2026, 9, 3, 9, 7), NOW)).toBe("09:07");
    expect(rosterTime(local(2026, 9, 3, 0, 0), NOW)).toBe("00:00");
  });

  test("yesterday is a word, even just after midnight", () => {
    expect(rosterTime(local(2026, 9, 2, 23, 59), NOW)).toBe("Kemarin");
    expect(rosterTime(local(2026, 9, 2, 0, 1), new Date(2026, 9, 3, 0, 2).getTime())).toBe("Kemarin");
  });

  test("earlier this year is day + month, older is a full date", () => {
    expect(rosterTime(local(2026, 8, 28, 10), NOW)).toBe("28 Sep");
    expect(rosterTime(local(2026, 0, 1, 10), NOW)).toBe("1 Jan");
    expect(rosterTime(local(2025, 11, 31, 10), NOW)).toBe("31/12/2025");
  });

  // Clock skew can put the stamp slightly ahead of the browser.
  test("a future stamp still reads as today's clock", () => {
    expect(rosterTime(local(2026, 9, 3, 14, 6), NOW)).toBe("14:06");
  });

  test("missing or zero time renders nothing", () => {
    expect(rosterTime(null, NOW)).toBe("");
    expect(rosterTime("", NOW)).toBe("");
    expect(rosterTime("0001-01-01T00:00:00Z", NOW)).toBe("");
  });
});
