/* A turn's trace carries its hand-offs to teammates as the server merges
   them in at read time (internal/tools/agents/team_trace.go): a team_task
   event per task, tied to the team_message call that sent it
   (tool_use_id), and a team_handoff event before the task an @handle line
   sent with no call. This reads them back, so the trace shows each call
   with one nested row per teammate and its thread. Pure. */
import type { TeamTaskItem, ThreadBlock, TurnEvent } from "./types/agents.js";
import { formatTimer, taskStatus, teamTaskIdsFromEvents, type TaskStatus } from "./delegations.js";

export type TeamTraceEntry = { role: string; handle?: string; name?: string; text: string; at?: string };

export type TeamTraceTask = {
  task_id: string;
  via: "tool" | "mention";
  to_handle: string;
  to_name?: string;
  title?: string;
  state: string;
  started_at?: string;
  ended_at?: string;
  entries?: TeamTraceEntry[];
};

export type TeamTrace = {
  /** team_message tool_use_id → the tasks that call sent, in order. */
  byUse: Map<string, TeamTraceTask[]>;
  /** Tasks an @handle line sent (team_handoff), in order. */
  mentions: TeamTraceTask[];
};

function parse(text: string | undefined): TeamTraceTask | null {
  try {
    const v = JSON.parse(text ?? "") as TeamTraceTask;
    return v && typeof v.task_id === "string" && v.task_id ? v : null;
  } catch {
    return null;
  }
}

/** teamTraceOf groups a trace's team_task events by the call that sent
    them; a mention task goes to mentions. Other events are ignored. */
export function teamTraceOf(events: TurnEvent[] | null | undefined): TeamTrace {
  const out: TeamTrace = { byUse: new Map(), mentions: [] };
  for (const e of events ?? []) {
    if (e.type !== "team_task") continue;
    const t = parse(e.text);
    if (!t) continue;
    if (t.via === "mention" || !e.tool_use_id) {
      out.mentions.push(t);
      continue;
    }
    out.byUse.set(e.tool_use_id, [...(out.byUse.get(e.tool_use_id) ?? []), t]);
  }
  return out;
}

/** A trace task's status, by the same rules as the block's chips. */
export function traceStatus(t: TeamTraceTask): TaskStatus {
  return taskStatus({ state: t.state } as TeamTaskItem);
}

/** How long the task ran: start to end, or to now while it works; "" when
    the start is unknown. */
export function traceDuration(t: TeamTraceTask, now: number = Date.now()): string {
  const start = t.started_at ? Date.parse(t.started_at) : NaN;
  if (!Number.isFinite(start)) return "";
  const end = t.ended_at ? Date.parse(t.ended_at) : NaN;
  return formatTimer((Number.isFinite(end) ? end : now) - start);
}

/** The row's one line: the newest thread entry the teammate wrote, else
    the task's title. */
export function traceLine(t: TeamTraceTask): string {
  const mine = (t.entries ?? []).filter((e) => e.role !== "user");
  const last = mine.at(-1)?.text ?? t.title ?? "";
  return last.replace(/\s+/g, " ").trim();
}

/** withLive lays the task's live state (the Team tasks stream) over the
    trace's snapshot: state, end and the newest line move with the task,
    the recorded thread stays. No live item = the snapshot as is. */
export function withLive(t: TeamTraceTask, live: TeamTaskItem | undefined): TeamTraceTask {
  if (!live) return t;
  const line = (live.progress || live.reply || live.summary || "").trim();
  const entries = line && traceLine(t) !== line.replace(/\s+/g, " ") ? [...(t.entries ?? []), { role: "agent", handle: live.to_handle, text: line, at: live.updated_at }] : t.entries;
  return { ...t, state: live.state, ended_at: live.ended_at ?? t.ended_at, entries };
}

/** traceTaskOf is a live task as a trace row, for a turn still running
    (its trace is not stored yet): no thread, the line from the task. */
export function traceTaskOf(t: TeamTaskItem): TeamTraceTask {
  const line = (t.progress || t.reply || t.summary || "").trim();
  return {
    task_id: t.task_id, via: "tool", to_handle: t.to_handle, to_name: t.to_name, title: t.title, state: t.state,
    started_at: t.started_at, ended_at: t.ended_at, entries: line ? [{ role: "agent", handle: t.to_handle, text: line, at: t.updated_at }] : [],
  };
}

/** liveBlockTasks is the rows of a running turn's team_message block:
    the tasks its result names, read from the live task list. */
export function liveBlockTasks(b: ThreadBlock, byId: Map<string, TeamTaskItem>): TeamTraceTask[] {
  if (b.kind !== "tool") return [];
  const events = [{ type: "tool_use", tool_name: b.toolName, tool_input: b.toolInput, tool_use_id: b.toolUseId }, ...(b.result ? [{ type: "tool_result", tool_name: b.toolName, tool_use_id: b.toolUseId, text: b.result }] : [])];
  return teamTaskIdsFromEvents(events).flatMap((id) => (byId.has(id) ? [traceTaskOf(byId.get(id)!)] : []));
}

/** True while any row's task can still change: the trace is worth
    reading again (its thread grows). */
export function traceActive(tasks: TeamTraceTask[]): boolean {
  return tasks.some((t) => {
    const s = traceStatus(t);
    return s === "working" || s === "answering" || s === "needs_you";
  });
}
