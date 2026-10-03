/* Group chats of Team agents: the pure rules the dialog, header and
   composer show. The server decides routing and caps (teamlink/group.go);
   these only word them. */

import type { GroupItem, GroupMember, GroupTurn } from "./api/team.js";

export const MIN_GROUP_MEMBERS = 2;
export const GROUP_NAME_MAX = 64;

/** groupFormError is why the New group / Settings form can't be saved, "" when it can. */
export function groupFormError(name: string, members: string[]): string {
  if (!name.trim()) return "Give the group a name.";
  if (name.trim().length > GROUP_NAME_MAX) return `Name is at most ${GROUP_NAME_MAX} characters.`;
  if (new Set(members).size < MIN_GROUP_MEMBERS) return `Pick at least ${MIN_GROUP_MEMBERS} agents.`;
  return "";
}

/** toggleMember adds or removes id, keeping pick order (the first picked is the "first" responder). */
export function toggleMember(members: string[], id: string): string[] {
  return members.includes(id) ? members.filter((m) => m !== id) : [...members, id];
}

/** stacked is the members drawn as overlapping avatars, capped at max, plus how many are left out. */
export function stacked(members: GroupMember[], max = 3): { shown: GroupMember[]; more: number } {
  return { shown: members.slice(0, max), more: Math.max(0, members.length - max) };
}

/** composerHint is the line under the group composer: where a message with no @ goes and the cap. */
export function composerHint(g: Pick<GroupItem, "responder" | "max_hops">): { route: string; cap: string; caption: string } {
  const who = g.responder ? `@${g.responder}` : "nobody (no enabled agent)";
  const n = g.max_hops;
  const cap = `max ${n} agent-to-agent turn${n === 1 ? "" : "s"}`;
  return {
    route: `Message the group — no @ goes to ${who}`,
    cap,
    // The Composer caption has little room: the rule and the cap first.
    caption: `No @ → ${who} · ${cap}`,
  };
}

/** handlesLine lists the members as @handles for the header. */
export function handlesLine(members: GroupMember[]): string {
  return members.map((m) => `@${m.handle}`).join(" · ");
}

/** overrideChoices are the caps a group may pick: none, or 1..the members' smallest cap. */
export function overrideChoices(membersMaxHops: number): { value: number; label: string }[] {
  const out = [{ value: 0, label: `Members' limit (${membersMaxHops})` }];
  for (let i = 1; i < membersMaxHops; i++) out.push({ value: i, label: String(i) });
  return out;
}

/** backingLink is the "open in @x's chat" target of a member's group
    reply: that agent's backing session in the Team app, or null when the
    turn names none (older turns). */
export function backingLink(t: Pick<GroupTurn, "speaker">): { handle: string; session: string } | null {
  const s = t.speaker;
  if (!s?.handle || !s.session_id) return null;
  return { handle: s.handle, session: s.session_id };
}

/** mergeTurn adds a live turn to the thread once (by turn_id). */
export function mergeTurn(turns: GroupTurn[], t: GroupTurn): GroupTurn[] {
  if (t.turn_id && turns.some((x) => x.turn_id === t.turn_id)) return turns;
  return [...turns, t];
}
