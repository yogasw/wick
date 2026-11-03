import { describe, test, expect } from "vitest";
import { matchesScheduleType, SCHEDULE_TYPE_FILTERS, scheduleType, stepSummary } from "./watch-steps.js";

describe("schedule type filter", () => {
  test("rows without a type are messages", () => {
    expect(scheduleType({})).toBe("message");
    expect(scheduleType({ type: "watch" })).toBe("watch");
  });
  test("each chip keeps only its type", () => {
    const rows = [{ type: "watch" }, {}, { type: "message" }];
    expect(SCHEDULE_TYPE_FILTERS.map((f) => f.id)).toEqual(["all", "message", "watch"]);
    expect(rows.filter((r) => matchesScheduleType(r, "all"))).toHaveLength(3);
    expect(rows.filter((r) => matchesScheduleType(r, "message"))).toHaveLength(2);
    expect(rows.filter((r) => matchesScheduleType(r, "watch"))).toEqual([{ type: "watch" }]);
  });
});

describe("stepSummary", () => {
  test("code: script_path, else the first non-comment line", () => {
    expect(stepSummary({ kind: "code", lang: "js", script_path: "watches/a.js" })).toBe("js · watches/a.js");
    expect(stepSummary({ kind: "code", code: "/* header\n * more */\nreturn {changed: []};" })).toBe("ts · return {changed: []};");
  });
});
