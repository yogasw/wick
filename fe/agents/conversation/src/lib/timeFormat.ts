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

const startOfDay = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate());

export function turnTime(turn: ConversationTurn): string {
  const d = turnDate(turn);
  if (!d) return "";
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
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
