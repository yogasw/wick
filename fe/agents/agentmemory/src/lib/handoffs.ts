// Handoffs tab logic: how a baton is described, and — the part that matters —
// how the outcome of cancelling one is reported.

import type { Handoff, HandoffCancelResult, Message } from "./types.js";

// handoffAge is how long a baton has been waiting. An old one is the signal
// the tab exists for: a handoff nobody picked up means the next session never
// got the context the last one left.
export function handoffAge(h: Handoff, now: number = Date.now()): string {
  if (!h.created_at_ms) return "age unknown";
  const ms = now - h.created_at_ms;
  if (ms < 0) return "just now";
  const mins = Math.floor(ms / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

// handoffParties describes who left the baton and who it is for. An unaddressed
// handoff ("to" empty) is the normal case — it is left for whoever comes next
// — and saying so beats an empty cell that reads like missing data.
export function handoffParties(h: Handoff): string {
  const from = (h.from_agent ?? "").trim() || "unknown agent";
  const to = (h.to_agent ?? "").trim();
  return to ? `${from} → ${to}` : `${from} → whoever picks it up next`;
}

// cancelConfirmBody is the ConfirmDialog text. It names what is lost, not just
// that something is: the summary, the open questions and the next steps the
// last session wrote for the next one all stop being handed over.
export function cancelConfirmBody(h: Handoff | null): string {
  if (!h) return "";
  const who = (h.from_agent ?? "").trim() || "an agent";
  const where = (h.cwd ?? "").trim();
  return (
    `This baton was left by ${who}${where ? ` in ${where}` : ""}. Cancelling retires it for good: ` +
    "the summary, open questions and next steps it carries stop being handed over, and any agent waiting on it gets nothing. It cannot be restored."
  );
}

// CancelOutcome is how a cancellation is reported back.
//
// `gone` exists because the backend answers a second cancel with
// `cancelled: false` and NO error — the baton had already expired or been
// accepted between the listing and the click (verified 2026-09-25). Folding
// that into a success tells the operator they stopped something that had
// already stopped itself, and they stop looking for the agent that took it.
export type CancelOutcome =
  | { kind: "cancelled"; title: string; body: string }
  | { kind: "gone"; title: string; body: string }
  | { kind: "failed"; title: string; body: string };

export function cancelOutcome(res: HandoffCancelResult | null | undefined, error?: string): CancelOutcome {
  if (error) return { kind: "failed", title: "Could not cancel", body: error };
  if (!res) return { kind: "failed", title: "Could not cancel", body: "The backend returned no result." };
  if (res.cancelled) {
    return { kind: "cancelled", title: "Handoff cancelled", body: `${res.handoff_id} is retired.` };
  }
  return {
    kind: "gone",
    title: "Already gone",
    body:
      `Nothing was cancelled: ${res.handoff_id} had already ${res.state === "expired" ? "expired or been accepted" : `moved to "${res.state ?? "another state"}"`}` +
      " before the request arrived. If another agent accepted it, that agent has the context.",
  };
}

// ── cross-project messages ───────────────────────────────────────────

// messageSender describes who sent a message.
//
// The backend gives a UUID and nothing else. There is no name for it in the
// CLI listing, in the MCP equivalent, or in the project listing — which
// carries no ids to join against (verified 2026-09-25). So the id is shown as
// an id, shortened for reading but never turned into a name: an operator acts
// on who sent a message, and a guessed project name is the worst possible
// thing to hand them.
export function messageSender(m: Message): string {
  const agent = (m.from_agent ?? "").trim();
  const id = (m.from_project_id ?? "").trim();
  if (!id) return agent || "unknown sender";
  const short = id.length > 8 ? `${id.slice(0, 8)}…` : id;
  return agent ? `${agent} · project ${short}` : `project ${short}`;
}

// SENDER_NOTE says out loud why the sender is an id. Without it the column
// looks like a bug rather than the limit of what the backend knows.
export const SENDER_NOTE =
  "The backend identifies a sender only by project id — no name for it exists anywhere in its API, so none is shown.";

// messageSummary is the one-line preview. Falls back to the body when there is
// no subject, because a mailbox row with an empty label is unusable.
export function messageSummary(m: Message): string {
  const subject = (m.subject ?? "").trim();
  if (subject) return subject;
  const body = (m.body ?? "").trim().replace(/\s+/g, " ");
  if (!body) return "(no subject or body)";
  return body.length > 80 ? `${body.slice(0, 80)}…` : body;
}

// messageStateLabel renders the mailbox state. "pending" is the one that
// matters — it means nobody has claimed the message yet.
export function messageStateLabel(m: Message): string {
  const s = (m.state ?? "").trim();
  if (s === "pending") return "waiting to be claimed";
  return s || "state unknown";
}
