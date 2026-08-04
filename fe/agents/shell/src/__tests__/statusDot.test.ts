import { describe, test, expect } from "vitest";
import { statusDotClass, effectiveStatus, applyEvent, type DotState } from "../statusDot.js";

/* These mirror internal/tools/agents/view/statusdot_test.go. The server
   paints the first frame and this code repaints it from live events, so a
   drift between them shows up as a dot that changes the moment an
   unrelated event arrives. */

describe("effectiveStatus", () => {
  const cases: [string, DotState, string][] = [
    ["leader working wins over child", { lifecycle: "working", subAgent: "working" }, "working"],
    ["leader spawning wins over child", { lifecycle: "spawning", subAgent: "working" }, "spawning"],
    ["idle leader with working child", { lifecycle: "idle", subAgent: "working" }, "subagent"],
    ["idle leader with spawning child", { lifecycle: "idle", subAgent: "spawning" }, "subagent"],
    ["killed leader with working child", { lifecycle: "killed", subAgent: "working" }, "subagent"],
    ["no leader process, working child", { lifecycle: "", subAgent: "working" }, "subagent"],
    ["idle leader, no child work", { lifecycle: "idle", subAgent: "" }, "idle"],
    ["killed leader alone stays blank", { lifecycle: "killed", subAgent: "" }, ""],
    ["nothing at all", { lifecycle: "", subAgent: "" }, ""],
  ];

  for (const [name, state, want] of cases) {
    test(name, () => {
      expect(effectiveStatus(state)).toBe(want);
    });
  }
});

describe("statusDotClass", () => {
  test("the sub-agent dot spins but is not the leader's green", () => {
    const sub = statusDotClass("subagent");
    expect(sub).toContain("animate-spin");
    expect(sub).not.toContain("border-green-500");
    expect(sub).not.toBe(statusDotClass("working"));
    // Same geometry as the other spinners, so rows do not jump on a change.
    expect(sub).toContain("h-3 w-3");
  });

  test("an unknown status stays hidden and still", () => {
    expect(statusDotClass("")).toContain("hidden");
    expect(statusDotClass("")).not.toContain("animate-spin");
  });
});

describe("applyEvent", () => {
  // The bug this guards: the sidebar used to hold one string per row, so a
  // leader's "idle" event overwrote everything — erasing a running
  // sub-agent's marker at the exact moment it became the only thing worth
  // showing.
  test("a leader transition leaves the sub-agent marker intact", () => {
    const prev: DotState = { lifecycle: "working", subAgent: "working" };
    const next = applyEvent(prev, { lifecycle: "idle" });
    expect(next).toEqual({ lifecycle: "idle", subAgent: "working" });
    expect(effectiveStatus(next)).toBe("subagent");
  });

  test("a sub-agent transition leaves the leader's lifecycle intact", () => {
    const prev: DotState = { lifecycle: "idle", subAgent: "" };
    const next = applyEvent(prev, { sub_agent: "working" });
    expect(next).toEqual({ lifecycle: "idle", subAgent: "working" });
  });

  test("a child that stopped clears only the sub-agent half", () => {
    const prev: DotState = { lifecycle: "idle", subAgent: "working" };
    const next = applyEvent(prev, { sub_agent: "" });
    expect(next).toEqual({ lifecycle: "idle", subAgent: "" });
    expect(effectiveStatus(next)).toBe("idle");
  });

  test("an event with neither key clears the sub-agent half", () => {
    // The server omits sub_agent entirely when it is empty (omitempty), so
    // a child that stopped arrives as a bare {type:"lifecycle"}.
    const prev: DotState = { lifecycle: "idle", subAgent: "working" };
    expect(applyEvent(prev, {})).toEqual({ lifecycle: "idle", subAgent: "" });
  });

  test("builds from nothing on a first event", () => {
    expect(applyEvent(undefined, { lifecycle: "working" })).toEqual({
      lifecycle: "working",
      subAgent: "",
    });
  });
});
