import { Effect } from "effect";
import { apiGetE, apiPostE, apiPutE, apiPatchE, apiDeleteE } from "@wick-fe/common-api";
import type {
  Note,
  NotesResponse,
  TicketBoard,
  TicketCard,
  TicketConfig,
  TicketDetail,
  TicketFilter,
} from "../types/agents.js";

const DEFAULT_STATUSES = ["open", "in_progress", "waiting", "done"];

/* ── board + tickets ─────────────────────────────────────────────────── */

/** Board options. These are not display filters — they are what the request
    asks for. The board's filter bar sets them, so switching a column off
    stops the server building those cards rather than making the client throw
    them away: a project with hundreds of chats costs the same to poll as a
    small one. */
export type BoardOptions = {
  /** Session rows per card. 0 = counts only. */
  rows?: number;
  /** Only these statuses. Omitted = every column; `[]` = no cards at all. */
  statuses?: string[];
  /** Only this person's tickets. "" = everyone, "me" resolves server-side. */
  assignee?: string;
  /** Ask for the untracked rows. Off unless switched on — the rail is the
      board's most expensive part and stays unbuilt until wanted. Its COUNT
      arrives either way, so it can be offered without being drawn. */
  untracked?: boolean;
  /** One page of the untracked rail. */
  untrackedLimit?: number;
};

export const getProjectTickets = (base: string, projectId: string, opt: BoardOptions = {}) => {
  const q = new URLSearchParams();
  if (opt.rows !== undefined) q.set("rows", String(opt.rows));
  // Set even when empty: "?statuses=" is an explicit "no columns", which is
  // not the same request as omitting it.
  if (opt.statuses !== undefined) q.set("statuses", opt.statuses.join(","));
  if (opt.assignee) q.set("assignee", opt.assignee);
  if (opt.untracked) q.set("untracked", "1");
  if (opt.untrackedLimit !== undefined) q.set("untracked_limit", String(opt.untrackedLimit));
  const qs = q.toString();
  return apiGetE<TicketBoard>(
    `${base}/api/projects/${encodeURIComponent(projectId)}/tickets${qs ? "?" + qs : ""}`,
  ).pipe(
    Effect.map((r) => ({
      ...r,
      tickets: r.tickets ?? [],
      untracked: r.untracked ?? [],
      untracked_total: r.untracked_total ?? 0,
      statuses: r.statuses ?? DEFAULT_STATUSES,
      users: r.users ?? {},
    })),
  );
};

export const createTicket = (
  base: string,
  projectId: string,
  body: {
    title: string;
    status?: string;
    assignee?: string;
    fields?: Record<string, string>;
    session_id?: string;
  },
) => apiPostE<TicketCard>(`${base}/api/projects/${encodeURIComponent(projectId)}/tickets`, body);

export const getTicket = (base: string, ticketId: string) =>
  apiGetE<TicketDetail>(`${base}/api/tickets/${encodeURIComponent(ticketId)}`).pipe(
    Effect.map((r) => ({
      ...r,
      sessions: r.sessions ?? [],
      notes: r.notes ?? [],
      statuses: r.statuses ?? DEFAULT_STATUSES,
      users: r.users ?? {},
    })),
  );

export const updateTicket = (
  base: string,
  ticketId: string,
  patch: {
    title?: string;
    /** Markdown description. An explicit "" clears it. */
    body?: string;
    status?: string;
    assignee?: string;
    fields?: Record<string, string>;
  },
) => apiPatchE<TicketCard>(`${base}/api/tickets/${encodeURIComponent(ticketId)}`, patch);

/** One custom-button click: the server POSTs the ticket to the button's URL
    and reports how that went — the user is waiting to see their "Sync" land. */
export const runTicketAction = (base: string, ticketId: string, buttonId: string) =>
  apiPostE<{ ok: boolean; status?: number; error?: string; attempts?: number }>(
    `${base}/api/tickets/${encodeURIComponent(ticketId)}/actions/${encodeURIComponent(buttonId)}`,
  );

/** Deleting a ticket either keeps its chats (they become untracked) or
    deletes them with it. The destructive shape has to be named: a ticket is
    cheap to recreate, the conversations under it are not. */
export const deleteTicket = (
  base: string,
  ticketId: string,
  sessions: "keep" | "delete" = "keep",
) =>
  apiDeleteE<{ status: string; sessions_deleted: number; sessions_kept: number }>(
    `${base}/api/tickets/${encodeURIComponent(ticketId)}?sessions=${sessions}`,
  );

/** A ticket the moved chat just left, when that emptied it — the caller is
    offered its removal rather than left with a husk on the board. */
export type EmptiedTicket = { id: string; title: string };
type MoveResult = { status: string; emptied_ticket?: EmptiedTicket };

export const attachSession = (base: string, ticketId: string, sessionId: string) =>
  apiPutE<MoveResult>(
    `${base}/api/tickets/${encodeURIComponent(ticketId)}/sessions/${encodeURIComponent(sessionId)}`,
  );

export const detachSession = (base: string, ticketId: string, sessionId: string) =>
  apiDeleteE<MoveResult>(
    `${base}/api/tickets/${encodeURIComponent(ticketId)}/sessions/${encodeURIComponent(sessionId)}`,
  );

/* ── standing answers to ticket prompts ──────────────────────────────── */

export type TicketPrefs = { auto_delete_empty?: "" | "always" | "never" };

export const getTicketPrefs = (base: string) =>
  apiGetE<TicketPrefs>(`${base}/api/me/ticket-prefs`).pipe(Effect.map((p) => p ?? {}));

export const saveTicketPrefs = (base: string, prefs: TicketPrefs) =>
  apiPutE<{ status: string }>(`${base}/api/me/ticket-prefs`, prefs);

/* ── notes ───────────────────────────────────────────────────────────── */

/** A notes scope is a ticket or a session; a session that belongs to a
    ticket resolves server-side to that ticket's notes. */
export type NotesScope = { ticketId: string } | { sessionId: string };

function scopeQuery(scope: NotesScope): string {
  return "ticketId" in scope
    ? `ticket_id=${encodeURIComponent(scope.ticketId)}`
    : `session_id=${encodeURIComponent(scope.sessionId)}`;
}

export const listNotes = (base: string, scope: NotesScope) =>
  apiGetE<NotesResponse>(`${base}/api/notes?${scopeQuery(scope)}`).pipe(
    Effect.map((r) => ({ ...r, notes: r.notes ?? [], users: r.users ?? {} })),
  );

export const addNote = (
  base: string,
  scope: NotesScope,
  body: { body: string; checkable?: boolean; audience?: string },
) => apiPostE<Note>(`${base}/api/notes?${scopeQuery(scope)}`, body);

export const updateNote = (
  base: string,
  scope: NotesScope,
  noteId: string,
  patch: { body?: string; checkable?: boolean; audience?: string; hidden?: boolean; done?: boolean },
) => apiPatchE<Note>(`${base}/api/notes/${encodeURIComponent(noteId)}?${scopeQuery(scope)}`, patch);

export const deleteNote = (base: string, scope: NotesScope, noteId: string) =>
  apiDeleteE<{ status: string }>(
    `${base}/api/notes/${encodeURIComponent(noteId)}?${scopeQuery(scope)}`,
  );

/* ── project config + saved board filter ─────────────────────────────── */

export const saveTicketConfig = (base: string, projectId: string, cfg: TicketConfig) =>
  apiPutE<{ status: string }>(
    `${base}/api/projects/${encodeURIComponent(projectId)}/ticket-config`,
    cfg,
  );

export const getTicketFilter = (base: string, projectId: string) =>
  apiGetE<TicketFilter>(`${base}/api/me/ticket-filters/${encodeURIComponent(projectId)}`).pipe(
    Effect.map((f) => f ?? {}),
  );

export const saveTicketFilter = (base: string, projectId: string, f: TicketFilter) =>
  apiPutE<{ status: string }>(`${base}/api/me/ticket-filters/${encodeURIComponent(projectId)}`, f);

/* ── rail layout (per user) ───────────────────────────────────────────── */

export type RailPrefsWire = { order?: string[]; visible?: number };

export const getRailPrefs = (base: string) =>
  apiGetE<RailPrefsWire>(`${base}/api/me/rail`).pipe(Effect.map((r) => r ?? {}));

export const saveRailPrefs = (base: string, prefs: RailPrefsWire) =>
  apiPutE<{ status: string }>(`${base}/api/me/rail`, prefs);
