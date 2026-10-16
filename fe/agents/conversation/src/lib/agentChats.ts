/* The Chats drawer's list logic, kept out of the component so it can be
   tested: the main chat is pinned first, and pinning another chat moves
   the pin without dropping the old main. */
import type { AgentSessionItem } from "./api/team.js";

/** orderChats puts the main chat first; the rest keep the server's
    newest-first order. */
export function orderChats(items: AgentSessionItem[]): AgentSessionItem[] {
  return [...items.filter((s) => s.agent_main), ...items.filter((s) => !s.agent_main)];
}

/** pinChat makes id the main chat: the old main stays, as an ordinary chat.
    Only the caller's own chats move: another person's main chat (All tab)
    stays theirs. */
export function pinChat(items: AgentSessionItem[], id: string): AgentSessionItem[] {
  return orderChats(items.map((s) => (s.mine === false ? s : { ...s, agent_main: s.id === id })));
}

/** The Chats drawer's tabs, shown only while the agent is shared. */
export type ChatsTab = "you" | "all";

const lastActiveMs = (s: AgentSessionItem) => (s.last_active ? new Date(s.last_active).getTime() || 0 : 0);

/** chatsForTab is what a tab lists. You: the caller's own chats, their
    main chat first. All: one main chat per person (the caller's first,
    then the others by last active), then every other chat by last active. */
export function chatsForTab(items: AgentSessionItem[], tab: ChatsTab): AgentSessionItem[] {
  if (tab === "you") return orderChats(items.filter((s) => s.mine !== false));
  const byActive = (a: AgentSessionItem, b: AgentSessionItem) => lastActiveMs(b) - lastActiveMs(a);
  const mains = items.filter((s) => s.agent_main).sort((a, b) => {
    if ((a.mine !== false) !== (b.mine !== false)) return a.mine !== false ? -1 : 1;
    return byActive(a, b);
  });
  return [...mains, ...items.filter((s) => !s.agent_main).sort(byActive)];
}

/** chatLabel is a row's title: "📌 Main chat" for the caller's main, with
    the owner's name in All for anyone else's ("📌 Main chat · Dana"). */
export function chatLabel(s: AgentSessionItem, tab: ChatsTab): string {
  if (s.agent_main) {
    return tab === "all" && s.mine === false ? `📌 Main chat · ${s.owner_name || s.owner_user_id || ""}` : "📌 Main chat";
  }
  return s.label || s.id;
}

/** filterChats is the drawer's search: a case-insensitive substring of the
    row's title (chatLabel, so "main" finds the main chat), order kept. */
export function filterChats(items: AgentSessionItem[], q: string, tab: ChatsTab): AgentSessionItem[] {
  const needle = q.trim().toLowerCase();
  if (!needle) return items;
  return items.filter((s) => chatLabel(s, tab).toLowerCase().includes(needle));
}

/** chatLifecycle is a row's badge: the live process's lifecycle, else
    what the session status says while a turn is running or queued. */
export function chatLifecycle(s: AgentSessionItem): string {
  if (s.lifecycle) return s.lifecycle;
  return s.status === "running" ? "working" : s.status === "queued" ? "queued" : "";
}

/** pinChatVia asks the server first (save) and only then moves the pin in
    the list, so a refused pin leaves the drawer as it was. */
export async function pinChatVia(
  items: AgentSessionItem[],
  id: string,
  save: (id: string) => Promise<unknown>,
): Promise<AgentSessionItem[]> {
  await save(id);
  return pinChat(items, id);
}

/** A draft chat's first message: what the composer hands over. */
export type DraftMessage = { text: string; files: File[] };

/** startDraftChat turns a draft into a chat. "+ New chat" opens a draft
    with no session behind it; the session is created here, on the first
    Send, and the message goes into it. Leaving a draft unsent leaves
    nothing behind. A send that fails after the create still returns the
    new chat (with the error), so the user lands in it and can retry. */
export async function startDraftChat(
  msg: DraftMessage,
  open: () => Promise<{ session_id: string }>,
  send: (sessionId: string, msg: DraftMessage) => Promise<unknown>,
  prepare?: (sessionId: string) => Promise<unknown>,
): Promise<{ sessionId: string; error?: string }> {
  const { session_id } = await open();
  try {
    // e.g. the session fields a plugin remote agent's first turn uses: a
    // failure keeps the message unsent so it never runs with other values.
    if (prepare) await prepare(session_id);
    await send(session_id, msg);
    return { sessionId: session_id };
  } catch (e) {
    return { sessionId: session_id, error: e instanceof Error ? e.message : String(e) };
  }
}
