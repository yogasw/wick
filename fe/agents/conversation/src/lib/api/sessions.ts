import { Effect } from "effect";
import { apiGetE, apiDeleteE, apiPostE } from "@wick-fe/common-api";
import type { SessionListItem, SessionMeta, ConversationTurn, TurnEvent, TurnEventPayload } from "../types/agents.js";

/** owner: "me" = only the caller's sessions (plus, under ticket mode,
    sessions on tickets assigned to them); omitted = everyone's. The server
    sends one page at a time — `offset` is the "Load more" cursor, `total`
    is the match count before the page window, `hasMore` says whether
    another page exists. */
export const listSessions = (
  base: string,
  projectId?: string,
  owner?: "me" | "all",
  offset?: number,
) => {
  const q = new URLSearchParams();
  if (projectId) q.set("project", projectId);
  if (owner === "me") q.set("owner", "me");
  if (offset) q.set("offset", String(offset));
  const qs = q.toString();
  return apiGetE<{ sessions: SessionListItem[]; total?: number; has_more?: boolean }>(
    `${base}/api/sessions${qs ? `?${qs}` : ""}`,
  ).pipe(
    Effect.map((r) => ({
      sessions: r.sessions ?? [],
      total: r.total ?? (r.sessions ?? []).length,
      hasMore: r.has_more === true,
    })),
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

// getTurnEvent fetches the spilled payload of one large trace event — an
// index row with large:true carries no text; its content lives in
// thinking/<turn_id>/<event_id>.json behind this endpoint.
export const getTurnEvent = (base: string, id: string, turnId: string, eventId: string) =>
  apiGetE<TurnEventPayload>(`${base}/sessions/${id}/turns/${turnId}/events/${eventId}`);
