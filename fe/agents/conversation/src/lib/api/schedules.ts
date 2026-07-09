import { Effect } from "effect";
import { apiGetE, apiPostE, apiDeleteE } from "@wick-fe/common-api";
import type { Schedule } from "../types/agents.js";

export const listSchedules = (base: string, sessionId: string) =>
  apiGetE<{ schedules: Schedule[] }>(
    `${base}/sessions/${encodeURIComponent(sessionId)}/schedules`,
  ).pipe(Effect.map((r) => r.schedules ?? []));

/* Timing is exactly one of runAt (one-shot), every (interval), or cron. */
export type ScheduleCreate = {
  message: string;
  runAt?: string;
  every?: string;
  cron?: string;
  maxRuns?: number;
};

export const createSchedule = (base: string, sessionId: string, c: ScheduleCreate) =>
  apiPostE<Schedule>(`${base}/sessions/${encodeURIComponent(sessionId)}/schedules`, {
    message: c.message,
    run_at: c.runAt ?? "",
    every: c.every ?? "",
    cron: c.cron ?? "",
    max_runs: c.maxRuns ?? 0,
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
    },
  );
