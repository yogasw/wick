import { describe, expect, it } from "vitest";
import { composerHint, groupFormError, handlesLine, mergeTurn, overrideChoices, stacked, toggleMember } from "../teamGroups.js";
import type { GroupMember } from "../api/team.js";

const m = (handle: string): GroupMember => ({ id: `a-${handle}`, handle, name: handle, avatar: { shape: "circle", color: "#0a0" }, is_captain: false, disabled: false, max_hops: 4 });

describe("New group dialog", () => {
  it("needs a name and two agents", () => {
    expect(groupFormError("", ["a", "b"])).toBe("Give the group a name.");
    expect(groupFormError("Ops", ["a"])).toBe("Pick at least 2 agents.");
    expect(groupFormError("Ops", ["a", "a"])).toBe("Pick at least 2 agents.");
    expect(groupFormError("Ops", ["a", "b"])).toBe("");
  });
  it("keeps pick order", () => {
    expect(toggleMember(toggleMember(["b"], "a"), "b")).toEqual(["a"]);
    expect(toggleMember(["b"], "a")).toEqual(["b", "a"]);
  });
});

describe("group header", () => {
  it("stacks three avatars and counts the rest", () => {
    const r = stacked([m("a"), m("b"), m("c"), m("d")]);
    expect(r.shown.map((x) => x.handle)).toEqual(["a", "b", "c"]);
    expect(r.more).toBe(1);
    expect(handlesLine([m("a"), m("b")])).toBe("@a · @b");
  });
});

describe("group composer hint", () => {
  it("names the responder and the cap", () => {
    expect(composerHint({ responder: "captain", max_hops: 3 })).toEqual({
      route: "Message the group — no @ goes to @captain",
      cap: "max 3 agent-to-agent turns",
    });
    expect(composerHint({ responder: "", max_hops: 1 }).cap).toBe("max 1 agent-to-agent turn");
  });
  it("offers only lower overrides", () => {
    expect(overrideChoices(3).map((c) => c.value)).toEqual([0, 1, 2]);
  });
  it("merges a live turn once", () => {
    const t = { turn_id: "1", ts: "", role: "assistant" as const, text: "hi" };
    expect(mergeTurn(mergeTurn([], t), t)).toHaveLength(1);
  });
});

describe("backing link", () => {
  it("points a member reply at the session it ran in", async () => {
    const { backingLink } = await import("../teamGroups.js");
    expect(backingLink({ speaker: { agent_id: "a1", handle: "anton", via: "group", session_id: "s1" } })).toEqual({ handle: "anton", session: "s1" });
    expect(backingLink({ speaker: { agent_id: "a1", handle: "anton", via: "group" } })).toBeNull();
    expect(backingLink({ speaker: null })).toBeNull();
  });
});
