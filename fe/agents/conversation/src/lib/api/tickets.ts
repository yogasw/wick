import { Effect } from "effect";
import { apiGetE, apiPutE } from "@wick-fe/common-api";
import type { TicketBoard, TicketConfig, TicketFilter, TicketItem } from "../types/agents.js";

export const getProjectTickets = (base: string, projectId: string) =>
  apiGetE<TicketBoard>(`${base}/api/projects/${encodeURIComponent(projectId)}/tickets`).pipe(
    Effect.map((r) => ({
      ...r,
      tickets: r.tickets ?? [],
      statuses: r.statuses ?? ["open", "in_progress", "waiting", "done"],
      users: r.users ?? {},
    })),
  );

export const saveTicketConfig = (base: string, projectId: string, cfg: TicketConfig) =>
  apiPutE<{ status: string }>(
    `${base}/api/projects/${encodeURIComponent(projectId)}/ticket-config`,
    cfg,
  );

export const updateSessionTicket = (
  base: string,
  sessionId: string,
  patch: { status?: string; assignee?: string; fields?: Record<string, string> },
) =>
  apiPutE<{ status: string; ticket: TicketItem }>(
    `${base}/api/sessions/${encodeURIComponent(sessionId)}/ticket`,
    patch,
  );

export type SessionTicket = {
  config: TicketConfig;
  ticket: {
    status: string;
    assignee?: string;
    fields?: Record<string, string>;
    updated_at?: string;
  } | null;
  statuses: string[];
  users?: Record<string, string>;
  me?: string;
};

export const getSessionTicket = (base: string, sessionId: string) =>
  apiGetE<SessionTicket>(`${base}/api/sessions/${encodeURIComponent(sessionId)}/ticket`).pipe(
    Effect.map((r) => ({
      ...r,
      statuses: r.statuses ?? ["open", "in_progress", "waiting", "done"],
      users: r.users ?? {},
    })),
  );

export const getTicketFilter = (base: string, projectId: string) =>
  apiGetE<TicketFilter>(`${base}/api/me/ticket-filters/${encodeURIComponent(projectId)}`).pipe(
    Effect.map((f) => f ?? {}),
  );

export const saveTicketFilter = (base: string, projectId: string, f: TicketFilter) =>
  apiPutE<{ status: string }>(`${base}/api/me/ticket-filters/${encodeURIComponent(projectId)}`, f);
