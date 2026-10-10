/* The thread's "Delegated to N teammates" block, the tray above the
   composer and the Tasks rail all read the same thing: the Team (A2A)
   tasks this chat sent (GET /api/sessions/{id}/team-tasks). This module
   holds the pure parts — which turn sent which task, what a task's status
   reads as, when a turn folds — so the components stay layout only. */
import type { ConversationTurn, TeamTaskItem, TurnEvent } from "./types/agents.js";

/** A team_message call, whatever namespace the provider puts on it
    (mcp__wick__team_message, team_message). */
export function isTeamMessageTool(name?: string): boolean {
  return !!name && /(^|__)team_message$/.test(name);
}

/** The task ids a turn's team_message calls returned, in call order.
    The result is the Hub's JSON (task_id, state, …), sometimes wrapped in
    an MCP text envelope, so the id is read from the first task_id field. */
export function teamTaskIdsFromEvents(events: TurnEvent[] | null | undefined): string[] {
  if (!events) return [];
  const teamUses = new Set(
    events.filter((e) => e.type === "tool_use" && isTeamMessageTool(e.tool_name)).map((e) => e.tool_use_id ?? ""),
  );
  const out: string[] = [];
  for (const e of events) {
    if (e.type !== "tool_result" || !(teamUses.has(e.tool_use_id ?? "") || isTeamMessageTool(e.tool_name))) continue;
    const m = /"task_id\\?"\s*:\s*\\?"([^"\\]+)/.exec(e.text ?? "");
    if (m && !out.includes(m[1])) out.push(m[1]);
  }
  return out;
}

function turnMs(t: ConversationTurn): number {
  const parsed = t.ts ? Date.parse(t.ts) : NaN;
  return Number.isFinite(parsed) ? parsed : (t.timestamp ?? 0);
}

/** Which assistant turn sent which task. A turn whose trace is loaded
    names its tasks itself (teamTaskIdsFromEvents); any other task goes to
    the last assistant turn between the user turns around its start — a
    turn's trace is fetched lazily, the block must not wait for it. */
export function tasksByTurn(turns: ConversationTurn[], tasks: TeamTaskItem[]): Map<string, TeamTaskItem[]> {
  const out = new Map<string, TeamTaskItem[]>();
  if (tasks.length === 0) return out;
  const byId = new Map(tasks.map((t) => [t.task_id, t]));
  const placed = new Set<string>();
  const push = (turnId: string, t: TeamTaskItem) => {
    if (placed.has(t.task_id)) return;
    placed.add(t.task_id);
    out.set(turnId, [...(out.get(turnId) ?? []), t]);
  };
  for (const turn of turns) {
    if (turn.role !== "assistant") continue;
    for (const id of teamTaskIdsFromEvents(turn.events)) {
      const t = byId.get(id);
      if (t) push(turn.turn_id, t);
    }
  }
  // Segments: the assistant turns between two user turns.
  let segStart = -Infinity;
  let lastAssistant: string | null = null;
  const close = (segEnd: number) => {
    if (lastAssistant) {
      for (const t of tasks) {
        const at = Date.parse(t.started_at);
        if (!placed.has(t.task_id) && at >= segStart && at < segEnd) push(lastAssistant, t);
      }
    }
    lastAssistant = null;
    segStart = segEnd;
  };
  for (const turn of turns) {
    if (turn.role === "user") close(turnMs(turn));
    else if (turn.role === "assistant") lastAssistant = turn.turn_id;
  }
  close(Infinity);
  return out;
}

export type TaskStatus = "needs_you" | "answering" | "working" | "replied" | "failed" | "canceled";

/** How a task reads in the UI. input_required splits two ways: the
    sending agent is on it ("Captain is answering…", grey) or it waits for
    the person (needs_you, amber) — the server decides (needs_you). */
export function taskStatus(t: TeamTaskItem): TaskStatus {
  switch (t.state) {
    case "input_required":
      return t.needs_you ? "needs_you" : "answering";
    case "completed":
      return "replied";
    case "failed":
    case "rejected":
      return "failed";
    case "canceled":
      return "canceled";
  }
  return "working";
}

export function isTaskActive(t: TeamTaskItem): boolean {
  const s = taskStatus(t);
  return s === "working" || s === "answering" || s === "needs_you";
}

const STATUS_LABEL: Record<Exclude<TaskStatus, "answering">, string> = {
  needs_you: "needs you",
  working: "working",
  replied: "replied",
  failed: "failed",
  canceled: "canceled",
};

/** A task's status as the list reads it. "answering" names the agent
    that sent the task (the chat's agent, not always the Captain), the
    same name DelegationBlock shows. */
export function statusLabel(s: TaskStatus, senderName: string): string {
  return s === "answering" ? `${senderName} is answering` : STATUS_LABEL[s];
}

/** A task that failed because wick restarted under it, worded like the
    thread's "Recovered after a restart". */
export const INTERRUPTED_LABEL = "Interrupted by a restart";

/** statusLabel for one task: an interrupted one says so. */
export function taskLabel(t: TeamTaskItem, senderName: string): string {
  return t.interrupted ? INTERRUPTED_LABEL.toLowerCase() : statusLabel(taskStatus(t), senderName);
}

/** "2 working · 1 needs input · 1 replied", in a fixed order, empty
    states left out. */
export function statusSummary(tasks: TeamTaskItem[]): string {
  const counts = new Map<TaskStatus, number>();
  for (const t of tasks) counts.set(taskStatus(t), (counts.get(taskStatus(t)) ?? 0) + 1);
  const parts: string[] = [];
  const add = (n: number, label: string) => n > 0 && parts.push(`${n} ${label}`);
  add((counts.get("working") ?? 0) + (counts.get("answering") ?? 0), "working");
  add(counts.get("needs_you") ?? 0, "needs input");
  add(counts.get("replied") ?? 0, "replied");
  add(counts.get("failed") ?? 0, "failed");
  add(counts.get("canceled") ?? 0, "canceled");
  return parts.join(" · ");
}

/** A block folds to one line once every task in it ended. */
export function allSettled(tasks: TeamTaskItem[]): boolean {
  return tasks.length > 0 && tasks.every((t) => !isTaskActive(t));
}

/** The folded line: "all replied" when every task completed, else the
    state summary so a failure is never hidden behind a tick. */
export function settledLine(tasks: TeamTaskItem[]): string {
  const n = tasks.length;
  const head = `Delegated to ${n} teammate${n === 1 ? "" : "s"}`;
  const turns = tasks.reduce((s, t) => s + (t.turns || 0), 0);
  const tail = turns > 0 ? ` · ${turns} message${turns === 1 ? "" : "s"}` : "";
  const state = tasks.every((t) => t.state === "completed") ? "all replied" : statusSummary(tasks);
  return `${head} · ${state}${tail}`;
}

export type TaskGroup = { key: "needs_you" | "working" | "done" | "failed"; label: string; tasks: TeamTaskItem[] };

/** The Tasks rail's groups, empty ones dropped. */
export function groupTasks(tasks: TeamTaskItem[]): TaskGroup[] {
  const groups: TaskGroup[] = [
    { key: "needs_you", label: "Needs you", tasks: [] },
    { key: "working", label: "Working", tasks: [] },
    { key: "done", label: "Done", tasks: [] },
    { key: "failed", label: "Failed · Canceled", tasks: [] },
  ];
  for (const t of tasks) {
    const s = taskStatus(t);
    const g = s === "needs_you" ? 0 : s === "working" || s === "answering" ? 1 : s === "replied" ? 2 : 3;
    groups[g].tasks.push(t);
  }
  return groups.filter((g) => g.tasks.length > 0);
}

/** Quick answers offered under a question: a trailing "(a / b / c)" or
    "a or b?" list of short options, else none. */
export function quickReplies(question: string): string[] {
  const q = (question ?? "").trim();
  const paren = /\(([^()]{1,80})\)\s*\??\s*$/.exec(q);
  const list = paren ? paren[1] : (/:\s*([^:?]{1,80})\?\s*$/.exec(q)?.[1] ?? "");
  if (!list) return [];
  const opts = list.split(/\s*(?:\/|,|\bor\b|\|)\s*/i).map((s) => s.trim()).filter(Boolean);
  return opts.length >= 2 && opts.length <= 4 && opts.every((o) => o.length <= 24) ? opts : [];
}

/** The Tasks rail tab's badge: how many tasks are still active, and
    whether one waits for the person (the badge turns amber). */
export function taskRailBadge(tasks: TeamTaskItem[]): { count: number; needsYou: boolean } {
  return { count: tasks.filter(isTaskActive).length, needsYou: tasks.some((t) => taskStatus(t) === "needs_you") };
}
