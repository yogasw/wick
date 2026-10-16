import type { AgentRow } from "./types.js";

// Which origin badge a "Running agents" row gets. Kept out of the
// component so the three-way decision is unit-testable without a DOM.
//
//   wick            — the pool knows it: spawned by this wick.
//   wick-session    — the pool does not, but the process carries a wick
//                     MCP token whose session (and project, if it has one)
//                     resolved. Usually an agent of the previous daemon
//                     still finishing during a reload.
//   wick-unresolved — a wick token, but its session title or project
//                     could not be read. Flagged, not hidden.
//   outside         — no wick token: started by another service.
export type AgentOrigin = "wick" | "wick-session" | "wick-unresolved" | "outside";

export function agentOrigin(a: Pick<AgentRow, "from_wick" | "wick_session">): AgentOrigin {
  if (a.from_wick) return "wick";
  const ws = a.wick_session;
  if (!ws?.detected) return "outside";
  // A session outside any project is not a failure to resolve; a
  // project_id whose name could not be read is.
  if (!ws.title || (ws.project_id && !ws.project)) return "wick-unresolved";
  return "wick-session";
}

// shortSessionId trims a session id for a tooltip: enough to search for,
// short enough to read.
export function shortSessionId(id: string | undefined): string {
  if (!id) return "unknown";
  return id.length > 12 ? `${id.slice(0, 12)}…` : id;
}

// unresolvedTitle is the warning tooltip. Names the session only by a
// shortened id; never anything taken from the token itself.
export function unresolvedTitle(a: Pick<AgentRow, "wick_session">): string {
  return `Wick session, but its project/title could not be resolved (session ${shortSessionId(a.wick_session?.session_id)}).`;
}
