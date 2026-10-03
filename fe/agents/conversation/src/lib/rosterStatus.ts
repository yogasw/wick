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
  if (a.disabled) tip = "disabled";
  else if (opts.hatching) tip = "just hatched";
  else if (attention) tip = "needs your attention";
  else if (working) tip = action || "typing";
  else if (unread) tip = "new message";
  else tip = "online · idle";
  return { unread, attention, typing: working ? action || "Typing" : null, tip };
}

/** withTurn is the roster after agent id's main-chat turn started or
    ended on the stream: the row reads working (or idle, with its tool
    cleared) at once instead of after the next poll. */
export function withTurn<T extends Pick<AgentItem, "id" | "status"> & Partial<Pick<AgentItem, "current_action">>>(
  agents: T[],
  id: string,
  active: boolean,
): T[] {
  return agents.map((x) =>
    x.id === id ? { ...x, status: active ? "running" : "idle", current_action: active ? x.current_action : "" } : x,
  );
}
