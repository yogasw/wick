/* The Chats drawer's list logic, kept out of the component so it can be
   tested: the main chat is pinned first, and pinning another chat moves
   the pin without dropping the old main. */
import type { AgentSessionItem } from "./api/team.js";

/** orderChats puts the main chat first; the rest keep the server's
    newest-first order. */
export function orderChats(items: AgentSessionItem[]): AgentSessionItem[] {
  return [...items.filter((s) => s.agent_main), ...items.filter((s) => !s.agent_main)];
}

/** pinChat makes id the main chat: the old main stays, as an ordinary chat. */
export function pinChat(items: AgentSessionItem[], id: string): AgentSessionItem[] {
  return orderChats(items.map((s) => ({ ...s, agent_main: s.id === id })));
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
