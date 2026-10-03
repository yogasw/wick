import { describe, test, expect } from "vitest";
import { isSharedAgent, sharedLabel, agentMenu, pickableUsers, PICK_LIMIT } from "../agentSharing.js";
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
});
