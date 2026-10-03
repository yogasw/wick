/* The kind → chip table for server-recorded system turns (agent_created,
   access_changed, mention_handoff, hop_limit, …). Same shape as the trace
   renderer registry in common/ui: a new kind is one registerSystemEvent call,
   no render site changes. A kind nobody registered falls back to a plain chip
   with the server's own text — the server already writes it in English, ready
   to show. Pure, so the rules are tested here and SystemEventChip stays thin. */

import { handoffOf, handoffState } from "./teamMention.js";

export type SystemEventIcon =
  | "info"
  | "user-plus"
  | "key"
  | "arrow"
  | "stop"
  | "ban"
  | "clock"
  | "plug"
  | "link";

export type SystemEventTone = "neutral" | "warn";

/** One run of the chip's text: plain words, or a teammate handle the Team
    app can turn into a link to that agent's chat. */
export type SystemEventPart = string | { handle: string };

export type SystemEventTurn = { kind?: string; text: string; extras?: Record<string, string> };

export type SystemEventCtx = {
  /** Team agents by handle, for a display name in place of "@handle". */
  names?: Record<string, string>;
};

export type SystemEventRenderer = {
  icon: SystemEventIcon;
  tone?: SystemEventTone;
  /** What the chip says. Defaults to the turn's own text. */
  parts?: (turn: SystemEventTurn, ctx: SystemEventCtx) => SystemEventPart[];
};

const renderers = new Map<string, SystemEventRenderer>();
const fallback: SystemEventRenderer = { icon: "info" };

export function registerSystemEvent(kind: string, r: SystemEventRenderer): void {
  renderers.set(kind, r);
}

export function getSystemEvent(kind: string | undefined): SystemEventRenderer {
  return (kind && renderers.get(kind)) || fallback;
}

/** isSystemEventKind reports whether kind renders as a chip (as opposed to a
    card — input_request / approval_request — or a legacy notice). */
export function isSystemEventKind(kind: string | undefined): boolean {
  return !!kind && renderers.has(kind);
}

/** systemEventParts resolves what a chip says for turn. */
export function systemEventParts(turn: SystemEventTurn, ctx: SystemEventCtx = {}): SystemEventPart[] {
  const r = getSystemEvent(turn.kind);
  const parts = r.parts?.(turn, ctx);
  return parts && parts.length > 0 ? parts : [turn.text ?? ""];
}

registerSystemEvent("agent_created", { icon: "user-plus" });
registerSystemEvent("access_changed", { icon: "key" });
registerSystemEvent("access_change_declined", { icon: "ban", tone: "warn" });
registerSystemEvent("persona_changed", { icon: "info" });
registerSystemEvent("hop_limit", { icon: "stop", tone: "warn" });
registerSystemEvent("mention_refused", { icon: "ban", tone: "warn" });
registerSystemEvent("group_member_added", { icon: "user-plus" });
registerSystemEvent("group_member_removed", { icon: "ban" });
registerSystemEvent("scheduled_fired", { icon: "clock" });
// The old name of scheduled_fired, still in stored history.
registerSystemEvent("routine_fired", { icon: "clock" });
registerSystemEvent("connection_changed", { icon: "plug" });
registerSystemEvent("a2a_context", { icon: "link" });
registerSystemEvent("mention_handoff", {
  icon: "arrow",
  // Who handed what to whom and how it ended. The target is a handle so the
  // Team app can open its chat; the state is the latest one after folding.
  parts: (turn, ctx) => {
    const h = handoffOf(turn.extras);
    if (!h.to) return [];
    const from = h.from === "user" ? "You" : (ctx.names?.[h.from] ?? "@" + h.from);
    return [from, " → ", { handle: h.to }, " · " + handoffState(h.state)];
  },
});

/** The extras key that ties the 2+ turns one server-side thing writes
    (working → final, pending → settled) into one row, per kind. */
const FOLD_KEY: Record<string, string> = {
  mention_handoff: "task_id",
  input_request: "ask_id",
  approval_request: "approval_id",
};

/** foldSystemEvents folds the system turns that share a fold key into one
    row: it stays where the first turn sits and takes the latest turn's text
    and extras (latest state wins). It also drops a second copy of the same
    turn_id — the live stream can deliver one turn under two event names. */
export function foldSystemEvents<T extends { turn_id?: string; role: string; kind?: string; text: string; extras?: Record<string, string> }>(turns: T[]): T[] {
  const keyOf = (t: T): string | undefined => {
    const k = t.kind ? FOLD_KEY[t.kind] : undefined;
    const v = k ? t.extras?.[k] : undefined;
    return v ? `${t.kind}:${v}` : undefined;
  };
  const latest = new Map<string, T>();
  for (const t of turns) {
    const k = keyOf(t);
    if (k) latest.set(k, t);
  }
  const seenKey = new Set<string>();
  const seenSys = new Set<string>();
  const out: T[] = [];
  for (const t of turns) {
    if (t.role === "system" && t.kind && t.turn_id) {
      if (seenSys.has(t.turn_id)) continue;
      seenSys.add(t.turn_id);
    }
    const k = keyOf(t);
    if (!k) { out.push(t); continue; }
    if (seenKey.has(k)) continue;
    seenKey.add(k);
    const last = latest.get(k)!;
    out.push(last === t ? t : { ...t, text: last.text, extras: { ...t.extras, ...last.extras } });
  }
  return out;
}
