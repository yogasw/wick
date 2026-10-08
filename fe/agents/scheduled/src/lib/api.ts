import { Effect } from "effect";
import { apiGetE, apiPostE } from "@wick-fe/common-api";

/** How the delivery target is resolved at each fire. */
export type SessionMode = "existing" | "new" | "template";

export type Schedule = {
  id: string;
  session_id: string;
  session_label?: string;
  created_by: string;
  /* Identity. owner_user_id is whose schedule it is; run_as_user_id is an
     admin override; effective_run_as is what the runner will actually use,
     and effective_run_as_name its display name. Empty effective_run_as means
     the fire is attached to NOBODY and falls back to wick's internal
     principal, which carries no access tags — worth saying out loud. */
  owner_user_id?: string;
  run_as_user_id?: string;
  effective_run_as?: string;
  effective_run_as_name?: string;
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

  /* "message" delivers on every fire; "watch" runs steps with no LLM and
     delivers once when the last step matches. Legacy rows read "message". */
  type?: ScheduleType;
  steps?: WatchStep[];
  step_count?: number;
  /* Latest watch run: matched | pending | error — live from the runner. */
  last_result?: string;
  consecutive_errors?: number;
  /* Watch on_match: "stop" (default) or "continue" (keeps running, notifies
     on new data); notified counts continue notices; no_timeout = no limit. */
  on_match?: "stop" | "continue";
  notified?: number;
  no_timeout?: boolean;
  /* A run of this watch is executing right now. */
  running?: boolean;
  /* Steps revision (1 = as created, +1 per edit). */
  steps_rev?: number;
  /* The caller may edit / test / read this watch (owner or admin). */
  can_edit?: boolean;
};

export type ScheduleType = "message" | "watch";

export type WatchRule = { path: string; op: string; value?: unknown };

export type WatchStep = {
  name?: string;
  kind: "connector" | "bash" | "check";
  tool_id?: string;
  params?: Record<string, unknown>;
  script?: string;
  timeout_sec?: number;
  match?: "all" | "any";
  rules?: WatchRule[];
  fail_rules?: WatchRule[];
  extract?: Record<string, string>;
  on_ok?: "next" | "done";
  on_fail?: "done" | "pending";
};

/** How a step ended: ok→next, ok→done, pending, fail→done, fail→pending. */
export type StepDecision = "ok→next" | "ok→done" | "pending" | "fail→done" | "fail→pending";

export type StopPoint = { index: number; name: string; kind: string; exit_code?: number; decision?: StepDecision };

export type RuleResult = {
  path: string;
  op: string;
  want?: unknown;
  got?: unknown;
  found: boolean;
  ok: boolean;
  fail?: boolean;
};

export type WatchRunSummary = {
  id: string;
  /* Where the run ended and one sentence on why. */
  stopped_at?: StopPoint;
  reason?: string;
  steps_rev?: number;
  dry_run?: boolean;
  run: number;
  started_at: string;
  duration_ms: number;
  result: string;
  manual: boolean;
  outcome?: string;
  error?: string;
  steps: number;
};

export type WatchStepRecord = {
  decision?: StepDecision;
  name: string;
  kind: string;
  exit_code: number;
  ok: boolean;
  duration_ms: number;
  output: string;
  error?: string;
  params?: Record<string, unknown>;
  tool_id?: string;
  stderr?: string;
  rules?: RuleResult[];
};

export type WatchRun = Omit<WatchRunSummary, "steps"> & {
  schedule_id: string;
  extract?: Record<string, unknown>;
  finished_at: string;
  steps: WatchStepRecord[];
};

export const scheduleType = (s: Schedule): ScheduleType => (s.type === "watch" ? "watch" : "message");

/** A rule as people read it: `state.name in [COMPLETED]`. */
export function ruleText(r: WatchRule): string {
  if (r.op === "exists" || r.op === "not_exists") return `${r.path} ${r.op}`;
  const v = Array.isArray(r.value) ? `[${r.value.map(String).join(", ")}]` : String(r.value);
  return `${r.path} ${r.op} ${v}`;
}

/** One line describing what a step does. */
export function stepSummary(st: WatchStep): string {
  switch (st.kind) {
    case "connector":
      return st.tool_id ?? "";
    case "bash": {
      const first = (st.script ?? "").split("\n")[0];
      return first.length > 80 ? first.slice(0, 80) + "…" : first;
    }
    case "check": {
      const join = st.match === "any" ? " OR " : " AND ";
      const parts = [(st.rules ?? []).map(ruleText).join(join)];
      if (st.fail_rules?.length) parts.push("fail if " + st.fail_rules.map(ruleText).join(" OR "));
      return parts.filter(Boolean).join(" · ");
    }
    default:
      return "";
  }
}

/** "12s ago" / "4m ago" / "3h ago" / "2d ago". */
export function ago(iso: string | undefined, now: number = Date.now()): string {
  if (!iso) return "";
  const t = new Date(iso).getTime();
  if (isNaN(t)) return "";
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return `${s}s ago`;
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

/** `step 2 · check 'done?'` for the "stopped at" column. */
export function stopLabel(p?: StopPoint): string {
  if (!p) return "—";
  return `step ${p.index + 1} · ${p.kind} '${p.name}'${p.decision ? " · " + p.decision : ""}`;
}

export const listRuns = (base: string, id: string, result = "") =>
  apiGetE<{ runs: WatchRunSummary[] }>(
    `${base}/scheduled/${encodeURIComponent(id)}/runs` + (result ? `?result=${encodeURIComponent(result)}` : ""),
  ).pipe(Effect.map((r) => r.runs ?? []));

/** One dry run now (optionally with unsaved steps): nothing is delivered. */
export const testWatch = (base: string, id: string, steps?: WatchStep[]) =>
  apiPostE<{ run: WatchRun }>(`${base}/scheduled/${encodeURIComponent(id)}/test`, { steps: steps ?? null }).pipe(
    Effect.map((r) => r.run),
  );

/** Replace a watch's steps; the server validates them in full. */
export const saveSteps = (base: string, id: string, steps: WatchStep[]) =>
  apiPostE<Schedule>(`${base}/scheduled/${encodeURIComponent(id)}/steps`, { steps });

export const getRun = (base: string, id: string, runId: string) =>
  apiGetE<{ run: WatchRun }>(
    `${base}/scheduled/${encodeURIComponent(id)}/runs/${encodeURIComponent(runId)}`,
  ).pipe(Effect.map((r) => r.run));

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

/** Remove a schedule and its run history. */
export const deleteById = (base: string, id: string) =>
  apiPostE<{ deleted: string }>(`${base}/scheduled/${encodeURIComponent(id)}/delete`);
