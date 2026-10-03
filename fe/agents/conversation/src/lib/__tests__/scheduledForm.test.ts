import { describe, test, expect } from "vitest";
import { everyLabel, cronLabel, whenLabel, statusOf, isLive, draftOf, draftError, bodyOf, kindOf, emptyDraft } from "../scheduledForm.js";
import { clampIdleHours } from "../sessionPolicy.js";
import type { AgentSchedule } from "../api/team.js";

const row = (o: Partial<AgentSchedule>): AgentSchedule => ({
  id: "s1", title: "t", message: "m", kind: "recurring", status: "active", run_count: 0, destination: "main", session_id: "x", ...o,
});

describe("scheduledForm labels", () => {
  test("everyLabel picks the largest whole unit", () => {
    expect(everyLabel(60_000)).toBe("Every minute");
    expect(everyLabel(3_600_000)).toBe("Every hour");
    expect(everyLabel(5_400_000)).toBe("Every 90 minutes");
    expect(everyLabel(2 * 86_400_000)).toBe("Every 2 days");
  });

  test("cronLabel reads common shapes, falls back to the expression", () => {
    expect(cronLabel("0 9 * * *", "WIB")).toBe("Every day 09:00 (WIB)");
    expect(cronLabel("30 8 * * 1-5")).toBe("Weekdays 08:30");
    expect(cronLabel("0 7 * * 1")).toBe("Every Monday 07:00");
    expect(cronLabel("15 * * * *")).toBe("Every hour at :15");
    expect(cronLabel("*/5 * * * *")).toBe("Cron */5 * * * *");
  });

  test("whenLabel: cron, interval, once", () => {
    expect(whenLabel(row({ cron: "0 9 * * *" }))).toBe("Every day 09:00");
    expect(whenLabel(row({ interval_ms: 3_600_000 }))).toBe("Every hour");
    expect(whenLabel(row({ kind: "once" }))).toBe("Once");
  });

  test("statusOf: held by agent beats paused beats status", () => {
    expect(statusOf(row({ held_by_agent: true, paused: true })).label).toBe("Held — agent off");
    expect(statusOf(row({ paused: true })).label).toBe("Paused");
    expect(statusOf(row({ status: "pending" })).tone).toBe("ok");
    expect(statusOf(row({ status: "failed" })).tone).toBe("error");
    expect(isLive(row({ status: "done" }))).toBe(false);
  });
});

describe("scheduledForm drafts", () => {
  test("draftOf maps intervals and cron back to the form", () => {
    expect(draftOf(row({ interval_ms: 2 * 86_400_000 }))).toMatchObject({ mode: "every", every: 2, unit: "d" });
    expect(draftOf(row({ interval_ms: 45 * 60_000 }))).toMatchObject({ mode: "every", every: 45, unit: "m" });
    expect(draftOf(row({ cron: "0 9 * * 1-5" }))).toMatchObject({ mode: "cron", cron: "0 9 * * 1-5" });
    expect(draftOf(row({ kind: "once", next_run_at: "2026-10-04T02:00:00Z" })).mode).toBe("once");
  });

  test("draftError guards each mode", () => {
    const d = { ...emptyDraft(), message: "hi" };
    expect(draftError({ ...d, message: " " })).toMatch(/Write/);
    expect(draftError({ ...d, mode: "every", every: 0 })).toMatch(/whole number/);
    expect(draftError({ ...d, mode: "cron", cron: "0 9 *" })).toMatch(/5 fields/);
    expect(draftError(d)).toBe("");
  });

  test("bodyOf sends exactly one of run_at / every / cron", () => {
    const d = { ...emptyDraft(), message: " go " };
    expect(bodyOf({ ...d, mode: "every", every: 3, unit: "h" })).toEqual({ message: "go", every: "3h" });
    expect(bodyOf({ ...d, mode: "cron", cron: " 0 9 * * * " })).toEqual({ message: "go", cron: "0 9 * * *" });
    const once = bodyOf({ ...d, mode: "once", at: "2026-10-04T09:00" });
    expect(Object.keys(once).sort()).toEqual(["message", "run_at"]);
    expect(kindOf("once")).toBe("once");
    expect(kindOf("cron")).toBe("recurring");
  });
});

describe("clampIdleHours", () => {
  test("clamps to 1–168, empty is the default", () => {
    expect(clampIdleHours(0)).toBe(8);
    expect(clampIdleHours("")).toBe(8);
    expect(clampIdleHours(-3)).toBe(1);
    expect(clampIdleHours(500)).toBe(168);
    expect(clampIdleHours(12.4)).toBe(12);
  });
});
