import { describe, test, expect } from "vitest";
import { idleCountdownText } from "../idleCountdown.js";

describe("idleCountdownText", () => {
  test("full timeout remaining → 'kill in 120s'", () => {
    expect(idleCountdownText(1000, 120000, 1000)).toBe("kill in 120s");
  });

  test("1 second remaining → 'kill in 1s'", () => {
    expect(idleCountdownText(1000, 120000, 1000 + 119000)).toBe("kill in 1s");
  });

  test("past deadline → '0s'", () => {
    expect(idleCountdownText(1000, 120000, 1000 + 120001)).toBe("0s");
  });

  test("exactly at deadline → '0s'", () => {
    expect(idleCountdownText(1000, 120000, 1000 + 120000)).toBe("0s");
  });

  test("fractional second remaining rounds up → 'kill in 42s'", () => {
    expect(idleCountdownText(0, 120000, 120000 - 41500)).toBe("kill in 42s");
  });

  test("60 seconds remaining → 'kill in 60s'", () => {
    const at = 0;
    const timeout = 120000;
    const now = 60000;
    expect(idleCountdownText(at, timeout, now)).toBe("kill in 60s");
  });

  test("atMs=0 idleTimeoutMs=0 nowMs=0 → '0s'", () => {
    expect(idleCountdownText(0, 0, 0)).toBe("0s");
  });
});
