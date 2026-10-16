import type { AgentItem, AgentShare, ShareUser } from "./api/team.js";
import type { RailTab } from "./agentMode.js";
import { REMOTE_HIDDEN_TABS } from "./remoteAgent.js";

/* Agents shared with the user (chat only, plan keputusan 18). The roster
   marks them, their ⋯ menu keeps only what a recipient may do, and their
   Settings drawer is a read-only card. Pure, so the rules are tested. */

type Shared = Pick<AgentItem, "role">;

/** isSharedAgent: another owner shared a with the user. */
export function isSharedAgent(a: Shared | null | undefined): boolean {
  return a?.role === "viewer";
}

/** sharedLabel is the short "shared by <owner>" text (Info menu hint, info drawer). */
export function sharedLabel(a: Pick<AgentItem, "role" | "shared_by">): string {
  if (!isSharedAgent(a)) return "";
  return a.shared_by ? `shared by ${a.shared_by}` : "shared with you";
}

/** initialsOf is the owner's tiny avatar text: the first letters of the
    first and last word ("Yoga Setiawan" → "YS"), one letter for one word. */
export function initialsOf(name: string | undefined): string {
  const words = (name ?? "").trim().split(/\s+/).filter(Boolean);
  if (words.length === 0) return "?";
  const first = Array.from(words[0])[0] ?? "";
  const last = words.length > 1 ? Array.from(words[words.length - 1])[0] ?? "" : "";
  return (first + last).toUpperCase();
}

/** shareInfo is the chat header's sharing part: whose it is for a
    recipient, how many have it for the owner, null when not shared. */
export function shareInfo(a: Pick<AgentItem, "role" | "shared_by" | "share_count">):
  | { kind: "shared-by"; owner: string; initials: string }
  | { kind: "shared-with"; count: number }
  | null {
  if (isSharedAgent(a)) {
    const owner = a.shared_by || "its owner";
    return { kind: "shared-by", owner, initials: initialsOf(a.shared_by) };
  }
  const n = a.share_count ?? 0;
  return n > 0 ? { kind: "shared-with", count: n } : null;
}

/** VIEWER_MENU is what a recipient keeps from the agent's ⋯ menu. */
export const VIEWER_MENU = ["Chats"];

/** agentMenu is the ⋯ menu for a: the full menu for the user's own agent;
    for a shared one only VIEWER_MENU items plus info. */
export function agentMenu<T extends { label: string }>(a: Shared | null | undefined, items: T[], info: T): T[] {
  if (!isSharedAgent(a)) return items;
  return [...items.filter((i) => VIEWER_MENU.includes(i.label)), info];
}

export const SHARED_RAIL_NOTE = "Shared with you for chat only — its files and tools stay with its owner.";

/** sharedChatMode is what DetailView's agentMode differs in for a shared
    agent's chat: every rail tab hidden (they read the owner's project), a
    footer saying why, and nothing loaded for them. null for the user's own
    agent. */
export function sharedChatMode(a: Shared | null | undefined): { hideTabs: RailTab[]; railNote: string; chatOnly: true } | null {
  if (!isSharedAgent(a)) return null;
  return { hideTabs: [...REMOTE_HIDDEN_TABS], railNote: SHARED_RAIL_NOTE, chatOnly: true };
}

export const PICK_LIMIT = 8;

/** pickableUsers is the share picker's list: users the agent is not shared
    with yet whose name matches query, PICK_LIMIT at most. */
export function pickableUsers(users: ShareUser[], shares: AgentShare[], query: string): ShareUser[] {
  const taken = new Set(shares.map((s) => s.user_id));
  const q = query.trim().toLowerCase();
  return users.filter((u) => !taken.has(u.id) && (!q || u.name.toLowerCase().includes(q))).slice(0, PICK_LIMIT);
}

/** shareHistoryNote is the risk note under "Recipients can view chat
    history". A built-in agent shares its project folder, memory and files
    with everyone, so hiding chats is UI only; a remote agent has no shared
    files; a Slack remote's threads stay visible in Slack itself. */
export function shareHistoryNote(remoteKind: string | undefined): string {
  if (!remoteKind) {
    return "Turning this off hides other people's chats in the UI only. The agent shares its project folder, memory and files across everyone it talks to, so it may still repeat what another user told it.";
  }
  const base = "Turning this off hides other people's chats in wick. The remote agent may still remember what another user told it.";
  return remoteKind === "slack" ? `${base} Hiding a chat in wick does not hide its thread in Slack.` : base;
}

/** shareHistoryOn is a share's toggle state: its own value, else the
    agent type's default (on for built-in, off for remote). */
export function shareHistoryOn(s: Pick<AgentShare, "history_visible">, historyDefault: boolean | undefined): boolean {
  return s.history_visible ?? historyDefault ?? true;
}

/** chatNeedsAccessCheck: a recipient's ?session= that is not their own
    main chat may name a chat that is no longer open to them (an old link
    to the owner's chat after history was turned off). Checked once
    against the chats they may list before the chat view mounts, so a
    closed one never fires its /conversation, /meta, /context or stream. */
export function chatNeedsAccessCheck(
  a: Pick<AgentItem, "role" | "main_session_id"> | null | undefined,
  session: string | null | undefined,
): boolean {
  return isSharedAgent(a) && !!session && session !== a?.main_session_id;
}

/** chatListed: session is one of the rows the caller may list. */
export function chatListed(rows: { id: string }[] | null | undefined, session: string): boolean {
  return (rows ?? []).some((r) => r.id === session);
}

/** noAccessError: a 404 on a chat read — a chat the caller may not open
    (another person's, history off) or one that is gone. An expected
    state: the view shows the empty chat, never an alert. */
export function noAccessError(e: unknown): boolean {
  const msg = e instanceof Error ? e.message : String(e);
  return /not found|404/i.test(msg);
}
