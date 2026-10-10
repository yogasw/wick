import { describe, test, expect } from "vitest";
import { mainChatOpen, rosterActivity, taskActivity } from "../avatarActivity.js";
import { rosterStatus } from "../rosterStatus.js";

/* P39: which busy cue another session's avatar shows. */
describe("rosterActivity", () => {
  const row = { id: "a", status: "idle", disabled: false };

  test("working: thinking, a tool (or its sub-agents) and a remote's wait", () => {
    expect(rosterActivity(rosterStatus({ ...row, status: "running" }))).toBe("thinking");
    expect(rosterActivity(rosterStatus({ ...row, status: "running", current_action: "Bash" }))).toBe("tool");
    expect(rosterActivity(rosterStatus({ ...row, subagents_working: ["helper"] }))).toBe("tool");
    expect(rosterActivity(rosterStatus({ ...row, status: "running", kind: "a2a-remote" }))).toBe("remote");
  });

  test("waiting on the person outranks working", () => {
    expect(rosterActivity(rosterStatus({ ...row, status: "running", current_action: "Bash", needs_attention: true }))).toBe("alert");
    expect(rosterActivity(rosterStatus({ ...row, needs_attention: true }))).toBe("alert");
  });

  test("idle, disabled and the chat on screen stay static", () => {
    expect(rosterActivity(rosterStatus(row))).toBe("idle");
    expect(rosterActivity(rosterStatus({ ...row, unread: true }))).toBe("idle");
    expect(rosterActivity(rosterStatus({ ...row, status: "running", disabled: true, needs_attention: true }))).toBe("idle");
    expect(rosterActivity(rosterStatus({ ...row, status: "running", needs_attention: true }), true)).toBe("idle");
  });
});

describe("taskActivity", () => {
  test("needs_you pulses, working orbits, the rest is static", () => {
    expect(taskActivity("needs_you")).toBe("alert");
    expect(taskActivity("working")).toBe("tool");
    for (const s of ["answering", "replied", "failed", "canceled"] as const) expect(taskActivity(s)).toBe("idle");
  });
});

/* S4: the row's status is the main session's, so only that chat on screen
   silences it. */
describe("mainChatOpen", () => {
  const a = { id: "a", main_session_id: "m1" };
  test("the agent's main chat on screen", () => {
    expect(mainChatOpen(a, "a", { session: null })).toBe(true);
    expect(mainChatOpen(a, "a", { session: "m1" })).toBe(true);
  });
  test("another chat of the same agent, another agent or a group chat", () => {
    expect(mainChatOpen(a, "a", { session: "s2" })).toBe(false);
    expect(mainChatOpen(a, "b", { session: null })).toBe(false);
    expect(mainChatOpen(a, undefined, { session: null })).toBe(false);
    expect(mainChatOpen(a, "a", { session: null, group: "g1" })).toBe(false);
  });
  test("another chat open: the main session's question still pulses", () => {
    const st = rosterStatus({ id: "a", status: "idle", disabled: false, needs_attention: true });
    expect(rosterActivity(st, mainChatOpen(a, "a", { session: "s2" }))).toBe("alert");
    expect(rosterActivity(st, mainChatOpen(a, "a", { session: null }))).toBe("idle");
  });
});
