/* The thread's "Delegated to N teammates" block, the tray above the
   composer and the Tasks rail all read the same thing: the Team (A2A)
   tasks this chat sent (GET /api/sessions/{id}/team-tasks). This module
   holds the pure parts — which turn sent which task, what a task's status
   reads as, when a turn folds — so the components stay layout only. */
import type { ConversationTurn, TeamTaskItem, ThreadBlock, TurnEvent } from "./types/agents.js";

/** A team_message call, whatever namespace the provider puts on it
    (mcp__wick__team_message, team_message). */
export function isTeamMessageTool(name?: string): boolean {
  return !!name && /(^|__)team_message$/.test(name);
}

/** A team_message input that answers an existing task (task_id set)
    rather than sending a new one. */
function isTaskAnswerInput(input?: string): boolean {
  return /"task_id"\s*:\s*"[^"\s]/.test(input ?? "");
}

/** The task ids a turn's team_message calls returned, in call order.
    The result is the Hub's JSON (task_id, state, …), sometimes wrapped in
    an MCP text envelope, so the id is read from the first task_id field.
    A turn the thread committed from its live state (live-*) carries each
    result on the tool_use itself, so a team_message tool_use's text counts
    too: the block stays on that turn by id, not by guess.
    A call that ANSWERS a task (its input names a task_id) did not send
    it: that task stays with the turn that did. */
export function teamTaskIdsFromEvents(events: TurnEvent[] | null | undefined): string[] {
  if (!events) return [];
  const uses = events.filter((e) => e.type === "tool_use" && isTeamMessageTool(e.tool_name));
  const teamUses = new Set(uses.map((e) => e.tool_use_id ?? ""));
  const answers = new Set(uses.filter((e) => isTaskAnswerInput(e.tool_input)).map((e) => e.tool_use_id ?? ""));
  const out: string[] = [];
  for (const e of events) {
    if (e.tool_use_id && answers.has(e.tool_use_id)) continue;
    const isResult = e.type === "tool_result" && (teamUses.has(e.tool_use_id ?? "") || isTeamMessageTool(e.tool_name));
    if (!isResult && !(e.type === "tool_use" && isTeamMessageTool(e.tool_name))) continue;
    const m = /"task_id\\?"\s*:\s*\\?"([^"\\]+)/.exec(e.text ?? "");
    if (m && !out.includes(m[1])) out.push(m[1]);
  }
  return out;
}

/** The tasks the running (not yet committed) turn sent, in call order:
    its tool blocks read as the trace events they become, so a task shows
    in the live block as soon as its team_message call returns. A call
    answering an older task is not one of them (teamTaskIdsFromEvents). */
export function liveTurnTasks(liveBlocks: ThreadBlock[] | null | undefined, tasks: TeamTaskItem[]): TeamTaskItem[] {
  if (!liveBlocks || tasks.length === 0) return [];
  const events: TurnEvent[] = [];
  for (const b of liveBlocks) {
    if (b.kind !== "tool") continue;
    events.push({ type: "tool_use", tool_name: b.toolName, tool_input: b.toolInput, tool_use_id: b.toolUseId });
    if (b.result) events.push({ type: "tool_result", tool_name: b.toolName, tool_use_id: b.toolUseId, text: b.result });
  }
  const byId = new Map(tasks.map((t) => [t.task_id, t]));
  return teamTaskIdsFromEvents(events).flatMap((id) => byId.get(id) ?? []);
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

/** The tasks a running turn holds back from the committed turns: an
    agent's task started after the last committed turn, that no committed
    turn names as its own. Its team_message result may reach the stream
    after the team_task event, and until then tasksByTurn's fallback would
    hand it to the previous turn — the block would show there, then jump
    to the live one. The person's own tasks (origin "user") and a Retry
    or Reassign (retry_of set) keep their place. Ids in the order of
    tasks; read only while a turn is live. */
export function heldForLiveTurn(turns: ConversationTurn[], tasks: TeamTaskItem[]): string[] {
  if (tasks.length === 0) return [];
  const last = turns.at(-1);
  const since = last ? turnMs(last) : -Infinity;
  const claimed = new Set(turns.flatMap((t) => (t.role === "assistant" ? teamTaskIdsFromEvents(t.events) : [])));
  return tasks
    .filter((t) => !isUserTask(t) && !t.retry_of && !claimed.has(t.task_id) && Date.parse(t.started_at) > since)
    .map((t) => t.task_id);
}

export type TaskStatus = "needs_you" | "answering" | "working" | "replied" | "declined" | "failed" | "canceled";

/** A task the person sent with an @mention in this chat: theirs, not the
    chat agent's — only they answer its question. */
export function isUserTask(t: TeamTaskItem): boolean {
  return t.origin === "user";
}

/** "You → @handle" for the person's own task, as the tray and the Tasks
    tab name it; null for an agent's task (named by its teammate). */
export function userTaskRoute(t: TeamTaskItem): string | null {
  return isUserTask(t) ? `You → @${t.to_handle}` : null;
}

/** The marker on the person's own task, in place of the sending agent. */
export const USER_TASK_MARKER = "You asked";

/** How a task reads in the UI. input_required splits two ways: the
    sending agent is on it ("Captain is answering…", grey) or it waits for
    the person (needs_you, amber) — the server decides (needs_you). The
    person's own task never has an "answering" stage: its question is
    theirs at once. */
export function taskStatus(t: TeamTaskItem): TaskStatus {
  switch (t.state) {
    case "input_required":
      return t.needs_you || isUserTask(t) ? "needs_you" : "answering";
    case "completed":
      return "replied";
    case "rejected":
      return "declined";
    case "failed":
      return "failed";
    case "canceled":
      return "canceled";
  }
  return "working";
}

/** Most a progress line shows, in characters (the server caps it too). */
export const PROGRESS_MAX = 140;

/** taskProgress is the live progress line of a working task (mockup t3,
    `Checking Loki for the 21:00 window…`): one line of plain text, capped;
    "" for any task that is not working or says nothing yet. */
export function taskProgress(t: TeamTaskItem): string {
  if (taskStatus(t) !== "working") return "";
  const line = (t.progress ?? "").replace(/\s+/g, " ").trim();
  const chars = Array.from(line);
  return chars.length > PROGRESS_MAX ? chars.slice(0, PROGRESS_MAX - 1).join("") + "…" : line;
}

export function isTaskActive(t: TeamTaskItem): boolean {
  const s = taskStatus(t);
  return s === "working" || s === "answering" || s === "needs_you";
}

const STATUS_LABEL: Record<Exclude<TaskStatus, "answering">, string> = {
  needs_you: "needs you",
  working: "working",
  replied: "replied",
  declined: "declined",
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

/** The chip's status word: like taskLabel, but a question waiting for
    the person reads "needs input" (the Tasks tab keeps "needs you"). */
export function chipLabel(t: TeamTaskItem, senderName: string): string {
  return !t.interrupted && taskStatus(t) === "needs_you" ? "needs input" : taskLabel(t, senderName);
}

/** One part of the block's summary, its status kept so each part gets
    its own colour. "answering" counts as working. */
export type StatusPart = { status: Exclude<TaskStatus, "answering">; count: number; label: string };

const PART_ORDER: StatusPart["status"][] = ["working", "needs_you", "replied", "declined", "failed", "canceled"];
const PART_WORD: Record<StatusPart["status"], string> = { ...STATUS_LABEL, needs_you: "needs input" };

/** "2 working", "1 needs input", "1 replied", … in a fixed order, empty
    states left out. */
export function statusParts(tasks: TeamTaskItem[]): StatusPart[] {
  const counts = new Map<StatusPart["status"], number>();
  for (const t of tasks) {
    const s = taskStatus(t);
    const k = s === "answering" ? "working" : s;
    counts.set(k, (counts.get(k) ?? 0) + 1);
  }
  return PART_ORDER.filter((s) => counts.has(s)).map((s) => ({ status: s, count: counts.get(s)!, label: `${counts.get(s)} ${PART_WORD[s]}` }));
}

/** "2 working · 1 needs input · 1 replied": statusParts as one string. */
export function statusSummary(tasks: TeamTaskItem[]): string {
  return statusParts(tasks).map((p) => p.label).join(" · ");
}

const ms = (iso: string | undefined): number => (iso ? Date.parse(iso) : NaN);

/** When a task ended (ended_at) and when its question was asked
    (asked_at); updated_at stands in on a task recorded before the server
    kept them. */
const endedMs = (t: TeamTaskItem): number => ms(t.ended_at || t.updated_at);
const askedMs = (t: TeamTaskItem): number => ms(t.asked_at || t.updated_at);

/** "45s", "2m 10s", "1h 5m": a running timer. */
export function formatTimer(msec: number): string {
  const sec = Math.max(0, Math.floor(msec / 1000));
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ${String(sec % 60).padStart(2, "0")}s`;
  return `${Math.floor(sec / 3600)}h ${Math.floor((sec % 3600) / 60)}m`;
}

/** "13m", "1h 5m", "40s": how long a whole block took. */
export function formatDuration(msec: number): string {
  const sec = Math.max(0, Math.round(msec / 1000));
  if (sec < 60) return `${sec}s`;
  const min = Math.round(sec / 60);
  if (min < 60) return `${min}m`;
  return min % 60 === 0 ? `${min / 60}h` : `${Math.floor(min / 60)}h ${min % 60}m`;
}

/** "10:08" in the viewer's local time, "" when the time is unknown. */
export function clockTime(at: number): string {
  if (!Number.isFinite(at)) return "";
  const d = new Date(at);
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
}

/** "Oct 9" (or "Oct 9, 2025" outside the current year): the day a task
    ended, for one finished before today; "" when the time is unknown. */
export function endedDay(t: TeamTaskItem, now: number): string {
  const at = endedMs(t);
  if (!Number.isFinite(at)) return "";
  const d = new Date(at);
  const md = d.toLocaleDateString("en", { month: "short", day: "numeric" });
  return d.getFullYear() === new Date(now).getFullYear() ? md : `${md}, ${d.getFullYear()}`;
}

/** The chip's time: a running timer while the teammate works, how long
    a question has waited (the sender answering it or the person), the
    clock time a task ended. */
export function chipElapsed(t: TeamTaskItem, now: number): string {
  const s = taskStatus(t);
  if (s === "working") {
    const at = ms(t.started_at);
    return Number.isFinite(at) ? formatTimer(now - at) : "";
  }
  if (s === "needs_you" || s === "answering") {
    const at = askedMs(t);
    return Number.isFinite(at) ? formatTimer(now - at) : "";
  }
  return clockTime(endedMs(t));
}

/** One teammate in the folded line's avatar stack. */
export type FoldAvatar = { to_agent_id: string; to_handle: string; to_name: string };

/** The folded line's avatars: one per teammate, in the order first asked.
    A task without to_agent_id is told apart by its handle, then its name,
    so two such teammates never merge. */
export function foldAvatars(tasks: TeamTaskItem[]): FoldAvatar[] {
  const seen = new Set<string>();
  const out: FoldAvatar[] = [];
  for (const t of tasks) {
    const key = t.to_agent_id ? "id:" + t.to_agent_id : t.to_handle ? "h:" + t.to_handle : "n:" + t.to_name;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ to_agent_id: t.to_agent_id, to_handle: t.to_handle, to_name: t.to_name });
  }
  return out;
}

/** A block folds to one line once every task in it ended. */
export function allSettled(tasks: TeamTaskItem[]): boolean {
  return tasks.length > 0 && tasks.every((t) => !isTaskActive(t));
}

/** The block's head: "Delegated to N teammates" for the agent's tasks,
    "You asked N" for the person's own, both when the block mixes them. */
export function blockHead(tasks: TeamTaskItem[]): string {
  const plural = (n: number) => `${n} teammate${n === 1 ? "" : "s"}`;
  const yours = tasks.filter(isUserTask).length;
  const agents = tasks.length - yours;
  if (yours === 0) return `Delegated to ${plural(agents)}`;
  if (agents === 0) return `You asked ${plural(yours)}`;
  return `Delegated to ${plural(agents)} · You asked ${yours}`;
}

/** The folded line in pieces: the head, "all replied" or the state
    parts (a failure never hides behind a tick), "12 messages" and how
    long the block took (first start to last end, "13m"). Empty strings
    for what is unknown. */
export type SettledParts = { head: string; allReplied: boolean; parts: StatusPart[]; messages: string; duration: string };

export function settledParts(tasks: TeamTaskItem[]): SettledParts {
  const turns = tasks.reduce((s, t) => s + (t.message_count ?? t.turns ?? 0), 0);
  const starts = tasks.map((t) => ms(t.started_at)).filter(Number.isFinite);
  const ends = tasks.map(endedMs).filter(Number.isFinite);
  const span = starts.length && ends.length ? Math.max(...ends) - Math.min(...starts) : NaN;
  return {
    head: blockHead(tasks),
    allReplied: tasks.length > 0 && tasks.every((t) => t.state === "completed"),
    parts: statusParts(tasks),
    messages: turns > 0 ? `${turns} message${turns === 1 ? "" : "s"}` : "",
    duration: Number.isFinite(span) ? formatDuration(span) : "",
  };
}

/** The asst-meta line's "09:31 – 09:44" over a turn that delegated:
    from the first task's start to the last task's end or the turn's own
    commit time, whichever is later. While a task still runs the end is
    not known yet, so only the start shows ("10:02", grouped §3 live).
    One clock when both read the same, "" when nothing is known. */
export function turnSpan(tasks: TeamTaskItem[], committed: number = NaN): string {
  const starts = tasks.map((t) => ms(t.started_at)).filter(Number.isFinite);
  const start = clockTime(starts.length ? Math.min(...starts) : committed);
  if (!allSettled(tasks)) return start;
  const ends = [...tasks.map(endedMs), committed].filter(Number.isFinite);
  const end = clockTime(ends.length ? Math.max(...ends) : NaN);
  if (!start || !end || start === end) return start || end;
  return `${start} – ${end}`;
}

/** A teammate as the block names them: their agent's name in this
    team, else the name the task carries, else "@handle". */
export function teammateName(t: TeamTaskItem, agents: Record<string, { name?: string }> = {}): string {
  return agents[t.to_handle]?.name || t.to_name || "@" + t.to_handle;
}

/** The live turn's asst-meta status (grouped §3 live), fixed by how many
    tasks the turn sent, never by the prompt: one names the teammate
    ("asked Atraxa — …"), more count them ("split the question into 4
    tasks — …"). The line stays while any task is still out (working, or
    a question waiting on input) and is null once every one has settled,
    or when the turn sent none. */
export function liveTurnStatus(tasks: TeamTaskItem[], agents: Record<string, { name?: string }> = {}): string | null {
  if (tasks.length === 0 || allSettled(tasks)) return null;
  const wait = "summary comes when they're back";
  if (tasks.length === 1) return `asked ${teammateName(tasks[0], agents)} — ${wait}`;
  return `split the question into ${tasks.length} tasks — ${wait}`;
}

export type TaskGroup = {
  key: "needs_you" | "working" | "done" | "earlier" | "failed";
  label: string;
  tasks: TeamTaskItem[];
  /** Starts folded (Earlier). */
  collapsed?: boolean;
};

/** The Tasks rail's groups, empty ones dropped. A reply that came in
    before today (the viewer's local midnight) goes to a folded Earlier
    group, so nothing disappears; declined joins Failed · Canceled. */
export function groupTasks(tasks: TeamTaskItem[], now: number = Date.now()): TaskGroup[] {
  const groups: TaskGroup[] = [
    { key: "needs_you", label: "Needs input", tasks: [] },
    { key: "working", label: "Working", tasks: [] },
    { key: "done", label: "Done today", tasks: [] },
    { key: "earlier", label: "Earlier", tasks: [], collapsed: true },
    { key: "failed", label: "Failed · Canceled", tasks: [] },
  ];
  const midnight = new Date(now).setHours(0, 0, 0, 0);
  for (const t of tasks) {
    const s = taskStatus(t);
    const g =
      s === "needs_you" ? 0 : s === "working" || s === "answering" ? 1 : s === "replied" ? (endedMs(t) < midnight ? 3 : 2) : 4;
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

/** The Tasks rail tab's marks: the green badge counts the tasks still
    active, the amber dot shows while one waits for the person (needsInput
    says how many, for the tab's name), and the ring spins while a
    teammate is working on one. */
export function taskRailBadge(tasks: TeamTaskItem[]): { count: number; needsInput: number; working: boolean } {
  return {
    count: tasks.filter(isTaskActive).length,
    needsInput: tasks.filter((t) => taskStatus(t) === "needs_you").length,
    working: tasks.some((t) => taskStatus(t) === "working"),
  };
}
