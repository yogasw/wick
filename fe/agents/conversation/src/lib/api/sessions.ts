import { Effect } from "effect";
import { apiGetE, apiDeleteE } from "@wick-fe/common-api";
import type { SessionListItem, SessionMeta, ConversationTurn } from "../types/agents.js";

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
