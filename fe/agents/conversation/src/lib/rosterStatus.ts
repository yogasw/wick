import { isWorking, type AgentItem } from "./api/team.js";
import type { SessionActivity } from "./stores/sessionsStream.js";
import { THINKING_LABEL, toolActivityLabel } from "./activityLabel.js";
import { isRemoteAgent, remoteWaitTarget } from "./remoteAgent.js";

/* What a roster row says about an agent (mockup row() / stateOf()): the
   red unread dot, the working line and the hover tip. Pure, so the order
   the cues win in is tested rather than buried in the template. The Team
   header reads the same answer, so the two never disagree. */

/** What a turn is doing: idle (no turn), thinking (a turn with no tool in
    flight), tool (waiting on a tool_use's result) or waiting (a remote
    agent: wick waits for the other side). */
export type WorkState = "idle" | "thinking" | "tool" | "waiting";

export type RosterStatus = {
  /** Red dot on the avatar: something new since the chat was last open. */
  unread: boolean;
  /** Waiting on the owner (ask_user / approval): the avatar's alert pose. */
  attention: boolean;
  work: WorkState;
  /** Preview line while a turn runs: "thinking…", the tool's label
      ("running Bash…") or what a remote waits on. null = not working,
      show the normal preview. */
  typing: string | null;
  /** Hover tip on the avatar. */
  tip: string;
};

type Row = Pick<AgentItem, "id" | "status" | "disabled"> &
  Partial<Pick<AgentItem, "unread" | "needs_attention" | "current_action" | "kind" | "handle" | "slack_remote">>;

/** rosterStatus reads one row. activeId is the agent whose chat is on
    screen: what it says is being read as it arrives, so it never shows
    unread. */
export function rosterStatus(a: Row, opts: { activeId?: string; hatching?: boolean } = {}): RosterStatus {
  const working = isWorking(a.status) && !a.disabled;
  const action = (a.current_action ?? "").trim();
  const work: WorkState = !working ? "idle" : isRemoteAgent(a) ? "waiting" : action ? "tool" : "thinking";
  const typing =
    work === "idle" ? null : work === "waiting" ? `${remoteWaitTarget({ handle: a.handle ?? "", slack_remote: a.slack_remote })}…` : work === "tool" ? toolActivityLabel(action) : THINKING_LABEL;
  const unread = !!a.unread && !a.disabled && a.id !== opts.activeId;
  const attention = !!a.needs_attention && !a.disabled;
  let tip: string;
  if (a.disabled) tip = "disabled";
  else if (opts.hatching) tip = "just hatched";
  else if (attention) tip = "needs your attention";
  else if (typing !== null) tip = typing;
  else if (unread) tip = "new message";
  else tip = "online · idle";
  return { unread, attention, work, typing, tip };
}

/** withTurn is the roster after agent id's main-chat turn started or
    ended on the stream: the row reads working (or idle, with its tool
    cleared) at once instead of after the next poll. */
export function withTurn<T extends Pick<AgentItem, "id" | "status"> & Partial<Pick<AgentItem, "current_action" | "tool_error">>>(
  agents: T[],
  id: string,
  active: boolean,
): T[] {
  return agents.map((x) =>
    x.id === id ? { ...x, status: active ? "running" : "idle", current_action: active ? x.current_action : "", ...(!active && x.tool_error ? { tool_error: false } : {}) } : x,
  );
}

/** withActivity folds one /stream/sessions `activity` event into the
    roster: the agent whose main chat it is reads working (with the tool,
    its failure and whether it waits on a person) or idle. finished = a row
    went from working to idle, time for a full read (preview, unread). An
    event for no agent on the roster, or one that changes nothing, returns
    the same list so the roster does not re-render. */
export function withActivity<
  T extends Pick<AgentItem, "id" | "status" | "main_session_id"> & Partial<Pick<AgentItem, "current_action" | "needs_attention" | "tool_error">>,
>(agents: T[], ev: SessionActivity): { agents: T[]; finished: boolean } {
  if (!ev.session_id) return { agents, finished: false };
  let finished = false;
  let changed = false;
  const next = agents.map((x) => {
    if (x.main_session_id !== ev.session_id) return x;
    const working = ev.work !== "";
    const status = working ? "running" : isWorking(x.status) ? "idle" : x.status;
    const action = ev.work === "tool" ? ev.action ?? "" : "";
    const attention = !!ev.needs_attention;
    const failed = working && !!ev.tool_error;
    if (status === x.status && action === (x.current_action ?? "") && attention === !!x.needs_attention && failed === !!x.tool_error) return x;
    changed = true;
    if (isWorking(x.status) && !isWorking(status)) finished = true;
    return { ...x, status, current_action: action, needs_attention: attention, tool_error: failed };
  });
  return { agents: changed ? next : agents, finished };
}
