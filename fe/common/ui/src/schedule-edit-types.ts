/* Shared shapes for ScheduleEditModal, which both the conversation SPA's
   Scheduled tab and the global Scheduled page render. Kept in common-ui
   because the two SPAs have their own Schedule row types (one from
   agents.ts, one from the scheduled SPA's api.ts) that agree on exactly
   these fields — the modal reads that common subset instead of importing
   either SPA's type. */

export type ScheduleSessionMode = "existing" | "new" | "template";

/** The subset of a schedule row the editor needs to render and seed itself. */
export type EditableSchedule = {
  id: string;
  kind: string; // once | recurring
  run_at: string;
  status: string;
  message: string;
  run_count: number;
  created_by?: string;
  paused?: boolean;
  interval_ms?: number;
  cron?: string;
  max_runs?: number;
  last_run_at?: string;
  last_error?: string;
  session_id?: string;
  session_mode?: ScheduleSessionMode;
  project_id?: string;
  project_name?: string;
  session_template?: string;
  last_session_id?: string;
  last_session_label?: string;
  source_session_id?: string;
  manual_runs?: number;
  cron_timezone?: string;
};

/** What the editor emits on save. Only changed fields are populated, so a
    caller can forward it straight to the reschedule endpoint. */
export type SchedulePatchInput = {
  run_at?: string;
  every?: string;
  cron?: string;
  message?: string;
  max_runs?: number;
  project_id?: string;
  session_mode?: ScheduleSessionMode;
  session_template?: string;
};

export type ScheduleProjectOption = { id: string; name: string };

/** True when the schedule resolves its target session per fire (project
    scope) rather than delivering into one fixed session. */
export const isProjectScopedSchedule = (s: { session_mode?: string }) =>
  !!s.session_mode && s.session_mode !== "existing";

/** Human-readable cadence for a recurring schedule ("every 5m", "cron 0 9 * * 1").
    Sub-minute intervals render in seconds: rounding them to minutes first turned
    "every 30s" into "every 1m". */
export function scheduleCadence(s: { cron?: string; interval_ms?: number }): string {
  if (s.cron) return `cron ${s.cron}`;
  if (s.interval_ms) return `every ${formatIntervalMs(s.interval_ms)}`;
  return "recurring";
}

/** Render an interval in the largest whole unit that divides it, matching the
    grammar the server's ParseWhen accepts (so it round-trips into an edit). */
export function formatIntervalMs(ms: number): string {
  if (ms % 86400000 === 0) return `${ms / 86400000}d`;
  if (ms % 3600000 === 0) return `${ms / 3600000}h`;
  if (ms % 60000 === 0) return `${ms / 60000}m`;
  if (ms % 1000 === 0) return `${ms / 1000}s`;
  return `${ms}ms`;
}

/** Mirror of the server's schedule.RenderTemplate (internal/agents/schedule/
    target.go) so the editor can preview the session a fire would land in.
    The backend re-renders and re-validates; this is a preview only. */
export function renderSessionTemplate(tpl: string, nextRun = 1, now = new Date()): string {
  const p = (n: number) => String(n).padStart(2, "0");
  const date = `${now.getUTCFullYear()}-${p(now.getUTCMonth() + 1)}-${p(now.getUTCDate())}`;
  return tpl
    .replaceAll("{datetime}", `${date}-${p(now.getUTCHours())}${p(now.getUTCMinutes())}`)
    .replaceAll("{date}", date)
    .replaceAll("{ym}", `${now.getUTCFullYear()}-${p(now.getUTCMonth() + 1)}`)
    .replaceAll("{run}", String(nextRun))
    .replaceAll("{id}", "abc12345");
}

/** Session ids are directory names server-side, so the same charset the Go
    storage.ValidateSessionID enforces applies to a rendered template. */
export const isLegalSessionID = (id: string) => /^[A-Za-z0-9._-]+$/.test(id);

/** Format an RFC3339 stamp for display, falling back to the raw string. */
export function formatScheduleTime(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}
