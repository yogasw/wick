/* The Team side of the `@` menu and of the thread: which teammates an
   agent's chat offers, and how a teammate's message and a mention_handoff
   line read. Pure, so DetailView and ThreadMessage stay thin and the rules
   are tested here. */

import type { ComposerMentionAgent } from "@wick-fe/common-ui";

export type TeamPeer = {
  id: string;
  handle: string;
  name: string;
  description: string;
  disabled: boolean;
  /** "off" = takes no mention from another agent; hidden from `@`. */
  mention_from?: string;
  avatar?: { kind?: string; shape?: string; color?: string; expression?: string } | null;
};

/** teamMentionAgents lists the owner's agents an agent's chat can @-mention:
    every agent but itself, the disabled ones and the ones whose mentions
    are off (the router refuses those), with a one-line tagline from the
    description. */
export function teamMentionAgents(peers: TeamPeer[] | null | undefined, selfId: string): ComposerMentionAgent[] {
  return (peers ?? [])
    .filter((p) => p.id !== selfId && !p.disabled && p.mention_from !== "off" && p.handle)
    .map((p) => ({
      handle: p.handle,
      label: p.name || p.handle,
      hint: tagline(p.description),
      group: "team" as const,
      avatar: { kind: p.avatar?.kind, shape: p.avatar?.shape, color: p.avatar?.color, expression: p.avatar?.expression },
    }));
}

function tagline(desc: string): string | undefined {
  const line = (desc ?? "").split("\n").find((l) => l.trim())?.trim() ?? "";
  if (!line) return undefined;
  return line.length > 80 ? `${line.slice(0, 79)}…` : line;
}

/** teamSender reads the frame teamlink puts on a teammate's message
    ("Message from <Name> (@handle):\n<body>") and the reply teamlink hands
    back. Only trusted on a turn whose source is "team" or "subagent" — a
    person typing the same words stays a person. */
export function teamSender(source: string | undefined, text: string): { name: string; handle: string; body: string } | null {
  const src = (source ?? "").trim().toLowerCase();
  // An @mentioned teammate's answer handed back into the caller's session
  // ("Reply from <Name> (@handle) [task <id>, <state>]:\n\n<body>"). It is
  // stored as a user turn carrying the session owner as sender, so without
  // this it would read as a bubble the person typed.
  const re =
    src === "team"
      ? /^Message from (.+) \(@([a-z0-9][a-z0-9-]*)\):\n?/
      : src === "subagent"
        ? /^Reply from (.+) \(@([a-z0-9][a-z0-9-]*)\) \[task [^\]\n]*\]:\n*/
        : null;
  const m = re?.exec(text ?? "");
  if (!m) return null;
  return { name: m[1].trim(), handle: m[2], body: text.slice(m[0].length) };
}

/** A mention_handoff system turn's fields, as the server writes them in Extras. */
export type Handoff = { from: string; to: string; state: string; taskId: string; contextId: string; toAgentId: string; origin: string };

export function handoffOf(extras: Record<string, string> | null | undefined): Handoff {
  const x = extras ?? {};
  return {
    from: x.from || "user",
    to: x.to ?? "",
    state: x.state ?? "",
    taskId: x.task_id ?? "",
    contextId: x.context_id ?? "",
    toAgentId: x.to_agent_id ?? "",
    /* "user" when the person sent the task with an @mention (P45). */
    origin: x.origin ?? "",
  };
}

/** handoffState turns an A2A task state into the word the thread shows. */
export function handoffState(state: string): string {
  const s = state.replace(/^TASK_STATE_/i, "").toLowerCase().replace(/_/g, "-");
  switch (s) {
    case "submitted":
    case "working":
      return "working";
    case "input-required":
      return "needs input";
    case "completed":
      return "completed";
    case "failed":
    case "rejected":
      return "failed";
    case "canceled":
      return "canceled";
  }
  return s || "sent";
}

/* Leads a continue puts in front of a sub-agent's next instruction
   (delegation/continue.go continuationTask). A row that predates `title`
   shows one of these as its label; that is not the task. */
const RESUME_LEADS = [
  "You were stopped partway through",
  "A human stopped you mid-task",
  "Your previous run ended in an error",
  "You already finished one round of work",
];

/** subAgentTitle is what a sub-agent card names as its job: the first
    leg's task, not a continue's resume text. */
export function subAgentTitle(s: { label: string; title?: string }): string {
  if (s.title) return s.title;
  if (RESUME_LEADS.some((l) => s.label.startsWith(l))) return "Continued task (original not recorded)";
  return s.label;
}

/** subAgentTurns labels turns per leg once a row has been continued:
    "11 turns this leg · 51 total"; a single leg keeps "x/y turns". The
    per-leg cap and "leg n of n" were dropped: the cap read like a second
    budget and the leg count only ever said "the latest of all of them". */
export function subAgentTurns(s: { turns_used: number; max_turns: number; resumes?: number; leg_base_turns?: number }): string {
  const resumes = s.resumes ?? 0;
  if (resumes <= 0) return s.max_turns > 0 ? `${s.turns_used}/${s.max_turns} turns` : `${s.turns_used} turns`;
  const base = s.leg_base_turns ?? 0;
  const used = Math.max(0, s.turns_used - base);
  return `${used} turns this leg · ${s.turns_used} total`;
}

/** speakerVia names who an agent's mention-driven turn answered: the
    teammate behind the nearest earlier user turn (framed by teamlink).
    "" when the turn is not via a mention or nobody can be named. */
export function speakerVia<T extends { role: string; source?: string; text: string; speaker?: { via?: string } }>(turns: T[], i: number): string {
  if (turns[i]?.speaker?.via !== "mention") return "";
  for (let j = i - 1; j >= 0; j--) {
    if (turns[j].role !== "user") continue;
    return teamSender(turns[j].source, turns[j].text ?? "")?.handle ?? "";
  }
  return "";
}
