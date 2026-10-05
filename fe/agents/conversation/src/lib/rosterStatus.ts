import { isWorking, type AgentItem, type AgentLive } from "./api/team.js";
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

/** liveIds are the agents the quick poll follows: the ones working now. */
export function liveIds(agents: Pick<AgentItem, "id" | "status" | "disabled">[]): string[] {
  return agents.filter((a) => isWorking(a.status) && !a.disabled).map((a) => a.id);
}

/** withLive folds a quick poll's rows into the roster. keepAction is the
    agent whose open chat reports its tool off the stream (fresher than the
    poll), so only its status is taken. finished = a row went from working
    to idle, time for a full read (preview, unread). The same list comes
    back when nothing moved, so the roster does not re-render. */
export function withLive<T extends Pick<AgentItem, "id" | "status"> & Partial<Pick<AgentItem, "current_action" | "needs_attention" | "tool_error">>>(
  agents: T[],
  rows: AgentLive[],
  keepAction = "",
): { agents: T[]; finished: boolean } {
  const by = new Map(rows.map((r) => [r.id, r]));
  let changed = false;
  let finished = false;
  const next = agents.map((x) => {
    const r = by.get(x.id);
    if (!r) return x;
    const action = r.status === "idle" ? "" : x.id === keepAction ? (x.current_action ?? "") : r.current_action ?? "";
    const attention = !!r.needs_attention;
    const failed = r.status !== "idle" && !!r.tool_error;
    if (r.status === x.status && action === (x.current_action ?? "") && attention === !!x.needs_attention && failed === !!x.tool_error) return x;
    changed = true;
    if (isWorking(x.status) && !isWorking(r.status)) finished = true;
    return { ...x, status: r.status, current_action: action, needs_attention: attention, tool_error: failed };
  });
  return { agents: changed ? next : agents, finished };
}

/** LIVE_POLL_MS is how often the quick poll runs while an agent works:
    a tool call shows on the roster within about this long. */
export const LIVE_POLL_MS = 2000;

/** createLivePoll runs tick every interval while there is someone to
    follow (update with a non-empty list) and the tab is visible; an empty
    list stops the timer. tick gets the ids of the moment and is never run
    twice at once. */
export function createLivePoll(o: {
  tick: (ids: string[]) => Promise<void>;
  visible?: () => boolean;
  interval?: number;
}): { update: (ids: string[]) => void; stop: () => void; running: () => boolean } {
  let ids: string[] = [];
  let timer: ReturnType<typeof setInterval> | undefined;
  let busy = false;
  const visible = o.visible ?? (() => typeof document === "undefined" || document.visibilityState === "visible");
  const stop = () => {
    if (timer !== undefined) clearInterval(timer);
    timer = undefined;
  };
  return {
    update(next) {
      ids = next;
      if (!ids.length) return stop();
      timer ??= setInterval(() => {
        if (busy || !ids.length || !visible()) return;
        busy = true;
        o.tick(ids).catch(() => {}).finally(() => (busy = false));
      }, o.interval ?? LIVE_POLL_MS);
    },
    stop,
    running: () => timer !== undefined,
  };
}
