import { describe, test, expect } from "vitest";
import { activeDayLabel } from "../timeFormat.js";

describe("activeDayLabel", () => {
  test("empty separator list returns empty string", () => {
    expect(activeDayLabel([], 0, 40)).toBe("");
  });

  test("single separator at or above the threshold is active", () => {
    expect(activeDayLabel([{ top: 5, label: "Today" }], 0, 40)).toBe("Today");
  });

  test("picks the last separator at or above the threshold", () => {
    const seps = [
      { top: -100, label: "Monday" },
      { top: 300, label: "Today" },
    ];
    expect(activeDayLabel(seps, 0, 40)).toBe("Monday");
  });

  test("when several are above the threshold, the lowest one wins", () => {
    const seps = [
      { top: -200, label: "Monday" },
      { top: -50, label: "Today" },
    ];
    expect(activeDayLabel(seps, 0, 40)).toBe("Today");
  });

  test("falls back to the first separator when none reached the threshold", () => {
    const seps = [
      { top: 100, label: "Monday" },
      { top: 400, label: "Today" },
    ];
    expect(activeDayLabel(seps, 0, 40)).toBe("Monday");
  });
});
