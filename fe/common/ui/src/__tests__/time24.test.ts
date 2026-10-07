import { describe, test, expect } from "vitest";
import { clock24 } from "../time24.js";

describe("clock24", () => {
  const afternoon = new Date(2026, 9, 4, 14, 13, 5);
  test("24-hour, no AM/PM", () => {
    expect(clock24(afternoon)).toMatch(/^14[:.]13$/);
    expect(clock24(afternoon, true)).toMatch(/^14[:.]13[:.]05$/);
    expect(clock24(afternoon)).not.toMatch(/[AP]M/i);
  });
  test("midnight is 00, not 24", () => {
    expect(clock24(new Date(2026, 9, 4, 0, 5))).toMatch(/^00[:.]05$/);
  });
  test("bad input renders nothing", () => {
    expect(clock24("not a date")).toBe("");
  });
});
