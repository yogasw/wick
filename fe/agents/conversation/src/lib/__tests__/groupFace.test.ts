import { describe, test, expect } from "vitest";
import { groupFace } from "../teamGroups.js";
import type { AgentItem } from "../api/team.js";
import type { RosterStatus } from "../rosterStatus.js";

const a = { id: "cap", handle: "captain", name: "Captain", status: "working", current_action: "Bash" } as unknown as AgentItem;
const st = { unread: false, attention: true, work: "subagent", typing: "🤖 captain working…", presence: "working", label: "working" } as unknown as RosterStatus;

describe("groupFace", () => {
  test("work outside the group leaves the group's face still", () => {
    const f = groupFace(a, st, false);
    expect(f.agent.status).toBe("idle");
    expect(f.st.work).toBe("idle");
    expect(f.st.attention).toBe(false);
  });
  test("a turn in the group animates the face", () => {
    const f = groupFace({ ...a, status: "idle" } as AgentItem, { ...st, work: "idle" }, true);
    expect(f.st.work).toBe("thinking");
  });
});
