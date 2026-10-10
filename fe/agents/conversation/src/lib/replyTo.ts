/*
 * Purpose:    Client side of a web "Reply": the chip's preview text and the
 *             target a bubble's Reply action hands to the composer.
 * Caller:     ThreadMessage (Reply action, quote preview), DetailView (chip)
 * Main Functions: replyPreview, replyable
 * Side Effects: none
 *
 * Display only. The server re-reads the quoted turn and builds the stored
 * author/excerpt itself; nothing here is sent except the turn id.
 */
import type { ConversationTurn } from "./types/agents.js";

/** One-line preview length for the chip and the quote above a bubble. */
export const REPLY_PREVIEW_MAX = 120;

export type ReplyTarget = { turnId: string; author: string; excerpt: string };

/** Collapses whitespace and clips to REPLY_PREVIEW_MAX characters. */
export function replyPreview(text: string | undefined, max = REPLY_PREVIEW_MAX): string {
  const one = (text ?? "").replace(/\s+/g, " ").trim();
  const chars = Array.from(one);
  return chars.length <= max ? one : chars.slice(0, max - 1).join("").trimEnd() + "…";
}

/** Ids the server gives turns: a stored UnixNano, or the position id
    (`turn-<n>`) older turns are numbered with. Everything else (live-,
    local-user-, remote-user-, postback-, …) exists only in this tab. */
const SERVER_TURN_ID = /^(\d+|turn-\d+)$/;

/** A turn can be replied to once the server knows it: a server id (a
    streaming or optimistic bubble has none yet) and something to quote. */
export function replyable(turn: ConversationTurn): boolean {
  if (!SERVER_TURN_ID.test(turn.turn_id ?? "")) return false;
  if (turn.role !== "user" && turn.role !== "assistant") return false;
  return !!(turn.text?.trim() || turn.postback || (turn.attachments?.length ?? 0) > 0);
}
