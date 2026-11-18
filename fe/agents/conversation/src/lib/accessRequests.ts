/* The access_request card: an agent asks a person to approve connector
   actions its own access cannot run. The server re-records the card under
   one request_id on every change; foldSystemEvents keeps the latest, so
   the view here is always the current one. Pure, so it is tested here. */

export type AccessItemView = {
  id: string;
  n: number;
  connector_key: string;
  connector_label: string;
  op_key: string;
  op_name: string;
  kind: "read" | "write" | "send" | "delete" | string;
  target: string;
  status: "pending" | "running" | "ok" | "error" | "denied" | string;
  mode: string;
  decided_by: string;
  note: string;
  error: string;
  result: string;
  approvers: string[];
  /** Who may approve as an approvers rule names it ("members of @ops"); replaces the list when set. */
  approvers_text?: string;
  can_decide: boolean;
  modes: string[];
  locked_reason: string;
  locked_code: string;
  params: { name: string; value: string; long: boolean }[];
  params_json: string;
};

export type AccessRequestView = {
  id: string;
  agent: string;
  requester: string;
  /** Whose message the agent was answering when it asked ("" = nobody: a schedule, another agent). */
  asked_by?: string;
  reason: string;
  status: "pending" | "done" | "cancelled" | string;
  pending: number;
  url: string;
  items: AccessItemView[];
  viewer_can_decide: boolean;
  viewer_reason: string;
  viewer_reason_code: string;
};

export type AccessCard = {
  id: string;
  api: string;
  view: AccessRequestView | null;
  /** Anything still waiting for a decision. */
  open: boolean;
};

export function accessCardOf(text: string, extras: Record<string, string> | undefined): AccessCard {
  const x = extras ?? {};
  let view: AccessRequestView | null = null;
  try {
    view = JSON.parse(x.view || "null") as AccessRequestView | null;
  } catch (_) { /* a malformed view shows just the text */ }
  const pending = view ? view.pending : Number(x.pending || "0");
  const status = view?.status ?? x.state ?? "pending";
  return { id: x.request_id ?? "", api: x.api ?? "", view, open: status === "pending" && pending > 0 };
}

export function accessStatusIcon(status: string): string {
  switch (status) {
    case "ok": return "✅";
    case "error": return "❌";
    case "denied": return "⛔";
    case "running": return "🔄";
  }
  return "⏳";
}

export const MODE_LABEL: Record<string, string> = {
  once: "Once",
  hour: "1 hour",
  "8h": "8 hours",
  "24h": "24 hours",
  session: "This session",
  always: "Always",
  deny: "Deny",
};

/** itemLine is the status line under one action on the card. */
export function itemLine(it: AccessItemView): string {
  const mode = (MODE_LABEL[it.mode] ?? it.mode).toLowerCase();
  switch (it.status) {
    case "pending": return `Waiting · who can approve: ${it.approvers_text || (it.approvers.length ? it.approvers.join(", ") : "nobody yet")}${it.note ? ` · ℹ️ ${it.note}` : ""}`;
    case "running": return `Approved ${mode} by ${it.decided_by} · running…`;
    case "ok": return `Approved ${mode} by ${it.decided_by} · OK`;
    case "error": return `Approved ${mode} by ${it.decided_by} · error: ${it.error}`;
    case "denied": return `Denied by ${it.decided_by}${it.note ? `: ${it.note}` : ""}`;
  }
  return it.status;
}

/** kindHint says why a mode is missing for sends and deletes. */
export function kindHint(kind: string): string {
  if (kind === "delete") return "Deletes: only \"Once\" is offered.";
  return "";
}

/** noAccess is true when the viewer can decide nothing for a reason worth
    a full panel (not linked, not an approver, no own access). */
export function noAccess(v: AccessRequestView): boolean {
  return !v.viewer_can_decide && !!v.viewer_reason && v.viewer_reason_code !== "already_decided" && v.viewer_reason_code !== "inactive";
}
