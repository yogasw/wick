import { Effect } from "effect";
import { apiGetE, apiPostE } from "@wick-fe/common-api";

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
};

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
