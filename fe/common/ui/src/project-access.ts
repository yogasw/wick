// Data and wording for a project's Access requests card.

export interface AccessItem {
  id: string;
  connector_key: string;
  op_key: string;
  target: string;
  status: string;
  mode: string;
  decided_by: string;
  note: string;
  error: string;
  approvers: string[];
}

export interface AccessRequestRow {
  id: string;
  session_id?: string;
  agent: string;
  reason: string;
  status: string;
  created_at: string;
  items: AccessItem[];
}

export interface AccessRuleRow {
  id: string;
  name: string;
  effect: string;
  simulate: boolean;
  disabled: boolean;
  rev: number;
  owner: string;
  can_edit: boolean;
  yaml: string;
}

export interface AccessGrantRow {
  id: string;
  op_key: string;
  target: string;
  session_id: string;
  approver: string;
}

export interface ProjectAccess {
  requests: AccessRequestRow[];
  rules: AccessRuleRow[];
  grants: AccessGrantRow[];
  fact_names: string[];
  rule_example: string;
  script_examples: string;
}

export interface SimulateReply {
  result: { effect: string; rule?: string; kind: string; simulated?: string[] };
  script?: { allow: boolean; reason?: string; error?: string; logs?: string[] };
  script_error?: string;
}

const MODE: Record<string, string> = { once: "once", session: "for this session" };

export function accessStatusIcon(status: string): string {
  switch (status) {
    case "ok": return "✅";
    case "error": return "❌";
    case "denied": return "🚫";
    case "running": return "🔄";
    default: return "⏳";
  }
}

/** One plain line for what happened to an action. */
export function accessRequestLine(it: AccessItem): string {
  const by = it.decided_by || "someone";
  switch (it.status) {
    case "ok": return `approved ${MODE[it.mode] ?? it.mode} by ${by}, ran OK`;
    case "error": return `approved ${MODE[it.mode] ?? it.mode} by ${by}, failed: ${it.error}`;
    case "denied": return `denied by ${by}${it.note ? `: ${it.note}` : ""}`;
    case "running": return `approved ${MODE[it.mode] ?? it.mode} by ${by}, running…`;
    default: return `waiting · who can approve: ${it.approvers.join(", ") || "nobody"}`;
  }
}

function url(base: string, projectId: string, path = ""): string {
  return `${base}/api/projects/${encodeURIComponent(projectId)}/access${path ? "/" + path : ""}`;
}

async function readError(resp: Response): Promise<string> {
  const text = await resp.text().catch(() => "");
  try {
    const j = JSON.parse(text) as { error?: string };
    if (j.error) return j.error;
  } catch {
    /* not JSON */
  }
  return text || `HTTP ${resp.status}`;
}

export async function loadProjectAccess(base: string, projectId: string): Promise<ProjectAccess> {
  const resp = await fetch(url(base, projectId), { credentials: "same-origin", headers: { Accept: "application/json" } });
  if (!resp.ok) throw new Error(await readError(resp));
  return resp.json() as Promise<ProjectAccess>;
}

export async function postProjectAccess(base: string, projectId: string, path: string, body: unknown): Promise<unknown> {
  const resp = await fetch(url(base, projectId, path), {
    method: "POST",
    credentials: "same-origin",
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    body: JSON.stringify(body ?? {}),
  });
  if (!resp.ok) throw new Error(await readError(resp));
  return resp.json();
}
