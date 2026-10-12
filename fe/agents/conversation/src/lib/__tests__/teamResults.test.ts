import { describe, test, expect } from "vitest";
import { foldTeamResults, isHandoffInBlock, teamResultOf } from "../teamResults.js";

const shown = new Set(["t1", "t2"]);
const u = (text: string, source = "subagent") => ({ role: "user", source, text });

describe("teamResults", () => {
  test("a batch prompt about a shown task hides; about an unshown one it stays", () => {
    expect(teamResultOf(u("[team results · group b · 2 of 2 replied]\nA — completed [task t1]:"), shown)).toEqual({ kind: "hide" });
    expect(teamResultOf(u("[team results · group b · 1 of 1 replied]\nA — completed [task zz]:"), shown)).toBeNull();
    // Ride-along follow-ups are not rows of any block: the prompt stays.
    expect(teamResultOf(u("[team results · group b · 2 of 2 replied]\nA — completed [task t1]:\n\nFollow-ups:\n<follow-up>\nps\n</follow-up>"), shown)).toBeNull();
  });

  test("an update names the teammates by their names", () => {
    const r = teamResultOf(u("[team results update · group b · Updated with @research's, @qa's reply · 2 of 3 replied]\nR — completed [task t2]:"), shown, { research: "Research" });
    expect(r).toEqual({ kind: "update", label: "Updated with Research's, @qa's reply" });
  });

  test("a single reply hides, a follow-up after first is an update", () => {
    expect(teamResultOf(u("Reply from Halodev (@halodev) [task t1, completed]:\n\nok"), shown)).toEqual({ kind: "hide" });
    expect(teamResultOf(u("Follow-up from Halodev [task t2, group b, after the first answer]:\n<follow-up>"), shown)).toEqual({ kind: "update", label: "Updated with Halodev's reply" });
  });

  test("only a subagent-source user turn counts", () => {
    expect(teamResultOf(u("[team results · group b · 1 of 1 replied] [task t1]", "ui"), shown)).toBeNull();
    expect(teamResultOf({ role: "assistant", source: "subagent", text: "[team results · [task t1]" }, shown)).toBeNull();
  });

  test("hand-off pills of shown tasks fold away", () => {
    expect(isHandoffInBlock({ role: "system", kind: "mention_handoff", extras: { task_id: "t1" } }, shown)).toBe(true);
    expect(isHandoffInBlock({ role: "system", kind: "mention_handoff", extras: { task_id: "x" } }, shown)).toBe(false);
    const turns = [
      { role: "system", kind: "mention_handoff", extras: { task_id: "t1" }, text: "" },
      u("[team results · group b · 1 of 1 replied] [task t1]"),
      u("[team results update · group b · Updated with @a's reply · 2 of 2 replied] [task t2]"),
      { role: "assistant", text: "answer" },
    ];
    const out = foldTeamResults(turns, shown, { a: "Alpha" });
    expect(out.map((t) => t.team_update ?? t.text)).toEqual(["Updated with Alpha's reply", "answer"]);
    expect(foldTeamResults(turns, new Set())).toBe(turns);
  });
});
