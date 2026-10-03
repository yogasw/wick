import type { AgentItem, GroupItem } from "./api/team.js";
import { MIN_GROUP_MEMBERS } from "./teamGroups.js";

/* The Team sidebar's list (team-sidebar mockup, final): agents and group
   chats mixed in one list, newest activity first, plus the mobile
   drawer's row of three big pinned agents. Pure, so the order and which
   agent lands in a pin are tested rather than buried in the template. */

export type RosterEntry =
  | { kind: "agent"; id: string; agent: AgentItem }
  | { kind: "group"; id: string; group: GroupItem };

type Searchable = Pick<AgentItem, "name" | "handle">;

const ts = (raw: string | null | undefined) => (raw ? Date.parse(raw) || 0 : 0);

function matches(q: string, a: Searchable): boolean {
  return !q || a.name.toLowerCase().includes(q) || a.handle.toLowerCase().includes(q);
}

/** rosterEntries is the sidebar list: agents and groups that match the
    search, the Captain pinned on top, the rest newest last_active first,
    ties by name. A group matches on its own name or any member's. */
export function rosterEntries(agents: AgentItem[], groups: GroupItem[], query: string): RosterEntry[] {
  const q = query.trim().toLowerCase();
  const list: RosterEntry[] = [
    ...agents.filter((a) => matches(q, a)).map((a) => ({ kind: "agent" as const, id: a.id, agent: a })),
    ...groups
      .filter((g) => !q || g.name.toLowerCase().includes(q) || g.members.some((m) => matches(q, m)))
      .map((g) => ({ kind: "group" as const, id: g.id, group: g })),
  ];
  const at = (e: RosterEntry) => ts(e.kind === "agent" ? e.agent.last_active : e.group.last_active);
  const name = (e: RosterEntry) => (e.kind === "agent" ? e.agent.name : e.group.name);
  const captain = (e: RosterEntry) => (e.kind === "agent" && e.agent.is_captain ? 1 : 0);
  return list.sort((x, y) => captain(y) - captain(x) || at(y) - at(x) || name(x).localeCompare(name(y)));
}

export const PIN_COUNT = 3;

/** mobilePins is the drawer's row of big avatars: the pinned agents
    when there are any, topped up with the Captain and then the agents
    used most recently, PIN_COUNT at most. Disabled agents are left out. */
export function mobilePins(agents: AgentItem[], pinned: string[] = []): AgentItem[] {
  const live = agents.filter((a) => !a.disabled);
  const out: AgentItem[] = [];
  const add = (a: AgentItem | undefined) => {
    if (a && out.length < PIN_COUNT && !out.includes(a)) out.push(a);
  };
  for (const id of pinned) add(live.find((a) => a.id === id));
  add(live.find((a) => a.is_captain));
  for (const a of [...live].sort((x, y) => ts(y.last_active) - ts(x.last_active))) add(a);
  return out;
}

/** canMakeGroup is whether New group is offered: a group needs at least
    MIN_GROUP_MEMBERS agents to pick from. */
export function canMakeGroup(agents: Pick<AgentItem, "id">[]): boolean {
  return agents.length >= MIN_GROUP_MEMBERS;
}

/** unreadLabel is the number in a row's green unread pill: the count when
    the server sends one, capped at "99+"; "" leaves a plain green dot
    (the roster API only flags unread today). */
export function unreadLabel(count?: number): string {
  if (!count || count < 1) return "";
  return count > 99 ? "99+" : String(Math.floor(count));
}
