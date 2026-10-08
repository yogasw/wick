import { describe, test, expect } from "vitest";
import { ago, ruleText, scheduleType, stepSummary, type Schedule } from "../api.js";

describe("watch helpers", () => {
  test("ruleText reads like a sentence", () => {
    expect(ruleText({ path: "state.name", op: "in", value: ["COMPLETED"] })).toBe("state.name in [COMPLETED]");
    expect(ruleText({ path: "result", op: "exists" })).toBe("result exists");
    expect(ruleText({ path: "building", op: "equals", value: false })).toBe("building equals false");
  });

  test("stepSummary per kind", () => {
    expect(stepSummary({ kind: "connector", tool_id: "conn:bb/get_pipeline" })).toBe("conn:bb/get_pipeline");
    expect(stepSummary({ kind: "bash", script: "jq -e .ok\nexit 1" })).toBe("jq -e .ok");
    expect(
      stepSummary({
        kind: "check",
        match: "any",
        rules: [
          { path: "state.name", op: "in", value: ["COMPLETED"] },
          { path: "state.stage.name", op: "equals", value: "PAUSED" },
        ],
        fail_rules: [{ path: "state.result.name", op: "in", value: ["FAILED"] }],
      }),
    ).toBe("state.name in [COMPLETED] OR state.stage.name equals PAUSED · fail if state.result.name in [FAILED]");
  });

  test("scheduleType defaults legacy rows to message", () => {
    expect(scheduleType({ type: "watch" } as Schedule)).toBe("watch");
    expect(scheduleType({} as Schedule)).toBe("message");
  });

  test("ago", () => {
    const now = Date.parse("2026-10-08T10:00:00Z");
    expect(ago("2026-10-08T09:59:48Z", now)).toBe("12s ago");
    expect(ago("2026-10-08T09:56:00Z", now)).toBe("4m ago");
    expect(ago(undefined, now)).toBe("");
  });
});

describe("stopLabel", () => {
  test("names the step a run ended on", async () => {
    const { stopLabel } = await import("../api.js");
    expect(stopLabel({ index: 1, name: "done?", kind: "check" })).toBe("step 2 · check 'done?'");
    expect(stopLabel(undefined)).toBe("—");
    expect(stopLabel({ index: 1, name: "selesai?", kind: "check", decision: "fail→done" })).toBe("step 2 · check 'selesai?' · fail→done");
  });
});
