import { Effect } from "effect";
import { apiGetE, apiDeleteE } from "@wick-fe/common-api";
import type { SessionListItem, SessionMeta, ConversationTurn, TurnEvent } from "../types/agents.js";

export const listSessions = (base: string, projectId?: string) => {
  const url = projectId
    ? `${base}/api/sessions?project=${encodeURIComponent(projectId)}`
    : `${base}/api/sessions`;
  return apiGetE<{ sessions: SessionListItem[] }>(url).pipe(
    Effect.map((r) => ({ sessions: r.sessions ?? [] })),
  );
};

export const getConversation = (base: string, id: string) =>
  apiGetE<{ turns: ConversationTurn[] }>(`${base}/api/sessions/${id}/conversation`).pipe(
    Effect.map((r) => ({
      turns: (r.turns ?? []).map((t) => ({
        ...t,
        events: t.events ?? [],
        attachments: t.attachments ?? [],
      })),
    })),
  );

export const getSessionMeta = (base: string, id: string) =>
  apiGetE<SessionMeta>(`${base}/api/sessions/${id}/meta`);

export const deleteSession = (base: string, id: string) =>
  apiDeleteE<unknown>(`${base}/sessions/${encodeURIComponent(id)}`);

function normalizeTurnEvents(raw: unknown): TurnEvent[] {
  if (Array.isArray(raw)) return raw as TurnEvent[];
  if (raw && typeof raw === "object" && Array.isArray((raw as Record<string, unknown>).events)) {
    return (raw as { events: TurnEvent[] }).events;
  }
  return [];
}

export const getTurnTrace = (base: string, id: string, turnId: string) =>
  apiGetE<unknown>(`${base}/api/sessions/${id}/turns/${turnId}`).pipe(
    Effect.map(normalizeTurnEvents),
  );
