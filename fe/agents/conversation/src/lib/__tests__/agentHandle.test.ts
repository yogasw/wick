import { describe, it, expect } from "vitest";
import { slugHandle, uniqueHandle, HANDLE_RE } from "../agentForm.js";

describe("slugHandle", () => {
  it("lowercases and joins words with -", () => {
    expect(slugHandle("Log Hunter")).toBe("log-hunter");
    expect(slugHandle("  Ops / Support!! ")).toBe("ops-support");
  });
  it("drops accents and trailing dashes after the cut", () => {
    expect(slugHandle("Café Ops")).toBe("cafe-ops");
    const long = slugHandle("a".repeat(30) + " bcd");
    expect(long.length).toBeLessThanOrEqual(31);
    expect(long.endsWith("-")).toBe(false);
  });
});

describe("uniqueHandle", () => {
  it("keeps a free handle", () => {
    expect(uniqueHandle("ops", ["captain"])).toBe("ops");
  });
  it("keeps an empty suggestion empty", () => {
    expect(uniqueHandle("", [""])).toBe("");
  });
  it("adds -2, then -3 when those are taken too", () => {
    expect(uniqueHandle("ops", ["ops"])).toBe("ops-2");
    expect(uniqueHandle("ops", ["ops", "ops-2"])).toBe("ops-3");
  });
  it("does not count on from a name that already ends in a number", () => {
    expect(uniqueHandle("ops-2", ["ops-2"])).toBe("ops-2-2");
  });
  it("cuts the root so the suffix fits the server limit", () => {
    const h = "a".repeat(31);
    const next = uniqueHandle(h, [h]);
    expect(next).toBe("a".repeat(29) + "-2");
    expect(HANDLE_RE.test(next)).toBe(true);
  });
  it("never leaves a double dash where the root was cut", () => {
    const h = "a".repeat(28) + "-bc";
    expect(uniqueHandle(h, [h])).toBe("a".repeat(28) + "-2");
  });
});
