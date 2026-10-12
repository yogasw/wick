import type { ComposerMentionAgent } from "@wick-fe/common-ui";
import type { TicketRoom, TicketRoomMember } from "./api/tickets.js";
import type { TicketCard, TicketCardRoomAgent } from "./types/agents.js";

/* A ticket room's view rules (PLAN-team-tickets §4/§5), kept out of the
   components so they are tested without mounting anything. Identity is
   (agent, owner), never the handle: everyone's Captain is "captain". */

/** One of the caller's own Team agents, as the @ menu and + Add agents
    list them. */
export type OwnAgent = {
  id: string;
  handle: string;
  name: string;
  is_captain?: boolean;
  disabled?: boolean;
  avatar?: { kind?: string; shape?: string; color?: string; expression?: string };
};

/** roomMentionAgents is the room composer's @ menu: "Your Team" — every
    one of the caller's agents, in the room or not (naming one brings it
    in) — then "In this room · others": other people's members, greyed,
    since only their owner can ask them. */
export function roomMentionAgents(room: TicketRoom | null, own: OwnAgent[]): ComposerMentionAgent[] {
  const mine: ComposerMentionAgent[] = own
    .filter((a) => !a.disabled)
    .map((a) => ({ handle: a.handle, label: a.name || a.handle, group: "team", section: "Your Team", avatar: a.avatar }));
  const others: ComposerMentionAgent[] = (room?.members ?? [])
    .filter((m) => !m.mine && m.kind === "agent")
    .map((m) => ({
      handle: `${m.owner_handle}/${m.handle}`,
      label: `${m.name || m.handle} · @${m.owner_handle}`,
      hint: `only takes instructions from @${m.owner_handle}`,
      group: "others",
      avatar: m.avatar,
    }));
  return [...mine, ...others];
}

/** responderHint is the composer caption: where a message with no @ goes. */
export function responderHint(room: TicketRoom | null, homeName: string): string {
  if (!room) return `No @ → ${homeName || "the project"} · first message opens the room`;
  if (!room.responder) return "No @ → nobody of yours · @mention one of your agents";
  return `No @ → @${room.responder}`;
}

/** memberLabel names a member: "Captain · @hana" for someone else's. */
export function memberLabel(m: TicketRoomMember): string {
  const name = m.name || m.handle;
  return !m.mine && m.owner_handle ? `${name} · @${m.owner_handle}` : name;
}

/** addable is the caller's agents + Add agents offers: all of them, those
    already in the room marked. */
export function addable(room: TicketRoom | null, own: OwnAgent[]): { agent: OwnAgent; inRoom: boolean }[] {
  const inRoom = new Set((room?.members ?? []).filter((m) => m.mine && m.agent_id).map((m) => m.agent_id));
  return own.filter((a) => !a.disabled).map((a) => ({ agent: a, inRoom: inRoom.has(a.id) }));
}

/** Key of a member on the board's "Agent in room" select. */
export const roomAgentKey = (m: Pick<TicketCardRoomAgent, "kind" | "agent_id" | "owner_id">) =>
  m.kind === "project_default" ? "project_default" : `${m.owner_id ?? ""}:${m.agent_id ?? ""}`;

/** roomAgentOptions lists, once each, every member of the cards' rooms. */
export function roomAgentOptions(cards: TicketCard[]): { key: string; label: string }[] {
  const seen = new Map<string, string>();
  for (const c of cards) {
    for (const m of c.room?.members ?? []) {
      const k = roomAgentKey(m);
      if (seen.has(k)) continue;
      const name = m.name || m.handle;
      seen.set(k, m.kind === "project_default" ? `${name} (project)` : m.owner_handle ? `${name} · @${m.owner_handle}` : name);
    }
  }
  return [...seen].map(([key, label]) => ({ key, label })).sort((a, b) => a.label.localeCompare(b.label));
}

/** withRoomAgent keeps the cards whose room has the member key ("" = all). */
export function withRoomAgent(cards: TicketCard[], key: string): TicketCard[] {
  if (!key) return cards;
  return cards.filter((c) => (c.room?.members ?? []).some((m) => roomAgentKey(m) === key));
}
