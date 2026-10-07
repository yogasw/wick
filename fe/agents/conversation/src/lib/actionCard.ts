/* The `actioncard` fence an agent writes into a reply: parsed into a live
   card with buttons. The server validates the same schema and computes, per
   card id, which turn holds the newest version and whether it is locked
   (`cards` on the conversation response) — this only splits the text and
   reads that state. Anything that is not a valid card stays text, so a broken
   fence renders as the code block it looks like instead of crashing. */

import type { CardState } from "./types/agents.js";

export type CardAction = { label: string; value: string; style?: "primary" | "ghost" | string };

export type ActionCardData = {
  id: string;
  icon?: string;
  title?: string;
  subtitle?: string;
  status?: string;
  rows?: [string, string][];
  actions?: CardAction[];
  final?: boolean;
};

export type CardSegment = { kind: "md"; text: string } | { kind: "card"; card: ActionCardData; raw: string };

const FENCE = /^```actioncard[ \t]*\n([\s\S]*?)\n```[ \t]*$/gm;

/** parseActionCard reads one fence body; null when it is not a card. */
export function parseActionCard(body: string): ActionCardData | null {
  let v: unknown;
  try { v = JSON.parse(body); } catch (_) { return null; }
  if (!v || typeof v !== "object" || Array.isArray(v)) return null;
  const o = v as Record<string, unknown>;
  if (typeof o.id !== "string" || !o.id.trim()) return null;
  const str = (x: unknown) => (typeof x === "string" ? x : undefined);
  const rows = Array.isArray(o.rows)
    ? (o.rows as unknown[]).filter((r): r is unknown[] => Array.isArray(r) && r.length >= 2).map((r) => [String(r[0]), String(r[1])] as [string, string])
    : [];
  const actions = Array.isArray(o.actions)
    ? (o.actions as unknown[])
        .filter((a): a is Record<string, unknown> => !!a && typeof a === "object" && typeof (a as Record<string, unknown>).value === "string")
        .map((a) => ({ label: str(a.label) || String(a.value), value: String(a.value), style: str(a.style) }))
    : [];
  return { id: o.id, icon: str(o.icon), title: str(o.title), subtitle: str(o.subtitle), status: str(o.status), rows, actions, final: o.final === true };
}

/** splitActionCards cuts text into markdown runs and cards, in order. A fence
    that does not parse stays inside the markdown run. */
export function splitActionCards(text: string): CardSegment[] {
  const out: CardSegment[] = [];
  let last = 0;
  for (const m of (text ?? "").matchAll(FENCE)) {
    const card = parseActionCard(m[1]);
    if (!card) continue;
    const before = text.slice(last, m.index);
    if (before.trim()) out.push({ kind: "md", text: before });
    out.push({ kind: "card", card, raw: m[0] });
    last = (m.index ?? 0) + m[0].length;
  }
  const rest = (text ?? "").slice(last);
  if (rest.trim() || out.length === 0) out.push({ kind: "md", text: rest });
  return out;
}

export type CardMode = "active" | "locked" | "superseded" | "none";

/** cardMode says how the card with id in turn turnId draws: no entry in the
    server's state = not a card (render as text); a newer version elsewhere =
    superseded; otherwise live, locked once clicked or final. */
export function cardMode(id: string, turnId: string, cards: Record<string, CardState> | undefined): CardMode {
  const st = cards?.[id];
  if (!st) return "none";
  if (st.turn_id !== turnId) return "superseded";
  return st.locked ? "locked" : "active";
}

/** lockCard marks a card clicked, as the postback event or a successful POST
    reports it, without waiting for the next reload. */
export function lockCard(cards: Record<string, CardState>, pb: { card_id: string; value: string; label: string }): Record<string, CardState> {
  const st = cards[pb.card_id];
  if (!st) return cards;
  return { ...cards, [pb.card_id]: { ...st, locked: true, postback: pb } };
}
