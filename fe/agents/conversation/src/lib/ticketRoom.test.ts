import { describe, test, expect } from "vitest";
import { addable, memberLabel, responderHint, roomAgentOptions, roomMentionAgents, withRoomAgent } from "./ticketRoom.js";
import type { TicketRoom } from "./api/tickets.js";
import type { TicketCard } from "./types/agents.js";

const room: TicketRoom = {
  session_id: "room-1",
  ticket_id: "T-1",
  project_id: "p1",
  share_full: false,
  responder: "captain",
  members: [
    { kind: "project_default", handle: "project", name: "Billing", home: true, mine: true },
    { kind: "agent", agent_id: "a-cap", handle: "captain", name: "Captain", owner_id: "alice", owner_handle: "alice", is_captain: true, mine: true },
    { kind: "agent", agent_id: "h-cap", handle: "captain", name: "Captain", owner_id: "hana", owner_handle: "hana", is_captain: true, mine: false },
  ],
};
const own = [
  { id: "a-cap", handle: "captain", name: "Captain", is_captain: true },
  { id: "a-log", handle: "log-hunter", name: "Log Hunter" },
  { id: "a-off", handle: "off", name: "Off", disabled: true },
];

describe("ticketRoom", () => {
  test("@ menu: Your Team (all your enabled agents), then others' members greyed with their owner", () => {
    const rows = roomMentionAgents(room, own);
    expect(rows.map((r) => [r.handle, r.group, r.section ?? ""])).toEqual([
      ["captain", "team", "Your Team"],
      ["log-hunter", "team", "Your Team"],
      ["hana/captain", "others", ""],
    ]);
    expect(rows[2]).toMatchObject({ label: "Captain · @hana", hint: "only takes instructions from @hana" });
  });

  test("responder hint names where a message with no @ goes", () => {
    expect(responderHint(room, "Billing")).toBe("No @ → @captain");
    expect(responderHint({ ...room, responder: "" }, "Billing")).toContain("nobody of yours");
    expect(responderHint(null, "Billing")).toContain("Billing");
  });

  test("labels name other people's agents by owner", () => {
    expect(memberLabel(room.members[1])).toBe("Captain");
    expect(memberLabel(room.members[2])).toBe("Captain · @hana");
  });

  test("+ Add agents offers your enabled agents, marking those already in", () => {
    expect(addable(room, own).map((r) => [r.agent.id, r.inRoom])).toEqual([["a-cap", true], ["a-log", false]]);
  });

  test("Agent in room: options once per (agent, owner); filter keeps the tickets with that member", () => {
    const card = (id: string, members: TicketCard["room"]): TicketCard => ({
      id, title: id, status: "open", sessions: 1, notes: 0, open_tasks: 0, updated_at: "", created_at: "", stale: false, room: members,
    });
    const cards = [
      card("T-1", { session_id: "r1", members: [{ kind: "agent", agent_id: "c", handle: "captain", name: "Captain", owner_id: "alice", owner_handle: "alice" }] }),
      card("T-2", { session_id: "r2", members: [{ kind: "agent", agent_id: "c2", handle: "captain", name: "Captain", owner_id: "hana", owner_handle: "hana" }] }),
      card("T-3", undefined),
    ];
    expect(roomAgentOptions(cards).map((o) => o.label)).toEqual(["Captain · @alice", "Captain · @hana"]);
    expect(withRoomAgent(cards, "hana:c2").map((c) => c.id)).toEqual(["T-2"]);
    expect(withRoomAgent(cards, "")).toHaveLength(3);
  });
});
