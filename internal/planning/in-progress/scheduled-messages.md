# Scheduled Messages (reminders + recurring)

A way to inject a message into an existing agent session at a future time —
without the workflow engine. Two shapes:

- **One-shot** — "besok jam 9 kabarin", "cek deploy in 20m". Fires once → `done`.
- **Recurring** — "per 5 menit cek Loki", "tiap Senin jam 9 report". Fires on a
  schedule until cancelled. Written as an interval (`every 5m`) OR a cron
  expression (`0 9 * * 1`).

The agent can schedule itself, or a user does it from the session's Scheduled
tab. Delivery reuses the normal pool send path, so a fired schedule behaves
exactly like a regular inbound message. Every schedule is bound to a session
(no session → no schedule); if the session is gone at fire time the schedule
errors and auto-cancels.

## TODO (v2 — recurring)

- [x] Extend `entity.ScheduledMessage`: `kind`, `interval_ms`, `cron`,
      `last_run_at`, `run_count`, `max_runs`, `ends_at`, `paused`. `run_at` is
      the next fire (one-shot single / recurring advanced).
- [x] Store: optimistic-lock claim (park run_at), Finalize (done / advance),
      SetPaused, Reschedule, Cancel across live statuses.
- [x] Runner: recurring advance after delivery; one-shot → done; missing
      session or send failure → error + stop (auto-cancel).
- [x] Cron: internal 5-field parser + `nextCronAfter` forward scan; interval +
      day-unit parser in `ParseWhen`. No new dependency.
- [x] MCP tool: pause / resume / reschedule actions; create takes
      every / cron / max_runs.
- [x] HTTP handler + routes: pause/resume/reschedule.
- [x] FE: SchedulePanel once/repeat toggle, interval presets + cron field,
      per-row pause/resume + cancel, cadence + run-count/next/last line.
- [x] Tests: Go (claim/finalize/advance/cron/pause-resume/parse) + FE
      (api lifecycle, panel modes/actions).
- [x] Global cross-session monitor: new `scheduled` SPA (fe/agents/scheduled)
      + "Scheduled" sidebar item; `/scheduled` page, `/scheduled/all` list
      (access = callerProjectAccess.allowSession — owner's own + admin only
      when admin_see_all), `/scheduled/{id}/{cancel,pause,resume}` by-id.
      Filter by status + group by session + next/last/count + inline actions
      + 15s auto-refresh. Store.ListAll added.
- [x] Align MCP scheduleScope with the UI rule: only the app owner
      (CanSeeAllSessions) enumerates all owners; a plain admin is scoped to
      their own (cross-user view = UI monitor, which reads admin_see_all).
- [ ] Docs page + changelog — NEXT

## Done (v1 — one-shot, shipped)

- [x] `entity.ScheduledMessage` + migration; store (repo) + runner (poll ticker)
- [x] Runner wired in `server.go` with `*pool.Pool`; boot recovery via first tick
- [x] MCP tool `wick_schedule_message` (create / list / cancel), owner+admin gated
- [x] System prompt: teach the agent to schedule itself
- [x] HTTP handler + routes (list/create/cancel); FE api + SchedulePanel + rail tab
- [x] Go + FE tests

## Recurring — the "cron vs interval" call

Both, one column each. `kind="recurring"` carries EITHER `interval_ms` (simple,
the common case) OR `cron` (5-field spec, for "tiap Senin jam 9"). `run_at` /
`next_run_at` is always the concrete next fire time the runner claims on — so
the poll loop stays identical; only "what to set next_run_at to after a fire"
branches on interval-vs-cron. This keeps the runner dumb (claim rows where
next_run_at <= now) and puts the recurrence math in one `advance()` helper.

Not a workflow node, not the workflow Cron trigger — this is the standalone
session-nudge primitive. (The workflow engine has its own cron for graph runs.)

## Data model

Table `scheduled_messages`:

| Column | Type | Holds |
|---|---|---|
| `id` | string (uuid) PK | schedule id (`sm_<uuid>`) |
| `session_id` | string, indexed | target session to deliver into |
| `owner_user_id` | string | **provenance**: who requested it. Copied from the target session's `Meta.UserID` at create time (falls back to the creating caller's id). Access + dashboard scoping key. |
| `created_by` | string | `"ai"` \| `"user"` \| `"api"` — how it was made |
| `source_session_id` | string, nullable | if an agent scheduled itself, the session it ran in (usually == session_id, but kept explicit so "who asked" survives even if it targets another session) |
| `agent_name` | string | pool agent to route to; default `"main"` |
| `message` | text | the prompt to inject as role=user |
| `run_at` | time, indexed | when to fire (UTC) |
| `status` | string | `pending` \| `sent` \| `cancelled` \| `failed` |
| `attempts` | int | fire attempts (for at-least-once + backoff cap) |
| `last_error` | text | last failure reason when status=failed |
| `created_at` / `updated_at` | time | audit |
| `sent_at` | time, nullable | when delivered |

Indexes: `(status, run_at)` for the runner scan, `(owner_user_id, status)` for the dashboard.

## Delivery path (confirmed against code)

The pool has no HTTP coupling. The workflow agent node already injects from a
goroutine via `pool.SendWithProject(ctx, sessionID, "default", "workflow",
"user", prompt, projectID)` (`internal/agents/workflow/nodes/agent.go:235`).
We do the same with `source="schedule"`:

```
runner fires → pool.SendWithProject(ctx, sess.ID, agentName, "schedule", "user", msg, sess.Meta.ProjectID)
```

- role=`"user"` is what actually spawns/feeds the agent (non-user roles only
  buffer). A dead/idle session → the pool's internal `ensureSession` +
  spawn-or-queue handles it. A busy session → FIFO queue, same as a normal
  message. This is exactly the "queue like now / like a normal send" behavior
  requested.
- Project id resolved from the target session's `Meta.ProjectID` (re-fetch
  live at fire time, not cached at create time).
- The runner holds `*pool.Pool` (already built as `agentsPool` in server.go
  and injected into the MCP handler via `WithPool`). Only functions in the
  HTTP server process — acceptable; scheduling is a server concern.

## Runner

`internal/agents/schedule/runner.go`:

- One background goroutine started from `server.go`, stopped on ctx cancel.
- **Poll model** (simple, restart-safe): every 30s (and once on boot) scan
  `SELECT ... WHERE status='pending' AND run_at <= now()`. For each row:
  claim it (atomic `UPDATE ... SET status='sent', sent_at=now() WHERE id=? AND
  status='pending'` — the affected-rows count is the lock, so two ticks or two
  instances never double-fire), then call the pool send. On send error →
  `status='failed'`, `last_error`, `attempts++` (no infinite retry; capped).
- Poll (not per-row timers): survives restart for free, no timer to rearm,
  and 30s granularity is plenty for "check in hours later". Mirrors the
  existing `Registry.WatchConfigs` 30s cadence so the pattern is familiar.
- Boot recovery is implicit: the first poll picks up every `pending` row whose
  `run_at` has passed while wick was down.

## MCP tool `wick_schedule_message`

Actions (single tool, `action` enum — mirrors `wick_session_workspace` shape):

- `create` — args: `session_id` (target), `run_at` (RFC3339 or `+2h`/`+90m`
  relative), `message`, optional `agent_name`. Returns the schedule id.
- `list` — args: `session_id` (optional; omit = all schedules the caller may
  see). Returns pending+recent schedules.
- `cancel` — args: `id`. Flips `pending` → `cancelled`.

Access control — reuse `canManageSession(caller, sess.Meta.UserID)` from
`title.go` (owner OR admin/see-all). On denial return "session not found"
(no existence leak), same as the title tools. `owner_user_id` is stamped from
`sess.Meta.UserID` at create; `list`/`cancel` filter to rows the caller may
manage. This is the "hak akses + tau asalnya siapa" requirement.

Registration: descriptor in `handlers/tools.go MetaToolDescriptors()`, a
`case "wick_schedule_message"` in `handler.go handleToolsCall` forwarding
`h.pool`, `h.layout`, the new schedule service, `user`. The handler needs
`*pool.Pool`? No — create/list/cancel only touch the store; the **runner**
holds the pool. Handler deps: schedule store + layout + user.

## Dashboard

Under `/tools/agents` (session-scoped area). A "Scheduled" panel/tab:

- Per-owner list (admin sees all): id, target session (link), run_at
  (relative + absolute), status, message preview, created_by, cancel button.
- Cancel → same service as the MCP `cancel`.
- Read model filtered by `owner_user_id` (or all for admin) so provenance is
  visible: every row shows who requested it and from which session.

## Open questions (resolved)

- Persistence: **DB table** ✅
- Busy session behavior: **queue like a normal send** ✅
- Access: **owner + admin** ✅
- Trigger source (v1): agent self-schedule via MCP **and** any caller with
  session access (a user, or an external cron hitting the MCP tool). External
  bash/cron is not blocked — it just authenticates as a user and calls the
  same tool. No separate token-free local CLI in v1; revisit if needed.
