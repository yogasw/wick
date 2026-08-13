import { Effect } from "effect";
import { apiGetE, apiPostE, apiDeleteE } from "@wick-fe/common-api";
import type { Schedule } from "../types/agents.js";

export const listSchedules = (base: string, sessionId: string) =>
  apiGetE<{ schedules: Schedule[] }>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/schedules`,
  ).pipe(Effect.map((r) => r.schedules ?? []));

/* Timing is exactly one of runAt (one-shot), every (interval), or cron.
   Target defaults to this session; naming a project instead runs the schedule
   there, resolving a session per fire (sessionMode). */
export type ScheduleCreate = {
  message: string;
  runAt?: string;
  every?: string;
  cron?: string;
  maxRuns?: number;
  projectId?: string;
  sessionMode?: "existing" | "new" | "template";
  sessionTemplate?: string;
};

export const createSchedule = (base: string, sessionId: string, c: ScheduleCreate) =>
  apiPostE<Schedule>(`${base}/sessions/${encodeURIComponent(sessionId)}/schedules`, {
    message: c.message,
    run_at: c.runAt ?? "",
    every: c.every ?? "",
    cron: c.cron ?? "",
    max_runs: c.maxRuns ?? 0,
    project_id: c.projectId ?? "",
    session_mode: c.sessionMode ?? "",
    session_template: c.sessionTemplate ?? "",
  });

export const cancelSchedule = (base: string, sessionId: string, id: string) =>
  apiDeleteE<{ id: string; status: string }>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/schedules/${encodeURIComponent(id)}`,
  );

export const pauseSchedule = (base: string, sessionId: string, id: string) =>
  apiPostE<Schedule>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/schedules/${encodeURIComponent(id)}/pause`,
  );

export const resumeSchedule = (base: string, sessionId: string, id: string) =>
  apiPostE<Schedule>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/schedules/${encodeURIComponent(id)}/resume`,
  );

export type ScheduleEdit = {
  runAt?: string;
  every?: string;
  cron?: string;
  message?: string;
  maxRuns?: number;
  /* Target edits. Present-but-empty is meaningful (it clears the field), so
     these are only sent when the caller actually set them. */
  projectId?: string;
  sessionMode?: "existing" | "new" | "template";
  sessionTemplate?: string;
};

export const rescheduleSchedule = (
  base: string,
  sessionId: string,
  id: string,
  edit: ScheduleEdit,
) =>
  apiPostE<Schedule>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/schedules/${encodeURIComponent(id)}/reschedule`,
    {
      run_at: edit.runAt ?? "",
      every: edit.every ?? "",
      cron: edit.cron ?? "",
      message: edit.message ?? "",
      ...(edit.maxRuns !== undefined ? { max_runs: edit.maxRuns } : {}),
      ...(edit.projectId !== undefined ? { project_id: edit.projectId } : {}),
      ...(edit.sessionMode !== undefined ? { session_mode: edit.sessionMode } : {}),
      ...(edit.sessionTemplate !== undefined ? { session_template: edit.sessionTemplate } : {}),
    },
  );

/** Fire a schedule now without changing its schedule — the way to test one
    instead of waiting for the clock. */
export const runScheduleNow = (base: string, sessionId: string, id: string) =>
  apiPostE<Schedule>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/schedules/${encodeURIComponent(id)}/run-now`,
  );
