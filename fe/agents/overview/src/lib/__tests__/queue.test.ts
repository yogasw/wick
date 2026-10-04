import { describe, test, expect } from "vitest";
import { explainQueue, waitText } from "../queue.js";

describe("waitText", () => {
  test("human units", () => {
    expect(waitText(45_000)).toBe("45s");
    expect(waitText(179_000)).toBe("2m");
    expect(waitText(180_000)).toBe("3m");
    expect(waitText(4_800_000)).toBe("1h 20m");
  });
});

describe("explainQueue", () => {
  const now = new Date(2026, 9, 4, 14, 13, 0).getTime();
  test("Resource Guard hold names the cause, the 24h start time and the safe level", () => {
    const e = explainQueue(
      { kind: "guard_hold", detail: "host near a hang (CPU 100% busy with pressure 40% for 30s); stopping agent work until CPU and memory are under 80%", since: new Date(2026, 9, 4, 13, 46, 31).toISOString(), safe_pct: 80 },
      1, 3, now,
    );
    expect(e.title).toBe("Paused by Resource Guard");
    expect(e.detail).toContain("CPU 100%");
    expect(e.detail).not.toContain("stopping agent work");
    expect(e.next).toContain("under 80%");
    expect(e.since).toMatch(/^since 13[:.]46 \(26m ago\)$/);
  });
  test("full pool shows busy/max", () => {
    expect(explainQueue({ kind: "slots_full" }, 3, 3, now).title).toBe("Waiting for a free slot (3/3 busy)");
  });
  test("no reason from the server still explains", () => {
    expect(explainQueue(undefined, 1, 3, now).title).toBe("Waiting to start");
  });
});
