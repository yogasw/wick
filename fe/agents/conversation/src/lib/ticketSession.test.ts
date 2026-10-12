import { describe, expect, test } from "vitest";
import { alsoReplied, speakerLabel } from "./ticketSession.js";
import type { ConversationTurn } from "./types/agents.js";

const turn = (speaker: ConversationTurn["speaker"], role = "assistant") => ({ turn_id: Math.random().toString(), role, text: "x", speaker }) as unknown as ConversationTurn;

describe("ticket session labels", () => {
  test("someone else's agent carries its owner; your own does not", () => {
    expect(speakerLabel({ handle: "captain", name: "Captain", owner_handle: "hana" }, false)).toBe("Captain · @hana");
    expect(speakerLabel({ handle: "captain", name: "Captain", owner_handle: "yoga" }, true)).toBe("Captain");
    expect(speakerLabel({ handle: "research" }, false)).toBe("@research");
  });
  test("also replied: once per (agent, owner), home turns left out", () => {
    const mine = (id: string) => id === "yr";
    const list = alsoReplied(
      [
        turn(undefined),
        turn({ agent_id: "yr", handle: "research", name: "Research", via: "group", owner_id: "yoga", owner_handle: "yoga" }),
        turn({ agent_id: "hc", handle: "captain", name: "Captain", via: "group", owner_id: "hana", owner_handle: "hana" }),
        turn({ agent_id: "yr", handle: "research", name: "Research", via: "group", owner_id: "yoga", owner_handle: "yoga" }),
        turn({ agent_id: "yc", handle: "captain", via: "direct" }),
        turn({ agent_id: "hc", handle: "captain", via: "group", owner_id: "hana" }, "user"),
      ],
      mine,
    );
    expect(list.map((r) => r.label)).toEqual(["Research", "Captain · @hana"]);
  });
});
