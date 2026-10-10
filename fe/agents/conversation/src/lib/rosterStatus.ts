import { isWorking, type AgentItem } from "./api/team.js";
import type { SessionActivity } from "./stores/sessionsStream.js";
import { THINKING_LABEL, toolActivityLabel } from "./activityLabel.js";
import { isRemoteAgent, remoteWaitTarget } from "./remoteAgent.js";

/* What a roster row says about an agent (mockup row() / stateOf()): the
   red unread dot, the working line and the presence dot with its spoken
   label. Pure, so the order
   the cues win in is tested rather than buried in the template. The Team
   header reads the same answer, so the two never disagree. */

/** What a turn is doing: idle (no turn), thinking (a turn with no tool in
    flight), tool (waiting on a tool_use's result), waiting (a remote
    agent: wick waits for the other side) or subagent (no turn of its own,
    but background sub-agents it delegated to are still working). */
export type WorkState = "idle" | "thinking" | "tool" | "waiting" | "subagent";

/** The status dot on the avatar: green = online (idle), a spinner =
    working (its own turn or its sub-agents), amber = needs the owner,
    grey = disabled. */
export type Presence = "online" | "working" | "attention" | "disabled";

export type RosterStatus = {
  /** Red dot on the avatar: something new since the chat was last open. */
  unread: boolean;
  /** Waiting on the owner (ask_user / approval): the avatar's alert pose. */
  attention: boolean;
  work: WorkState;
  /** Preview line while a turn runs: "thinking…", the tool's label
      ("running Bash…"), what a remote waits on, or "🤖 <handle> bekerja…"
      while only its sub-agents work. null = not working, show the normal
      preview. */
  typing: string | null;
  presence: Presence;
  /** What the dot means in words, for screen readers (sr-only / aria):
      there is no visible tooltip. */
  label: string;
};

type Row = Pick<AgentItem, "id" | "status" | "disabled"> &
  Partial<Pick<AgentItem, "unread" | "needs_attention" | "current_action" | "kind" | "handle" | "slack_remote" | "subagents_working">>;

/** subagentLabel is the working line for background sub-agents: the one
    handle, or how many when there are more. */
export function subagentLabel(handles: string[]): string {
  return handles.length === 1 ? `🤖 ${handles[0]} bekerja…` : `🤖 ${handles.length} sub-agent bekerja…`;
}

/** rosterStatus reads one row. activeId is the agent whose chat is on
    screen: what it says is being read as it arrives, so it never shows
    unread. */
export function rosterStatus(a: Row, opts: { activeId?: string; hatching?: boolean } = {}): RosterStatus {
  const working = isWorking(a.status) && !a.disabled;
  const action = (a.current_action ?? "").trim();
  // The agent's own turn outranks its sub-agents' work.
  const subs = a.disabled ? [] : (a.subagents_working ?? []).filter((h) => h.trim() !== "");
  const work: WorkState = working ? (isRemoteAgent(a) ? "waiting" : action ? "tool" : "thinking") : subs.length > 0 ? "subagent" : "idle";
  const typing =
    work === "idle"
      ? null
      : work === "waiting"
        ? `${remoteWaitTarget({ handle: a.handle ?? "", slack_remote: a.slack_remote })}…`
        : work === "tool"
          ? toolActivityLabel(action)
          : work === "subagent"
            ? subagentLabel(subs)
            : THINKING_LABEL;
  const unread = !!a.unread && !a.disabled && a.id !== opts.activeId;
  const attention = !!a.needs_attention && !a.disabled;
  const presence: Presence = a.disabled ? "disabled" : attention ? "attention" : work !== "idle" ? "working" : "online";
  let label: string;
  if (a.disabled) label = "disabled";
  else if (opts.hatching) label = "just hatched";
  else if (attention) label = "needs your attention";
  else if (typing !== null) label = typing;
  else if (unread) label = "new message";
  else label = "online, idle";
  return { unread, attention, work, typing, presence, label };
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
    its failure and whether it waits on a person) or idle. subagents_working
    rides along untouched: a turn ending does not end its sub-agents. finished = a row
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
