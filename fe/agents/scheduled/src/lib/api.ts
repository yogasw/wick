import { Effect } from "effect";
import { apiGetE, apiPostE } from "@wick-fe/common-api";

/** How the delivery target is resolved at each fire. */
export type SessionMode = "existing" | "new" | "template";

export type Schedule = {
  id: string;
  session_id: string;
  session_label?: string;
  created_by: string;
  kind: string; // once | recurring
  run_at: string;
  status: string; // pending | active | done | cancelled | failed
  message: string;
  run_count: number;
  paused?: boolean;
  interval_ms?: number;
  cron?: string;
  max_runs?: number;
  ends_at?: string;
  last_run_at?: string;
  last_error?: string;

  /* Scope. "existing" nudges session_id; "new" / "template" run in
     project_id and resolve a session per fire. */
  session_mode: SessionMode;
  project_id?: string;
  project_name?: string;
  session_template?: string;
  last_session_id?: string;
  last_session_label?: string;
  /* The session this schedule was created from — for a project job it is the
     only link back to that conversation. */
  source_session_id?: string;
  /* Fires triggered by Run now. Counted separately from run_count so
     "ran 3x" stays the number of SCHEDULED fires (what max_runs caps). */
  manual_runs?: number;
  /* Zone a cron expression is matched in (the server's). Present on cron
     rows only. */
  cron_timezone?: string;
};

/** Fields a live schedule's target/timing edit may change. */
export type ReschedulePatch = {
  run_at?: string;
  every?: string;
  cron?: string;
  message?: string;
  max_runs?: number;
  project_id?: string;
  session_mode?: SessionMode;
  session_template?: string;
};

export const isProjectScoped = (s: Schedule) => s.session_mode !== "existing";

export const listAll = (base: string) =>
  apiGetE<{ schedules: Schedule[] }>(`${base}/scheduled/all`).pipe(
    Effect.map((r) => r.schedules ?? []),
  );

export const cancelById = (base: string, id: string) =>
  apiPostE<Schedule>(`${base}/scheduled/${encodeURIComponent(id)}/cancel`);

export const pauseById = (base: string, id: string) =>
  apiPostE<Schedule>(`${base}/scheduled/${encodeURIComponent(id)}/pause`);

export const resumeById = (base: string, id: string) =>
  apiPostE<Schedule>(`${base}/scheduled/${encodeURIComponent(id)}/resume`);

export const rescheduleById = (base: string, id: string, patch: ReschedulePatch) =>
  apiPostE<Schedule>(`${base}/scheduled/${encodeURIComponent(id)}/reschedule`, patch);

/** Fire a live schedule now without changing its schedule — the way to test
    one instead of waiting for the clock. */
export const runNowById = (base: string, id: string) =>
  apiPostE<Schedule>(`${base}/scheduled/${encodeURIComponent(id)}/run-now`);

export type ProjectOption = { id: string; name: string };

/** Projects the caller may target, for the scope editor's picker.
    `/projects/options` returns a bare array (already access-filtered). */
export const listProjects = (base: string) =>
  apiGetE<ProjectOption[]>(`${base}/projects/options`).pipe(Effect.map((r) => r ?? []));
