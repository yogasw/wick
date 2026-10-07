import { describe, test, expect } from "vitest";
import { ago, isDormant, channelClass } from "../format.js";

const NOW = new Date("2026-09-16T12:00:00Z");

describe("ago", () => {
  test("an account that never signed in says so, rather than showing 1970", () => {
    expect(ago(undefined, NOW)).toBe("never");
    expect(ago("", NOW)).toBe("never");
    expect(ago("not-a-date", NOW)).toBe("never");
  });

  test("scales the unit to the distance", () => {
    expect(ago("2026-09-16T11:59:30Z", NOW)).toBe("just now");
    expect(ago("2026-09-16T11:30:00Z", NOW)).toBe("30m ago");
    expect(ago("2026-09-16T06:00:00Z", NOW)).toBe("6h ago");
    expect(ago("2026-09-10T12:00:00Z", NOW)).toBe("6d ago");
    expect(ago("2026-06-16T12:00:00Z", NOW)).toBe("3mo ago");
    expect(ago("2024-09-16T12:00:00Z", NOW)).toBe("2y ago");
  });

  test("a clock that is slightly ahead does not read as the future", () => {
    expect(ago("2026-09-16T12:00:30Z", NOW)).toBe("just now");
  });
});

describe("isDormant", () => {
  test("no activity at all counts as dormant", () => {
    expect(isDormant(undefined, NOW)).toBe(true);
  });
  test("last month is dormant, last week is not", () => {
    expect(isDormant("2026-07-01T12:00:00Z", NOW)).toBe(true);
    expect(isDormant("2026-09-12T12:00:00Z", NOW)).toBe(false);
  });
});

describe("channelClass", () => {
  test("known channels are distinguishable", () => {
    expect(channelClass("slack")).not.toBe(channelClass("telegram"));
    expect(channelClass("ui")).not.toBe(channelClass("slack"));
  });
  test("an unknown channel still gets a readable chip", () => {
    expect(channelClass("whatsapp")).toContain("text-black-700");
  });
});
