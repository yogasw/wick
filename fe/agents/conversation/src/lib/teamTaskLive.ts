/* Keeping a chat's team tasks current. The server pushes a `team_task`
   event to the sending chat's stream whenever one of its tasks changes;
   this module holds the pure parts — folding that event into the list and
   deciding when the fallback poll runs — so DetailView stays wiring. */
import type { SSEStatus, TeamTaskItem } from "./types/agents.js";

/** How often the list is re-read while the stream is down. */
export const TEAM_TASK_FALLBACK_POLL_MS = 12_000;

/** The task a `team_task` event carries, or null when it is not one. */
export function parseTeamTaskEvent(data: string | undefined): TeamTaskItem | null {
  try {
    const t = JSON.parse(data ?? "") as TeamTaskItem | null;
    return t && typeof t.task_id === "string" && t.task_id !== "" ? t : null;
  } catch {
    return null;
  }
}

function ms(ts: string | undefined): number {
  const n = ts ? Date.parse(ts) : NaN;
  return Number.isFinite(n) ? n : 0;
}

/** tasks with t folded in: replaces the task of the same id, or adds it,
    newest sent first like the list endpoint. A copy older than the one
    held (a late refetch beat it) is ignored. */
export function patchTeamTask(tasks: TeamTaskItem[], t: TeamTaskItem): TeamTaskItem[] {
  const i = tasks.findIndex((x) => x.task_id === t.task_id);
  if (i >= 0) {
    if (ms(t.updated_at) < ms(tasks[i].updated_at)) return tasks;
    const out = tasks.slice();
    out[i] = t;
    return out;
  }
  return [...tasks, t].sort((a, b) => ms(b.started_at) - ms(a.started_at));
}

/** The fallback poll's interval: only while the stream is not connected
    (events are missed then), null otherwise. */
export function teamTaskPollMs(status: SSEStatus, enabled: boolean): number | null {
  return enabled && status !== "connected" ? TEAM_TASK_FALLBACK_POLL_MS : null;
}

/** Whether a stream status change is a reconnect: back to connected
    after it was down, so the events sent meanwhile need one refetch. */
export function isReconnect(prev: SSEStatus, next: SSEStatus): boolean {
  return next === "connected" && prev === "error";
}
