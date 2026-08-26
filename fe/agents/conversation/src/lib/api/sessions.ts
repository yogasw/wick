import { Effect } from "effect";
import { apiGetE, apiDeleteE, apiPostE } from "@wick-fe/common-api";
import type { SessionListItem, SessionMeta, ConversationTurn, TurnEvent } from "../types/agents.js";

export const listSessions = (base: string, projectId?: string) => {
  const url = projectId
    ? `${base}/api/sessions?project=${encodeURIComponent(projectId)}`
    : `${base}/api/sessions`;
  return apiGetE<{ sessions: SessionListItem[] }>(url).pipe(
    Effect.map((r) => ({ sessions: r.sessions ?? [] })),
  );
};

export const getConversation = (
  base: string,
  id: string,
  opts?: { limit?: number; before?: string },
) => {
  const params = new URLSearchParams();
  if (opts?.limit) params.set("limit", String(opts.limit));
  if (opts?.before) params.set("before", opts.before);
  const qs = params.toString();
  const url = `${base}/api/sessions/${id}/conversation${qs ? `?${qs}` : ""}`;
  return apiGetE<{ turns: ConversationTurn[]; has_more?: boolean }>(url).pipe(
    Effect.map((r) => ({
      turns: (r.turns ?? []).map((t) => ({
        ...t,
        events: t.events ?? [],
        attachments: t.attachments ?? [],
      })),
      hasMore: r.has_more === true,
    })),
  );
};

export const getSessionMeta = (base: string, id: string) =>
  apiGetE<SessionMeta>(`${base}/api/sessions/${id}/meta`);

export const deleteSession = (base: string, id: string) =>
  apiDeleteE<unknown>(`${base}/sessions/${encodeURIComponent(id)}`);

// cancelRun aborts one in-flight connector run (the per-tool-call Cancel button
// on a running wick_execute card). The run must belong to this session.
export const cancelRun = (base: string, sessionId: string, runId: string) =>
  apiPostE<{ status: string }>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/runs/${encodeURIComponent(runId)}/cancel`,
    {},
  );

function normalizeTurnEvents(raw: unknown): TurnEvent[] {
  if (Array.isArray(raw)) return raw as TurnEvent[];
  if (raw && typeof raw === "object" && Array.isArray((raw as Record<string, unknown>).events)) {
    return (raw as { events: TurnEvent[] }).events;
  }
  return [];
}

export const getTurnTrace = (base: string, id: string, turnId: string) =>
  apiGetE<unknown>(`${base}/sessions/${id}/turns/${turnId}`).pipe(
    Effect.map(normalizeTurnEvents),
  );
