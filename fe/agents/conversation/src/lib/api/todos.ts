/*
 * Purpose:    Reads the session's checklists — the live one and the ones
 *             before it — from the server, rather than reconstructing them
 *             from todo tool calls scattered through the trace. The trace
 *             says what was said; this says what is true now.
 * Caller:     DetailView.svelte (rail data) → TodoPanel.svelte
 * Dependencies: @wick-fe/common-api
 */

import { Effect } from "effect";
import { apiGetE } from "@wick-fe/common-api";

export type TodoSubstep = { step: string; status: string };

export type TodoItem = {
  id?: string;
  label: string;
  description?: string;
  status: string;
  substeps?: TodoSubstep[];
};

export type TodoList = {
  items: TodoItem[];
  total: number;
  completed: number;
  done: boolean;
  started_at?: string;
  updated_at?: string;
};

export type TodosResponse = {
  active: TodoList | null;
  /** Newest first. */
  history: TodoList[];
};

/** A server that predates this endpoint answers 404; the caller treats that
    as "no todos" rather than an error, which keeps the tab hidden instead of
    showing a broken panel. */
export const getTodos = (base: string, sessionId: string) =>
  apiGetE<TodosResponse>(`${base}/api/sessions/${encodeURIComponent(sessionId)}/todos`).pipe(
    Effect.map((r) => ({ active: r.active ?? null, history: r.history ?? [] })),
  );
