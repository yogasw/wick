/*
 * Purpose:    Web side of the Slack "[react :name:]" reply marker: find it on
 *             a reply's first line, drop it from the shown text, and name the
 *             emoji it put on the message that woke the turn.
 * Caller:     ThreadMessage (shown text, explicit-ts chip),
 *             ConversationThread (reaction chips on the waking user turn)
 * Main Functions: splitReactMarker, emojiGlyph, reactionsByTurn
 * Side Effects: none
 *
 * Mirrors reactReplyRe in internal/agents/channels/slack/react_reply.go.
 */
import type { ConversationTurn } from "./types/agents.js";

const REACT_LINE = /^\[react[:\s]\s*:?([a-z0-9_+'-]+(?:::skin-tone-[2-6])?):?(?:\s+(\d+\.\d+))?\s*\]$/;

export type ReactMarker = { name: string; ts: string; rest: string };

/** The marker on text's first line, or null when it does not open with one. */
export function splitReactMarker(text: string | undefined): ReactMarker | null {
  const t = (text ?? "").trim();
  const nl = t.indexOf("\n");
  const first = nl < 0 ? t : t.slice(0, nl);
  const m = REACT_LINE.exec(first.trim().toLowerCase());
  if (!m) return null;
  return { name: m[1], ts: m[2] ?? "", rest: nl < 0 ? "" : t.slice(nl + 1).trim() };
}

/* The emoji agents reach for; anything else (a workspace's custom emoji)
   stays its :name:. */
const GLYPHS: Record<string, string> = {
  pray: "🙏", "+1": "👍", thumbsup: "👍", ok_hand: "👌", heart: "❤️", eyes: "👀",
  white_check_mark: "✅", heavy_check_mark: "✔️", raised_hands: "🙌", tada: "🎉",
  clap: "👏", muscle: "💪", saluting_face: "🫡", smile: "😄", slightly_smiling_face: "🙂",
  rocket: "🚀", fire: "🔥", ok: "🆗", x: "❌", wave: "👋", handshake: "🤝",
};

/** The glyph for a Slack emoji name, or ":name:" when unknown. */
export function emojiGlyph(name: string): string {
  return GLYPHS[name.replace(/::skin-tone-[2-6]$/, "")] ?? `:${name}:`;
}

/** Per turn index, the emoji later replies put on it: a marker without an
    explicit ts lands on the nearest user turn before the reply, as Slack
    puts it on the message that woke the turn. */
export function reactionsByTurn(turns: ConversationTurn[]): string[][] {
  const out: string[][] = turns.map(() => []);
  turns.forEach((t, i) => {
    if (t.role !== "assistant") return;
    const m = splitReactMarker(t.text);
    if (!m || m.ts) return;
    for (let j = i - 1; j >= 0; j--) {
      if (turns[j].role === "user") {
        if (!out[j].includes(m.name)) out[j].push(m.name);
        return;
      }
    }
  });
  return out;
}
