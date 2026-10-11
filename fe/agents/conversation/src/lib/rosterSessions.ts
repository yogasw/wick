import type { AgentItem, WorkingSession } from "./api/team.js";
import type { SessionActivity } from "./stores/sessionsStream.js";

/* An agent's chats other than its main chat on its roster row (mockup
   roster: "working on Captain's task, not its main session"): which chat
   a click opens, the count badge and its popover, and the subtle
   "Last: …" line of the newest that finished. Pure, so the order and
   when each cue shows are tested rather than buried in the template. */

type Row = Pick<AgentItem, "disabled"> & Partial<Pick<AgentItem, "working_sessions" | "last_session">>;

/** runningSessions is the row's running non-main chats, oldest turn
    first as the server sends them; none for a disabled agent. */
export function runningSessions(a: Row): WorkingSession[] {
  return a.disabled ? [] : (a.working_sessions ?? []);
}

/** rowTarget is the chat a click on the row opens: the oldest still
    running non-main chat (stable — a newer one never steals the click),
    or null for the pinned main chat. */
export function rowTarget(a: Row): string | null {
  return runningSessions(a)[0]?.id ?? null;
}

/** sessionTitle is a chat's name in the popover and the Last line. */
export function sessionTitle(s: Pick<WorkingSession, "label">): string {
  return s.label.trim() || "Untitled chat";
}

/** triggerLabel is who a chat works for, as the popover says it: "You",
    the sending agent, or where it came from ("Slack", "A2A"); "" when
    none is known. */
export function triggerLabel(s: Pick<WorkingSession, "trigger" | "source">): string {
  if (s.trigger === "you") return "You";
  return s.trigger || s.source || "";
}

/** elapsedLabel is how long a turn has run: "<1m", "4m", "1h 5m". */
export function elapsedLabel(since: string | undefined, now: number): string {
  const at = since ? Date.parse(since) : NaN;
  if (Number.isNaN(at)) return "";
  const min = Math.max(0, Math.floor((now - at) / 60000));
  if (min < 1) return "<1m";
  if (min < 60) return `${min}m`;
  return `${Math.floor(min / 60)}h ${min % 60}m`;
}

/** lastSessionKey names one finish of a chat: a later turn in the same
    chat finishing again shows the line again. */
export function lastSessionKey(s: WorkingSession): string {
  return `${s.id}@${s.since ?? ""}`;
}

/** lastSession is the finished chat the row's "Last: …" line points to:
    only while nothing runs elsewhere, until it is opened (seen) — the chat
    on screen counts as opened. */
export function lastSession(a: Row, seen: ReadonlySet<string>, openSession: string | null): WorkingSession | null {
  const s = a.last_session;
  if (!s || a.disabled || runningSessions(a).length > 0) return null;
  if (s.id === openSession || seen.has(lastSessionKey(s))) return null;
  return s;
}

/** SEEN_KEY keeps the opened Last lines across reloads; SEEN_MAX bounds it. */
export const SEEN_KEY = "wick.agents.lastSessionSeen";
export const SEEN_MAX = 50;

/** loadSeen reads the opened Last lines; an unreadable store is empty. */
export function loadSeen(store: Pick<Storage, "getItem"> | undefined = globalThis.localStorage): Set<string> {
  try {
    const raw = JSON.parse(store?.getItem(SEEN_KEY) ?? "[]");
    return new Set(Array.isArray(raw) ? raw.filter((x): x is string => typeof x === "string") : []);
  } catch {
    return new Set();
  }
}

/** markSeen adds key, keeping the newest SEEN_MAX, and stores it. */
export function markSeen(seen: ReadonlySet<string>, key: string, store: Pick<Storage, "setItem"> | undefined = globalThis.localStorage): Set<string> {
  const next = [...seen].filter((k) => k !== key);
  next.push(key);
  const kept = new Set(next.slice(-SEEN_MAX));
  try {
    store?.setItem(SEEN_KEY, JSON.stringify([...kept]));
  } catch {
    /* private mode: the line just shows again after a reload */
  }
  return kept;
}

/** sessionSignal says whether one /stream/sessions `activity` event calls
    for a roster read (the caller debounces it): a turn starting or ending
    in one of the viewer's non-main chats with an agent on the roster —
    the server names that agent (agent_id) and leaves it off every other
    conversation (main chats, group sessions, other people's chats), so
    those never read the roster. Its start makes the chat working, its
    end hands the click on and leaves the Last line. active holds the
    chats seen mid-turn; a chat already on a row as working that ends
    counts too (the page loaded mid-turn). Steps inside a turn do not. */
export function sessionSignal(
  agents: Pick<AgentItem, "id" | "main_session_id" | "working_sessions">[],
  ev: SessionActivity,
  active: Set<string>,
): boolean {
  const id = ev.session_id;
  if (!id || !ev.agent_id) return false;
  const agent = agents.find((a) => a.id === ev.agent_id);
  if (!agent || agent.main_session_id === id) return false;
  if (ev.work !== "") {
    if (active.has(id)) return false;
    active.add(id);
    return true;
  }
  if (active.delete(id)) return true;
  return (agent.working_sessions ?? []).some((s) => s.id === id);
}
