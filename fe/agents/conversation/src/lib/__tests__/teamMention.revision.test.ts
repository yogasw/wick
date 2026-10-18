import { describe, expect, it } from "vitest";
import { teamFrame } from "../teamMention.js";

describe("teamFrame - revision", () => {
  it("reads a revision handed into a running turn", () => {
    const f = teamFrame("subagent", "Revision 2 of task 01ab-cd from Captain (@captain):\n\nalso count the 500s\n\n(Same task: your final answer is still its one reply.)");
    expect(f).toMatchObject({ kind: "revision", handle: "captain", name: "Captain", taskId: "01ab-cd", revision: 2, replace: false, body: "also count the 500s" });
  });
  it("marks a replacing revision", () => {
    const f = teamFrame("subagent", "Revision 1 of task t1 from Yelan (@yelan), replacing the brief: drop what it asked and do this instead:\n\ncheck vera");
    expect(f).toMatchObject({ kind: "revision", replace: true, body: "check vera" });
  });
  it("is not trusted from a person", () => {
    expect(teamFrame("ui", "Revision 1 of task t1 from Yelan (@yelan):\n\nx")).toBeNull();
  });
});
