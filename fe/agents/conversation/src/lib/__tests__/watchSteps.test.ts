import { describe, test, expect } from "vitest";
import { parseWatchSteps, WATCH_STEPS_EXAMPLE } from "../watchSteps.js";

describe("parseWatchSteps", () => {
  test("the pre-filled example parses", () => {
    const r = parseWatchSteps(WATCH_STEPS_EXAMPLE);
    expect("steps" in r && r.steps.length).toBe(2);
  });
  test("names the bad field", () => {
    expect(parseWatchSteps("{}")).toEqual({ error: "steps: must be a JSON array of steps" });
    expect(parseWatchSteps("[]")).toEqual({ error: "steps: a watch needs at least one step" });
    expect(parseWatchSteps('[{"kind":"go"}]')).toMatchObject({ error: expect.stringContaining("steps[0].kind") });
    expect(parseWatchSteps(JSON.stringify(Array(9).fill({ kind: "bash" })))).toMatchObject({ error: expect.stringContaining("max 8") });
  });
});
