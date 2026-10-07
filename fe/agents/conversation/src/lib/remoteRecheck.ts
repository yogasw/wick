import type { ConversationTurn } from "./types/agents.js";
import type { RemoteRecheck } from "./api/team.js";

/* The "Check again" of a remote turn: which turns offer it, how a re-read
   is worded, and how a reply that replaced a timed-out turn is shown in
   its place. No components: the tests import it. */

/** remote.TimeoutMessage's opening words. */
const TIMEOUT_PREFIX = "No reply from the remote agent";
/** remote.NoteNoMarker / remote.NoteLate / remote.NoteRechecked. */
export const NOTE_NO_MARKER = "ended without marker";
export const NOTE_LATE = "late reply";
export const NOTE_RECHECKED = "rechecked";

/** isRemoteTimeout: the error a remote turn ends with when nothing came. */
export function isRemoteTimeout(t: Pick<ConversationTurn, "is_error" | "text">): boolean {
  return !!t.is_error && (t.text ?? "").trimStart().startsWith(TIMEOUT_PREFIX);
}

/** canRecheck: a timed-out turn, or one the idle window closed without
    the remote's end marker. */
export function canRecheck(t: Pick<ConversationTurn, "is_error" | "text" | "role" | "remote_note">): boolean {
  return isRemoteTimeout(t) || (t.role === "assistant" && t.remote_note === NOTE_NO_MARKER);
}

/** isLateReply: a reply the remote finished after its turn timed out —
    arrived on its own, or read back by "Check again". */
export function isLateReply(t: Pick<ConversationTurn, "role" | "remote_note">): boolean {
  return t.role === "assistant" && (t.remote_note === NOTE_LATE || t.remote_note === NOTE_RECHECKED);
}

function turnMs(t: Pick<ConversationTurn, "ts" | "timestamp">): number {
  const ms = t.ts ? Date.parse(t.ts) : t.timestamp;
  return Number.isFinite(ms) ? ms : 0;
}

/** foldReplaced shows a reply that replaced a turn (`replaces`) where that
    turn was, instead of the turn itself; the replaced turn stays in the
    history for audit but is not rendered. With several replacements of
    one turn the latest wins. `late_ms` is how long after the replaced turn
    the reply was kept. */
export function foldReplaced<T extends Pick<ConversationTurn, "turn_id" | "replaces" | "ts" | "timestamp"> & { late_ms?: number }>(turns: T[]): T[] {
  const latest = new Map<string, T>();
  for (const t of turns) if (t.replaces) latest.set(t.replaces, t);
  if (latest.size === 0) return turns;
  const present = new Set(turns.map((t) => t.turn_id).filter(Boolean));
  const out: T[] = [];
  for (const t of turns) {
    if (t.replaces && present.has(t.replaces)) continue;
    const by = t.turn_id ? latest.get(t.turn_id) : undefined;
    if (by) {
      out.push({ ...by, late_ms: Math.max(0, turnMs(by) - turnMs(t)) });
      continue;
    }
    out.push(t);
  }
  return out;
}

/** lateLabel is the small line on a late reply: how it came, and how late. */
export function lateLabel(t: Pick<ConversationTurn, "remote_note"> & { late_ms?: number }): string {
  const how = t.remote_note === NOTE_RECHECKED ? "Rechecked" : "Late reply";
  if (!t.late_ms) return how;
  const min = Math.max(1, Math.round(t.late_ms / 60000));
  return `${how} · arrived ${min} min late`;
}

/** recheckToast is what a kept reply says: saved, and to whom it went. */
export function recheckToast(r: Pick<RemoteRecheck, "replaced" | "forwarded_to">): string {
  if (!r.replaced) return "";
  return r.forwarded_to ? `Reply saved · forwarded to @${r.forwarded_to}` : "Reply saved";
}

/** recheckNote is the line under the re-read reply: kept, still working,
    done, or nothing new compared with what the turn already shows. */
export function recheckNote(r: RemoteRecheck, shown: string): string {
  if (r.replaced) return recheckToast(r);
  if (r.busy) return r.label ? `The remote is still working — ${r.label}` : "The remote is still working…";
  if (!r.text.trim() || r.text.trim() === shown.trim()) return "No new reply yet.";
  return r.done ? "Latest reply from the remote." : "Latest reply from the remote (no end marker).";
}
