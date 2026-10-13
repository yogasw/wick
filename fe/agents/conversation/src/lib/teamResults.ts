/* One UI per delegation (T16 S9): a teammate's reply lives in the
   "Delegated to N teammates" block of the turn that sent the task, so the
   prompts teamlink hands the agent with those replies (a user turn, source
   "subagent") and the mention_handoff pills of the same tasks leave the
   thread. A late reply that re-woke the agent stays as one small line,
   "Updated with Research's reply", right above the answer it refined.
   Pure, so ConversationThread stays layout. */

/** What a team-results prompt is: "hide" (its replies show in a block),
    "update" (a late reply: the label stays), or null (not one, or about a
    task no block shows — it keeps rendering as before). */
export type TeamResult = { kind: "hide" } | { kind: "update"; label: string };

const TASK_ID = /\[task ([^\],\s]+)/g;
// teamlink/batch.go combinedLocked: the batched wake and a later update.
const BATCH_HEAD = /^\[team results · /;
const UPDATE_HEAD = /^\[team results update · [^\]\n]*?· Updated with (.+?) · \d+ of \d+ replied\]/;
// singleText / framedSingle: one task's reply or question.
const SINGLE_HEAD = /^Reply from .+ \(@[a-z0-9][a-z0-9-]*\) \[task ([^\],\s]+)/;
// A reply after fan_in first already answered; Label() is "Name (@handle)".
const FOLLOW_HEAD = /^Follow-up from (.+?)(?: \(@[a-z0-9][a-z0-9-]*\))? \[task ([^\],\s]+)/;
// A real ride-along follow-up: its own "<follow-up>" line under "Follow-ups:".
// The batch preamble names the tag mid-line, so a substring is not enough.
const FOLLOW_UP_LINE = /^<follow-up>$/m;

function taskIds(text: string): string[] {
  return [...text.matchAll(TASK_ID)].map((m) => m[1]);
}

/** "@research's, @qa's reply" with each handle read as its name. */
function namedLabel(who: string, names: Record<string, string>): string {
  return who.replace(/@([a-z0-9][a-z0-9-]*)/g, (all, h: string) => names[h] || all);
}

/** teamResultOf reads one turn. shown = the task ids a block in this chat
    shows; names = handle → display name. Only a user turn whose source is
    "subagent" counts: a person typing the same words stays a person. */
export function teamResultOf(
  turn: { role: string; source?: string; text?: string },
  shown: Set<string>,
  names: Record<string, string> = {},
): TeamResult | null {
  if (turn.role !== "user" || (turn.source ?? "").trim().toLowerCase() !== "subagent") return null;
  const text = turn.text ?? "";
  const inBlock = (ids: string[]) => ids.some((id) => shown.has(id));
  const upd = UPDATE_HEAD.exec(text);
  if (upd) return inBlock(taskIds(text)) ? { kind: "update", label: `Updated with ${namedLabel(upd[1], names)}` } : null;
  // Follow-ups riding along are not rows of any block: the prompt shrinks
  // to one line instead of hiding.
  if (BATCH_HEAD.test(text)) {
    if (!inBlock(taskIds(text))) return null;
    return FOLLOW_UP_LINE.test(text) ? { kind: "update", label: "Updated with follow-ups" } : { kind: "hide" };
  }
  const fol = FOLLOW_HEAD.exec(text);
  if (fol) return shown.has(fol[2]) ? { kind: "update", label: `Updated with ${fol[1]}'s reply` } : null;
  const one = SINGLE_HEAD.exec(text);
  if (one) return shown.has(one[1]) ? { kind: "hide" } : null;
  return null;
}

/** A mention_handoff pill whose task a block shows: the block names the
    hand-off already. */
export function isHandoffInBlock(turn: { role: string; kind?: string; extras?: Record<string, string> }, shown: Set<string>): boolean {
  return turn.role === "system" && turn.kind === "mention_handoff" && !!turn.extras?.task_id && shown.has(turn.extras.task_id);
}

/** foldTeamResults drops what a block already shows (results prompts,
    hand-off pills) and marks a late reply's prompt with its label
    (team_update), for the thread to draw as one line. */
export function foldTeamResults<T extends { role: string; source?: string; text?: string; kind?: string; extras?: Record<string, string> }>(
  turns: T[],
  shown: Set<string>,
  names: Record<string, string> = {},
): (T & { team_update?: string })[] {
  if (shown.size === 0) return turns;
  const out: (T & { team_update?: string })[] = [];
  for (const t of turns) {
    if (isHandoffInBlock(t, shown)) continue;
    const r = teamResultOf(t, shown, names);
    if (!r) out.push(t);
    else if (r.kind === "update") out.push({ ...t, team_update: r.label });
  }
  return out;
}
