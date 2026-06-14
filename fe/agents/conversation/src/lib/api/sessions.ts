import { Effect } from "effect";
import { apiGetE } from "@wick-fe/common-api";
import type { SessionListItem, SessionMeta, ConversationTurn } from "../types/agents.js";

export const listSessions = (base: string) =>
  apiGetE<{ sessions: SessionListItem[] }>(`${base}/api/sessions`).pipe(
    Effect.map((r) => ({ sessions: r.sessions ?? [] })),
  );

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
