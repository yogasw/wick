import { isWorking, type AgentItem } from "./api/team.js";

/* What a roster row says about an agent (mockup row() / stateOf()): the
   red unread dot, the typing line and the hover tip. Pure, so the order
   the cues win in is tested rather than buried in the template. */

export type RosterStatus = {
  /** Red dot on the avatar: something new since the chat was last open. */
  unread: boolean;
  /** Waiting on the owner (ask_user / approval): the avatar's alert pose. */
  attention: boolean;
  /** Preview line while a turn runs — the tool it is on, or "Typing".
      null = not working, show the normal preview. */
  typing: string | null;
  /** Hover tip on the avatar. */
  tip: string;
};

type Row = Pick<AgentItem, "id" | "status" | "disabled"> &
  Partial<Pick<AgentItem, "unread" | "needs_attention" | "current_action">>;

/** rosterStatus reads one row. activeId is the agent whose chat is on
    screen: what it says is being read as it arrives, so it never shows
    unread. */
export function rosterStatus(a: Row, opts: { activeId?: string; hatching?: boolean } = {}): RosterStatus {
  const working = isWorking(a.status) && !a.disabled;
  const action = (a.current_action ?? "").trim();
  const unread = !!a.unread && !a.disabled && a.id !== opts.activeId;
  const attention = !!a.needs_attention && !a.disabled;
  let tip: string;
  if (a.disabled) tip = "nonaktif";
  else if (opts.hatching) tip = "baru menetas";
  else if (attention) tip = "butuh perhatianmu";
  else if (working) tip = action || "sedang mengetik";
  else if (unread) tip = "pesan baru";
  else tip = "online · idle";
  return { unread, attention, typing: working ? action || "Typing" : null, tip };
}
