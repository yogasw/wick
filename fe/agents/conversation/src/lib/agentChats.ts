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
