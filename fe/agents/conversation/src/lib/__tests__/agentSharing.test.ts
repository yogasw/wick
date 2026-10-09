import { describe, test, expect } from "vitest";
import { isSharedAgent, sharedLabel, agentMenu, pickableUsers, PICK_LIMIT, sharedChatMode, SHARED_RAIL_NOTE } from "../agentSharing.js";
import { chatNeedsAccessCheck, chatListed, noAccessError } from "../agentSharing.js";
import { REMOTE_HIDDEN_TABS } from "../remoteAgent.js";
import { rosterEntries } from "../rosterList.js";
import type { AgentItem } from "../api/team.js";

const item = (label: string) => ({ label });
const full = ["Chats", "Settings", "Connections", "Scheduled", "Duplicate agent", "Disable"].map(item);

describe("agentSharing", () => {
  test("isSharedAgent and the roster marker", () => {
    expect(isSharedAgent({ role: "viewer" })).toBe(true);
    expect(isSharedAgent({})).toBe(false);
    expect(isSharedAgent(null)).toBe(false);
    expect(sharedLabel({ role: "viewer", shared_by: "Alice" })).toBe("shared by Alice");
    expect(sharedLabel({ role: "viewer" })).toBe("shared with you");
    expect(sharedLabel({})).toBe("");
  });

  test("a recipient's menu keeps Chats and adds info; the owner's is untouched", () => {
    const info = item("Info");
    expect(agentMenu({ role: "viewer" }, full, info).map((i) => i.label)).toEqual(["Chats", "Info"]);
    expect(agentMenu({}, full, info)).toBe(full);
  });

  test("pickableUsers skips people already shared with and filters by name", () => {
    const users = [{ id: "u1", name: "Alice" }, { id: "u2", name: "Bob" }, { id: "u3", name: "Albert" }];
    const shares = [{ user_id: "u1", name: "Alice", created_at: "" }];
    expect(pickableUsers(users, shares, "").map((u) => u.id)).toEqual(["u2", "u3"]);
    expect(pickableUsers(users, shares, " al ").map((u) => u.id)).toEqual(["u3"]);
    const many = Array.from({ length: 20 }, (_, i) => ({ id: `x${i}`, name: `X${i}` }));
    expect(pickableUsers(many, [], "")).toHaveLength(PICK_LIMIT);
  });

  test("shared agents sit in the roster list with the user's own", () => {
    const own = { id: "mine", name: "Mine", handle: "mine", last_active: "2026-10-01T00:00:00Z" } as AgentItem;
    const shared = { id: "lena", name: "Lena", handle: "lena", role: "viewer", last_active: "2026-10-02T00:00:00Z" } as AgentItem;
    expect(rosterEntries([own, shared], [], "").map((e) => e.id)).toEqual(["lena", "mine"]);
  });
  test("sharedChatMode hides the whole rail of a shared agent's chat", () => {
    const m = sharedChatMode({ role: "viewer" });
    expect(m?.chatOnly).toBe(true);
    expect(m?.railNote).toBe(SHARED_RAIL_NOTE);
    expect([...(m?.hideTabs ?? [])].sort()).toEqual([...REMOTE_HIDDEN_TABS].sort());
    for (const tab of ["notes", "source", "files", "process", "browser", "subagents", "todos", "ticket", "workspace", "scheduled"]) {
      expect(m?.hideTabs).toContain(tab);
    }
    expect(sharedChatMode({ role: "" } as AgentItem)).toBeNull();
    expect(sharedChatMode(null)).toBeNull();
  });
});

import { shareHistoryNote, shareHistoryOn } from "../agentSharing.js";

describe("share history toggle", () => {
  test("follows the stored value, else the type default", () => {
    expect(shareHistoryOn({}, true)).toBe(true);
    expect(shareHistoryOn({}, false)).toBe(false);
    expect(shareHistoryOn({ history_visible: false }, true)).toBe(false);
    expect(shareHistoryOn({ history_visible: true }, false)).toBe(true);
  });
  test("words the note per agent type", () => {
    expect(shareHistoryNote("")).toContain("project folder");
    expect(shareHistoryNote("a2a")).not.toContain("project folder");
    expect(shareHistoryNote("slack")).toContain("thread in Slack");
    expect(shareHistoryNote("plugin")).not.toContain("Slack");
  });
});

describe("shared chat ?session= guard", () => {
  const viewer = { role: "viewer" as const, main_session_id: "mine" };
  test("only a recipient's non-main ?session= is checked", () => {
    expect(chatNeedsAccessCheck(viewer, "owners")).toBe(true);
    expect(chatNeedsAccessCheck(viewer, "mine")).toBe(false);
    expect(chatNeedsAccessCheck(viewer, null)).toBe(false);
    expect(chatNeedsAccessCheck({ main_session_id: "mine" }, "owners")).toBe(false);
    expect(chatNeedsAccessCheck(null, "owners")).toBe(false);
  });
  test("chatListed matches the listed rows only", () => {
    expect(chatListed([{ id: "a" }, { id: "b" }], "b")).toBe(true);
    expect(chatListed([{ id: "a" }], "owners")).toBe(false);
    expect(chatListed(null, "owners")).toBe(false);
  });
});

describe("noAccessError", () => {
  test("a 404 history read is an expected state, other errors are not", () => {
    expect(noAccessError(new Error("HTTP 404: session not found"))).toBe(true);
    expect(noAccessError("not found")).toBe(true);
    expect(noAccessError(new Error("HTTP 500: boom"))).toBe(false);
  });
});
