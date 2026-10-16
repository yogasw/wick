import { describe, expect, test } from "vitest";
import { sessionAgents, speakerLabel } from "./ticketSession.js";
import type { ConversationTurn } from "./types/agents.js";

const turn = (speaker: ConversationTurn["speaker"], role = "assistant") => ({ turn_id: Math.random().toString(), role, text: "x", speaker }) as unknown as ConversationTurn;

describe("ticket session labels", () => {
  test("someone else's agent carries its owner; your own does not", () => {
    expect(speakerLabel({ handle: "captain", name: "Captain", owner_handle: "hana" }, false)).toBe("Captain · @hana");
    expect(speakerLabel({ handle: "captain", name: "Captain", owner_handle: "yoga" }, true)).toBe("Captain");
    expect(speakerLabel({ handle: "research" }, false)).toBe("@research");
  });
});

describe("session agents (rail Session tab)", () => {
  test("owner, how it joined and reply count per (agent, owner)", () => {
    const list = sessionAgents(
      [
        turn({ agent_id: "li", handle: "logs", name: "Log Investigator", via: "mention", owner_id: "hana", owner_handle: "hana" }),
        turn({ agent_id: "hc", handle: "captain", name: "Captain", via: "group", owner_id: "hana", owner_handle: "hana" }),
        turn({ agent_id: "li", handle: "logs", name: "Log Investigator", via: "mention", owner_id: "hana", owner_handle: "hana" }),
        turn({ agent_id: "yc", handle: "captain", via: "direct" }),
        turn({ agent_id: "li", handle: "logs", via: "mention", owner_id: "hana", owner_handle: "hana" }),
      ],
      () => false,
    );
    expect(list.map(({ name, owner, joined, replies }) => ({ name, owner, joined, replies }))).toEqual([
      { name: "Log Investigator", owner: "@hana", joined: "via @mention", replies: 3 },
      { name: "Captain", owner: "@hana", joined: "joined when @hana wrote", replies: 1 },
    ]);
  });
  test("the viewer's own agent carries no owner", () => {
    const [a] = sessionAgents([turn({ agent_id: "yr", handle: "research", via: "mention", owner_id: "yoga", owner_handle: "yoga" })], (id) => id === "yr");
    expect(a.owner).toBe("");
    expect(a.name).toBe("@research");
  });
  // The server counts the whole history; the loaded pages may hold none of
  // an agent's replies, or newer ones the server has not seen yet.
  test("the server's whole-history count comes first, loaded turns add to it", () => {
    const list = sessionAgents(
      [
        turn({ agent_id: "hc", handle: "captain", name: "Captain", via: "group", owner_id: "hana", owner_handle: "hana" }),
        turn({ agent_id: "dx", handle: "deploy", via: "mention", owner_id: "dana", owner_handle: "dana" }),
        turn({ agent_id: "dx", handle: "deploy", via: "mention", owner_id: "dana", owner_handle: "dana" }),
      ],
      () => false,
      [
        { agent_id: "li", handle: "logs", name: "Log Investigator", via: "mention", owner_id: "hana", owner_handle: "hana", replies: 4 },
        { agent_id: "hc", handle: "captain", name: "Captain", via: "group", owner_id: "hana", owner_handle: "hana", replies: 5 },
        { agent_id: "dx", handle: "deploy", via: "mention", owner_id: "dana", owner_handle: "dana", replies: 1 },
      ],
    );
    expect(list.map(({ name, replies }) => ({ name, replies }))).toEqual([
      { name: "Log Investigator", replies: 4 },
      { name: "Captain", replies: 5 },
      { name: "@deploy", replies: 2 },
    ]);
  });
});
