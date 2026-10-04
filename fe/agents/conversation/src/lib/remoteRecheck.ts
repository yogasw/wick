import type { ConversationTurn } from "./types/agents.js";
import type { SlackRecheck } from "./api/team.js";

/* The "Cek ulang" of a Slack remote turn: which turns offer it, and how a
   re-read of the thread is worded. No components: the tests import it. */

/** remote.TimeoutMessage's opening words. */
const TIMEOUT_PREFIX = "No reply from the remote agent";
/** remote.NoteNoMarker / remote.NoteLate. */
export const NOTE_NO_MARKER = "ended without marker";
export const NOTE_LATE = "late reply";

/** isRemoteTimeout: the error a remote turn ends with when nothing came. */
export function isRemoteTimeout(t: Pick<ConversationTurn, "is_error" | "text">): boolean {
  return !!t.is_error && (t.text ?? "").trimStart().startsWith(TIMEOUT_PREFIX);
}

/** canRecheck: a timed-out turn, or one the idle window closed without
    the remote's end marker. */
export function canRecheck(t: Pick<ConversationTurn, "is_error" | "text" | "role" | "remote_note">): boolean {
  return isRemoteTimeout(t) || (t.role === "assistant" && t.remote_note === NOTE_NO_MARKER);
}

/** isLateReply: a reply the remote finished after its turn timed out. */
export function isLateReply(t: Pick<ConversationTurn, "role" | "remote_note">): boolean {
  return t.role === "assistant" && t.remote_note === NOTE_LATE;
}

/** recheckNote is the line under the re-read reply: still working, done,
    or nothing new compared with what the turn already shows. */
export function recheckNote(r: SlackRecheck, shown: string): string {
  if (r.busy) return r.label ? `Remote masih mengerjakan — ${r.label}` : "Remote masih mengerjakan…";
  if (!r.text.trim() || r.text.trim() === shown.trim()) return "Belum ada balasan baru.";
  return r.done ? "Balasan terbaru dari Slack." : "Balasan terbaru dari Slack (tanpa penanda selesai).";
}
