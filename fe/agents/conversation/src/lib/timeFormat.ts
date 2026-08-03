/* Shared WhatsApp-style time/date formatting for the conversation thread.
   `turnTime` → per-bubble clock ("15:36"). `turnDay` → centered separator
   label ("Today" / "Yesterday" / "Monday" / "08/06/2026"). `turnDayKey` →
   stable per-day key so the parent can decide when to insert a separator.

   Both read a turn's `ts` (RFC3339 string from history) first, falling back
   to `timestamp` (epoch ms set on client-built live turns). */
import type { ConversationTurn } from "./types/agents.js";

function turnDate(turn: ConversationTurn): Date | null {
  const raw = turn.ts ?? (turn.timestamp ? turn.timestamp : null);
  if (raw == null) return null;
  const d = new Date(raw);
  return isNaN(d.getTime()) ? null : d;
}

/** Parses an RFC3339 timestamp (a trace event's `at`/`end_at`) into epoch
    ms, or undefined for an absent/malformed/zero value. Go's zero
    time.Time marshals as "0001-01-01T00:00:00Z" (an omitted `end_at` on
    a still-running tool call, or a trace recorded before these fields
    existed) — Date parses that fine but it must not render as a real
    timestamp, so reject anything before 2000. */
export function parseEventTime(raw?: string): number | undefined {
  if (!raw) return undefined;
  const ms = Date.parse(raw);
  if (isNaN(ms) || ms < 946684800000) return undefined; // 946684800000 = 2000-01-01 UTC
  return ms;
}

const startOfDay = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate());

/** Short elapsed time: "just now", "4m", "3h", "2d".

    Coarse on purpose. These labels sit next to a status chip and a turn
    count, and "3h" answers "is this stale?" as well as "3h 12m" does
    while taking a third of the width. */
export function shortDuration(ms: number): string {
  if (!isFinite(ms) || ms < 0) return "";
  const s = Math.floor(ms / 1000);
  if (s < 45) return "just now";
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.round(h / 24)}d`;
}

/** "4m ago" / "just now" for an RFC3339 timestamp, "" when unparseable.

    Pass `nowMs` from the `now` store so the label keeps up with the
    clock; it defaults to Date.now() for callers that render once. */
export function timeAgo(raw?: string, nowMs: number = Date.now()): string {
  const ms = parseEventTime(raw);
  if (ms === undefined) return "";
  const d = shortDuration(nowMs - ms);
  return d === "just now" || d === "" ? d : `${d} ago`;
}

/** Full local timestamp for a `title` tooltip — the exact moment behind
    a rounded "4m ago". Empty when unparseable, so the caller can leave
    the attribute off rather than render an empty tooltip. */
export function exactTime(raw?: string): string {
  const ms = parseEventTime(raw);
  if (ms === undefined) return "";
  return new Date(ms).toLocaleString([], {
    weekday: "short",
    day: "2-digit",
    month: "short",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
}

export function turnTime(turn: ConversationTurn): string {
  const d = turnDate(turn);
  if (!d) return "";
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
}

/* Label of the day separator currently at the top of the viewport: the lowest
   separator still at/above `parentTop + offset`, else the first separator. */
export function activeDayLabel(
  separators: { top: number; label: string }[],
  parentTop: number,
  offset: number,
): string {
  if (separators.length === 0) return "";
  const limit = parentTop + offset;
  let active = "";
  for (const sep of separators) {
    if (sep.top <= limit) active = sep.label;
  }
  return active || separators[0].label;
}

/** Stable per-day key ("2026-06-18"); empty when the turn has no parseable time. */
export function turnDayKey(turn: ConversationTurn): string {
  const d = turnDate(turn);
  if (!d) return "";
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
}

/** Separator label: Today / Yesterday / weekday (last 6 days) / DD/MM/YYYY. */
export function turnDay(turn: ConversationTurn): string {
  const d = turnDate(turn);
  if (!d) return "";
  const dayDiff = Math.round((startOfDay(new Date()).getTime() - startOfDay(d).getTime()) / 86400000);
  if (dayDiff === 0) return "Today";
  if (dayDiff === 1) return "Yesterday";
  if (dayDiff > 1 && dayDiff < 7) return d.toLocaleDateString([], { weekday: "long" });
  return d.toLocaleDateString([], { day: "2-digit", month: "2-digit", year: "numeric" });
}
