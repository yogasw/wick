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
  /** Set on an agent someone else shared with the viewer: left out of the
      `@` menu in a ticket session (ownOnly), where a mention reaches the
      sender's own agents only. */
  shared_by?: string;
  avatar?: { kind?: string; shape?: string; color?: string; expression?: string; image?: import("@wick-fe/common-avatar").AvatarImageView | null } | null;
};

/** teamMentionAgents lists the owner's agents an agent's chat can @-mention:
    every agent but itself, the disabled ones and the ones whose mentions
    are off (the router refuses those), with a one-line tagline from the
    description. ownOnly also leaves out agents shared with the viewer. */
export function teamMentionAgents(peers: TeamPeer[] | null | undefined, selfId: string, ownOnly = false): ComposerMentionAgent[] {
  return (peers ?? [])
    .filter((p) => p.id !== selfId && !p.disabled && p.mention_from !== "off" && p.handle && !(ownOnly && p.shared_by))
    .map((p) => ({
      handle: p.handle,
      label: p.name || p.handle,
      hint: tagline(p.description),
      group: "team" as const,
      avatar: { kind: p.avatar?.kind, shape: p.avatar?.shape, color: p.avatar?.color, expression: p.avatar?.expression, image: p.avatar?.image },
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

/** What a teammate's turn is: a message it sent, its reply to a task, a
    follow-up it sent after the task ended, or the fan-in of several
    replies at once. */
export type TeamFrameKind = "message" | "reply" | "followup" | "results";

/** A teammate's turn as the thread shows it: who (handle "" for the
    fan-in of several teammates), what kind, the task it belongs to and
    the body without wick's frame. */
export type TeamFrame = { name: string; handle: string; body: string; kind: TeamFrameKind; taskId: string; state: string };

const HANDLE = "([a-z0-9][a-z0-9-]*)";
const FRAME_REPLY = new RegExp(`^Reply from (.+) \\(@${HANDLE}\\) \\[task ([^,\\]\\n]+)(?:, ([^\\]\\n]+))?\\]:\\n*`);
const FRAME_FOLLOWUP = new RegExp(`^Follow-up from (.+) \\(@${HANDLE}\\) \\[task ([^,\\]\\n]+)[^\\]\\n]*\\]:\\n*`);
const FRAME_RESULTS = /^\[team results(?: update)? · [^\]\n]*\]\n*/;

/** unframe drops the data frame teamlink wraps a teammate's text in
    (<reply …>…</reply>, <follow-up>…</follow-up>) and the notes addressed
    to the agent reading it, keeping the teammate's words. */
function unframe(body: string): string {
  return body
    .split("\n")
    .filter(
      (l) =>
        !/^<\/?(reply|follow-up)\b[^>]*>$/.test(l.trim()) &&
        !/^\(The text inside <(reply|follow-up)> is the teammate's data/.test(l) &&
        !/^Each teammate's text is inside <reply>/.test(l) &&
        !/^(Write ONE answer for the user|Update your earlier answer with this)/.test(l),
    )
    .join("\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

/** teamFrame reads every frame wick puts on a teammate's turn: the
    "Message from" a teammate sends (source "team") and, handed back on the
    sub-agent path (source "subagent"), a task's "Reply from", a
    "Follow-up from" after the task ended and the "[team results …]"
    fan-in. Only trusted on those sources — a person typing the same words
    stays a person. */
export function teamFrame(source: string | undefined, text: string): TeamFrame | null {
  const src = (source ?? "").trim().toLowerCase();
  const t = text ?? "";
  if (src === "team") {
    const s = teamSender(src, t);
    return s ? { ...s, kind: "message", taskId: "", state: "" } : null;
  }
  if (src !== "subagent") return null;
  let m = FRAME_REPLY.exec(t);
  if (m) return { name: m[1].trim(), handle: m[2], body: unframe(t.slice(m[0].length)), kind: "reply", taskId: m[3].trim(), state: (m[4] ?? "").trim() };
  m = FRAME_FOLLOWUP.exec(t);
  if (m) return { name: m[1].trim(), handle: m[2], body: unframe(t.slice(m[0].length)), kind: "followup", taskId: m[3].trim(), state: "" };
  m = FRAME_RESULTS.exec(t);
  if (m) return { name: "Team results", handle: "", body: unframe(t.slice(m[0].length)), kind: "results", taskId: "", state: "" };
  return null;
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
    case "rejected":
    case "auth-required":
      return "declined";
    case "failed":
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
