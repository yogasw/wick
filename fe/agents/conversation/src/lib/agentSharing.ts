import type { AgentItem, AgentShare, ShareUser } from "./api/team.js";

/* Agents shared with the user (chat only, plan keputusan 18). The roster
   marks them, their ⋯ menu keeps only what a recipient may do, and their
   Settings drawer is a read-only card. Pure, so the rules are tested. */

type Shared = Pick<AgentItem, "role">;

/** isSharedAgent: another owner shared a with the user. */
export function isSharedAgent(a: Shared | null | undefined): boolean {
  return a?.role === "viewer";
}

/** sharedLabel is the roster's small marker text: "shared by <owner>". */
export function sharedLabel(a: Pick<AgentItem, "role" | "shared_by">): string {
  if (!isSharedAgent(a)) return "";
  return a.shared_by ? `shared by ${a.shared_by}` : "shared with you";
}

/** VIEWER_MENU is what a recipient keeps from the agent's ⋯ menu. */
export const VIEWER_MENU = ["Chats"];

/** agentMenu is the ⋯ menu for a: the full menu for the user's own agent;
    for a shared one only VIEWER_MENU items plus info. */
export function agentMenu<T extends { label: string }>(a: Shared | null | undefined, items: T[], info: T): T[] {
  if (!isSharedAgent(a)) return items;
  return [...items.filter((i) => VIEWER_MENU.includes(i.label)), info];
}

export const PICK_LIMIT = 8;

/** pickableUsers is the share picker's list: users the agent is not shared
    with yet whose name matches query, PICK_LIMIT at most. */
export function pickableUsers(users: ShareUser[], shares: AgentShare[], query: string): ShareUser[] {
  const taken = new Set(shares.map((s) => s.user_id));
  const q = query.trim().toLowerCase();
  return users.filter((u) => !taken.has(u.id) && (!q || u.name.toLowerCase().includes(q))).slice(0, PICK_LIMIT);
}
