import { describe, expect, it } from "vitest";
import { clampHops, hopsNote, mentionFromOf, MENTION_FROM_OPTIONS } from "../mentionSettings.js";
import { teamMentionAgents } from "../teamMention.js";

describe("mention settings", () => {
  it("reads unknown policies as all", () => {
    expect(mentionFromOf(undefined)).toBe("all");
    expect(mentionFromOf("nope")).toBe("all");
    expect(mentionFromOf("off")).toBe("off");
    expect(MENTION_FROM_OPTIONS.map((o) => o.value)).toEqual(["all", "captain", "list", "off"]);
  });
  it("clamps the hop cap to 1..10", () => {
    expect(clampHops(0)).toBe(4);
    expect(clampHops(NaN)).toBe(4);
    expect(clampHops(42)).toBe(10);
    expect(clampHops(2.4)).toBe(2);
    expect(hopsNote(1)).toContain("After 1 agent-to-agent turn in a row");
  });
  it("hides agents whose mentions are off from the @ menu", () => {
    const peers = [
      { id: "a", handle: "a", name: "A", description: "", disabled: false },
      { id: "b", handle: "b", name: "B", description: "", disabled: false, mention_from: "off" },
      { id: "c", handle: "c", name: "C", description: "", disabled: false, mention_from: "captain" },
    ];
    expect(teamMentionAgents(peers, "a").map((p) => p.handle)).toEqual(["c"]);
  });
});
