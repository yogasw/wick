/* Who a user-role turn in the thread is from, for how it is drawn. Only a
   person's message sits on the right in the brand green: a teammate's
   message and anything a program posted (a schedule, a job's CLI report,
   a sub-agent's result) is a card on the left, the width of an answer, so
   the right side keeps meaning "a person typed this". */

import { teamFrame, type TeamFrame } from "./teamMention.js";

export type MessageKind = "human" | "teammate" | "system";

/** A source no person types into: its turn is a system card. "rest" and
    "api" are not here: a person writing through the API is a person. */
export type SystemSource = { key: string; icon: string; label: string };

const SYSTEM_SOURCES: Record<string, Omit<SystemSource, "key">> = {
  schedule: { icon: "⏰", label: "schedule" },
  watch: { icon: "👁", label: "watch" },
  cli: { icon: "⌨", label: "cli" },
  agentctl: { icon: "⌨", label: "cli" },
  subagent: { icon: "🤖", label: "sub-agent" },
  agent: { icon: "🤖", label: "agent" },
  workflow: { icon: "🔀", label: "workflow" },
  failover: { icon: "↻", label: "failover" },
};

/** systemSource is the chip of a source no person types into, null for
    a person's (web, Slack, Telegram, …). */
export function systemSource(source: string | undefined): SystemSource | null {
  const key = (source ?? "").trim().toLowerCase();
  const s = SYSTEM_SOURCES[key];
  return s ? { key, ...s } : null;
}

/** messageKind classifies a user-role turn: a teammate's framed turn,
    a program's, or a person's. */
export function messageKind(source: string | undefined, text: string): { kind: MessageKind; team: TeamFrame | null; system: SystemSource | null } {
  const team = teamFrame(source, text);
  if (team) return { kind: "teammate", team, system: null };
  const system = systemSource(source);
  if (system) return { kind: "system", team: null, system };
  return { kind: "human", team: null, system: null };
}

/** foldable reports whether a card's body is long enough to fold: more
    than lines lines or chars characters. */
export function foldable(text: string, lines: number, chars: number): boolean {
  const t = text ?? "";
  return t.split("\n").length > lines || t.length > chars;
}

/** firstLine is what a folded system card shows: its first non-empty
    line. */
export function firstLine(text: string): string {
  return (text ?? "").split("\n").find((l) => l.trim())?.trim() ?? "";
}
