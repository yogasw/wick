/* What the server-made interactive cards in the thread say: an ask_user
   question (input_request) and a gate approval (approval_request). Both are
   written twice under one id — pending, then settled — and folded into one row
   by foldSystemEvents, so these read the latest extras. Pure, so the cards
   stay thin and the rules are tested here. */

export type InputRequestView = {
  question: string;
  state: "pending" | "answered" | "timeout" | "cancelled" | string;
  pending: boolean;
  /** The pill shown once settled: "answered: X" / "no answer in time" / … */
  pill: string;
  /** Option labels, shown under a pending question as a hint. */
  options: string[];
};

export function inputRequestView(text: string, extras: Record<string, string> | undefined): InputRequestView {
  const x = extras ?? {};
  const state = x.state || "pending";
  let options: string[] = [];
  try {
    const req = JSON.parse(x.request || "{}") as { options?: { label?: string; value?: string }[] };
    options = (req.options ?? []).map((o) => o.label || o.value || "").filter(Boolean);
  } catch (_) { /* a malformed request just shows no options */ }
  const question = x.question || (state === "pending" ? text : "");
  let pill = "";
  if (state === "answered") pill = `answered: ${x.answer ?? ""}`.trim();
  else if (state !== "pending") pill = text || state;
  return { question, state, pending: state === "pending", pill, options };
}

export type ApprovalDecisionChoice = "accept" | "accept_for_session" | "decline";

export type ApprovalView = {
  id: string;
  pending: boolean;
  tool: string;
  cmd: string;
  agent: string;
  /** Settled: the gate's decision in words ("accepted", "declined", …). */
  outcome: string;
  /** Settled: whether the command went ahead. */
  allowed: boolean;
  /** An access change a Captain proposed (agents.set_access): no command,
      a target agent and the diff lines; Accept / Decline only. */
  access?: { target: string; changes: string[]; reason: string };
};

const OUTCOME: Record<string, string> = {
  approve_once: "accepted",
  approve_session: "accepted for this agent",
  approve_always: "always allowed",
  block: "declined",
  guide: "declined with guidance",
};

export function approvalView(text: string, extras: Record<string, string> | undefined): ApprovalView {
  const x = extras ?? {};
  const state = x.state || "pending";
  const pending = state === "pending";
  let tool = x.tool ?? "";
  let cmd = x.cmd ?? "";
  // The settled turn carries only id + decision; the folded row keeps the
  // pending turn's tool/cmd, but a lone settled turn falls back to its text.
  if (!tool && !cmd && pending) {
    const m = /^([^:]+):\s*(.*)$/s.exec(text);
    if (m) { tool = m[1]; cmd = m[2]; }
  }
  const access = x.type === "access_change";
  return {
    id: x.approval_id ?? "",
    pending,
    tool: access ? "" : tool,
    cmd: access ? "" : cmd,
    agent: x.agent ?? "",
    outcome: pending ? "" : access ? (state.startsWith("approve") ? "applied" : "declined") : (OUTCOME[state] ?? text ?? state),
    allowed: state.startsWith("approve"),
    ...(access
      ? { access: { target: x.target ?? "", changes: (x.changes ?? "").split("\n").filter(Boolean), reason: x.reason ?? "" } }
      : {}),
  };
}
