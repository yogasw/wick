import { Effect } from "effect";
import { apiGetE, apiPostE, apiDeleteE } from "@wick-fe/common-api";
import type { Schedule, WatchDryRun } from "../types/agents.js";

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
  /* type "watch": steps poll every `every` (default 10s) until one matches
     or `timeout` (default 24h, "off" = none, no max) passes; message is the
     notification. onMatch "continue" keeps it running and notifies on every
     new match. */
  type?: "message" | "watch";
  steps?: unknown[];
  timeout?: string;
  onMatch?: "stop" | "continue";
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
    ...(c.type === "watch" ? { type: "watch", steps: c.steps ?? [], timeout: c.timeout ?? "", on_match: c.onMatch ?? "stop" } : {}),
  });

/* One dry run of unsaved watch steps, as the caller, against this session.
   Nothing is stored or delivered. */
export const testWatchSteps = (base: string, sessionId: string, steps: unknown[]) =>
  apiPostE<{ run: WatchDryRun }>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/schedules/watch-test`,
    { steps },
  ).pipe(Effect.map((r) => r.run));

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
