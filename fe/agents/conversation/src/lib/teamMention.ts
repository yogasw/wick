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
  avatar?: { shape?: string; color?: string } | null;
};

/** teamMentionAgents lists the owner's agents an agent's chat can @-mention:
    every agent but itself and the disabled ones (the router refuses those),
    with a one-line tagline from the description. */
export function teamMentionAgents(peers: TeamPeer[] | null | undefined, selfId: string): ComposerMentionAgent[] {
  return (peers ?? [])
    .filter((p) => p.id !== selfId && !p.disabled && p.handle)
    .map((p) => ({
      handle: p.handle,
      label: p.name || p.handle,
      hint: tagline(p.description),
      group: "team" as const,
      avatar: { shape: p.avatar?.shape, color: p.avatar?.color },
    }));
}

function tagline(desc: string): string | undefined {
  const line = (desc ?? "").split("\n").find((l) => l.trim())?.trim() ?? "";
  if (!line) return undefined;
  return line.length > 80 ? `${line.slice(0, 79)}…` : line;
}

/** teamSender reads the frame teamlink puts on a teammate's message
    ("Message from <Name> (@handle):\n<body>"). Only trusted on a turn whose
    source is "team" — a person typing the same words stays a person. */
export function teamSender(source: string | undefined, text: string): { name: string; handle: string; body: string } | null {
  if ((source ?? "").trim().toLowerCase() !== "team") return null;
  const m = /^Message from (.+) \(@([a-z0-9][a-z0-9-]*)\):\n?/.exec(text ?? "");
  if (!m) return null;
  return { name: m[1].trim(), handle: m[2], body: text.slice(m[0].length) };
}

/** A mention_handoff system turn's fields, as the server writes them in Extras. */
export type Handoff = { from: string; to: string; state: string; taskId: string; contextId: string; toAgentId: string };

export function handoffOf(extras: Record<string, string> | null | undefined): Handoff {
  const x = extras ?? {};
  return {
    from: x.from || "user",
    to: x.to ?? "",
    state: x.state ?? "",
    taskId: x.task_id ?? "",
    contextId: x.context_id ?? "",
    toAgentId: x.to_agent_id ?? "",
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
    "12/50 turns · leg 2 of 2 · 51 total"; a single leg keeps "x/y turns". */
export function subAgentTurns(s: { turns_used: number; max_turns: number; resumes?: number; leg_base_turns?: number }): string {
  const resumes = s.resumes ?? 0;
  if (resumes <= 0) return s.max_turns > 0 ? `${s.turns_used}/${s.max_turns} turns` : `${s.turns_used} turns`;
  const base = s.leg_base_turns ?? 0;
  const used = Math.max(0, s.turns_used - base);
  const cap = s.max_turns > base ? `/${s.max_turns - base}` : "";
  return `${used}${cap} turns · leg ${resumes + 1} of ${resumes + 1} · ${s.turns_used} total`;
}

/** collapseHandoffs folds the mention_handoff turns of one task into one
    row: it stays where the first (working) turn sits and takes the latest
    turn's state. The server appends one turn per transition. */
export function collapseHandoffs<T extends { role: string; kind?: string; text: string; extras?: Record<string, string> }>(turns: T[]): T[] {
  const latest = new Map<string, T>();
  for (const t of turns) {
    const id = t.kind === "mention_handoff" ? t.extras?.task_id : undefined;
    if (id) latest.set(id, t);
  }
  if (latest.size === 0) return turns;
  const seen = new Set<string>();
  const out: T[] = [];
  for (const t of turns) {
    const id = t.kind === "mention_handoff" ? t.extras?.task_id : undefined;
    if (!id) { out.push(t); continue; }
    if (seen.has(id)) continue;
    seen.add(id);
    const last = latest.get(id)!;
    out.push(last === t ? t : { ...t, text: last.text, extras: { ...t.extras, ...last.extras } });
  }
  return out;
}
