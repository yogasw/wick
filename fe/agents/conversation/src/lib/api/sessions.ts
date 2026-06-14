import { apiGetE } from "@wick-fe/common-api";
import type { SessionListItem, SessionMeta, ConversationTurn } from "../types/agents.js";

export const listSessions = (base: string) =>
  apiGetE<{ sessions: SessionListItem[] }>(`${base}/api/sessions`);

export const getConversation = (base: string, id: string) =>
  apiGetE<{ turns: ConversationTurn[] }>(`${base}/api/sessions/${id}/conversation`);

export const getSessionMeta = (base: string, id: string) =>
  apiGetE<SessionMeta>(`${base}/api/sessions/${id}/meta`);
