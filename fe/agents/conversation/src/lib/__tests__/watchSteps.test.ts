import { describe, test, expect } from "vitest";
import { parseWatchSteps, WATCH_CODE_STEP_EXAMPLE, WATCH_STEPS_EXAMPLE } from "../watchSteps.js";

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
  test("code steps: exactly one of code / script_path", () => {
    expect(parseWatchSteps(`[${WATCH_CODE_STEP_EXAMPLE}]`)).toMatchObject({ steps: [{ kind: "code" }] });
    expect(parseWatchSteps('[{"kind":"code","script_path":"watches/new_rows.js","lang":"JS"}]')).toMatchObject({ steps: [{ kind: "code" }] });
    expect(parseWatchSteps('[{"kind":"code"}]')).toMatchObject({ error: expect.stringContaining("steps[0]: kind=code takes exactly one of") });
    expect(parseWatchSteps('[{"kind":"code","code":"return {}","script_path":"a.ts"}]')).toMatchObject({ error: expect.stringContaining("exactly one of") });
    expect(parseWatchSteps('[{"kind":"code","code":"  "}]')).toMatchObject({ error: expect.stringContaining("exactly one of") });
  });
  test("code steps: path stays in the project, lang ts or js", () => {
    expect(parseWatchSteps('[{"kind":"code","script_path":"/etc/x.ts"}]')).toMatchObject({ error: expect.stringContaining("steps[0].script_path: must be relative") });
    expect(parseWatchSteps('[{"kind":"code","script_path":"~/x.ts"}]')).toMatchObject({ error: expect.stringContaining("must be relative") });
    expect(parseWatchSteps('[{"kind":"code","script_path":"a/../../x.ts"}]')).toMatchObject({ error: expect.stringContaining("no ..") });
    expect(parseWatchSteps('[{"kind":"bash"},{"kind":"code","code":"1","lang":"py"}]')).toMatchObject({ error: expect.stringContaining("steps[1].lang") });
  });
});
