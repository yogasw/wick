import { describe, test, expect } from "vitest";
import { workingTeammates, waitingHeaderLabel, waitingTrayLine, stopDialog, teammatesHiddenNote, threadTyping } from "../teammatesBusy.js";
import { activityLabel } from "../activityLabel.js";
import type { TeamTaskItem } from "../types/agents.js";

const task = (to: string, state: string): TeamTaskItem => ({ task_id: to + state, to_handle: to, state } as unknown as TeamTaskItem);

describe("teammatesBusy", () => {
  test("counts working teammates per handle", () => {
    const got = workingTeammates([task("halodev", "working"), task("halodev", "working"), task("qa", "working"), task("docs", "completed"), task("ops", "input_required")]);
    expect(got).toEqual(["halodev", "qa"]);
  });
  test("header and tray wording", () => {
    expect(waitingHeaderLabel(3)).toBe("working · waiting on 3 teammates");
    expect(waitingHeaderLabel(1)).toBe("working · waiting on 1 teammate");
    expect(waitingHeaderLabel(0)).toBe("");
    expect(waitingTrayLine("Captain", 2)).toBe("Captain is waiting on 2 teammates");
  });
  test("Stop offers to stop teammates too", () => {
    expect(stopDialog(0, 0)).toBeNull();
    expect(stopDialog(0, 2)).toEqual({
      title: "Stop this agent and its teammates' tasks?",
      body: "2 teammates still working will have their tasks canceled. The chat history is kept.",
      confirm: "Stop all",
    });
    expect(stopDialog(1, 1)?.title).toBe("Stop this agent, its sub-agents and teammates' tasks?");
    expect(stopDialog(2, 0)?.title).toBe("Stop this agent and its sub-agents?");
  });
  test("hidden note names the channel", () => {
    expect(teammatesHiddenNote("Sent to Slack")).toBe("teammates hidden in Slack");
    expect(teammatesHiddenNote("Sent to Telegram")).toBe("teammates hidden in Telegram");
  });
});

describe("threadTyping", () => {
  test("the agent's own turn wins", () => {
    const t = { active: true, toolName: "Bash" };
    expect(threadTyping(t, 2)).toBe(t);
  });
  test("no turn and nobody working: idle", () => {
    expect(threadTyping({ active: false }, 0)).toEqual({ active: false });
  });
  test("no turn but teammates working: still working", () => {
    const t = threadTyping({ active: false }, 1);
    expect(t.active).toBe(true);
    expect(activityLabel(t.substate, t.toolName)).toBe("working…");
  });
});
