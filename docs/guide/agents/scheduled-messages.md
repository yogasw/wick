---
outline: deep
---

# Scheduled Messages

A **scheduled message** injects a message into an agent session at a future time — one-shot or recurring — without spinning up the [workflow engine](/workflow/). Use it for "check back in 20 minutes," a daily standup nudge, or any cadence a chat session should just remember on its own.

A schedule can also be **project-scoped** instead of tied to one session: each fire then opens its own session in that project, so a recurring job starts from clean context every run. See [Scope: where a fire lands](#scope-where-a-fire-lands).

::: info Source
Store + runner + recurrence: [`internal/agents/schedule/`](https://github.com/yogasw/wick/blob/master/internal/agents/schedule) — [`store.go`](https://github.com/yogasw/wick/blob/master/internal/agents/schedule/store.go) (CRUD + atomic claim), [`runner.go`](https://github.com/yogasw/wick/blob/master/internal/agents/schedule/runner.go) (poll + deliver), [`recurrence.go`](https://github.com/yogasw/wick/blob/master/internal/agents/schedule/recurrence.go) (timing grammar), [`target.go`](https://github.com/yogasw/wick/blob/master/internal/agents/schedule/target.go) (scope + per-fire target).
Row: [`entity.ScheduledMessage`](https://github.com/yogasw/wick/blob/master/internal/entity/scheduled_message.go).
MCP tool: [`internal/mcp/handlers/schedule_message.go`](https://github.com/yogasw/wick/blob/master/internal/mcp/handlers/schedule_message.go).
Web UI: [`internal/tools/agents/session_schedule_handler.go`](https://github.com/yogasw/wick/blob/master/internal/tools/agents/session_schedule_handler.go).
:::

## Mental model

A schedule is a row that says "deliver this text into this session at this time." When it fires, the runner sends the message through the **same pool path a channel message takes** — `role=user`, `source="schedule"` — so it spawns the session if idle, or queues behind an in-flight turn if busy. There's no separate execution engine; the delivered message is indistinguishable from a normal inbound message once it lands.

```
schedule row (run_at, message, session_id)
        │
        ▼  runner polls every 30s, claims due rows atomically
Pool.SendWithProject(session_id, agent, source="schedule", role="user", message)
        │
        ▼
same spawn/queue/resume path as a channel message
```

## Creating a schedule

Three surfaces write to the same store:

| Surface | Who | Where |
|---|---|---|
| `wick_schedule_message` MCP tool | The agent, scheduling itself or another session it owns | Any MCP client wired to wick |
| **Scheduled** tab on a session | The human viewing that session | Session detail page, rail tab |
| **Scheduled** monitor page | The human, across every session they can see | `/tools/agents/scheduled` |

All three enforce the same access rule: **owner-or-admin** for a session-scoped schedule (`OwnerUserID` is copied from the target session's `Meta.UserID` at create time), and **project access** for a project-scoped one (owner, tag share, or a shared untagged project — the row's owner comes from the project).

## Scope: where a fire lands

`session_mode` decides how the delivery target is resolved at each fire. It is the only thing that differs between a "nudge" and a "job":

| `session_mode` | Needs | Each fire delivers into |
|---|---|---|
| `existing` (default) | `session_id` | That same session, every time. History accumulates. |
| `new` | `project_id` | A **freshly generated** session, `sch-<schedule>-<run>` (a manual run gets `sch-<schedule>-manual-<n>`, so it can't be mistaken for a scheduled one). Clean context every run. |
| `template` | `project_id` + `session_template` | The session named by rendering the template. Fires that render the same name share one session. |

The mode is inferred when you name only one target, so the common cases stay a single argument: `session_id` alone → `existing`; `project_id` alone → `new`.

```
existing  ──▶ sess-abc      ──▶ sess-abc      ──▶ sess-abc       (one long thread)
new       ──▶ sch-a1b2c3-1  ──▶ sch-a1b2c3-2  ──▶ sch-a1b2c3-3   (fresh each run)
template  ──▶ daily-08-13   ──▶ daily-08-13   ──▶ daily-08-14    (grouped by {date})
```

For the project-scoped modes the runner calls the pool's idempotent `EnsureSession` before sending: it creates the session when absent and reuses it when the name already exists. A project-scoped schedule therefore **cannot be orphaned by session reaping** — unlike `existing`, where a target session that has been reaped fails the schedule and stops it.

### A project job belongs to the project, not to one session

A project job is visible and manageable from **every session in its project**, not just the conversation that created it. Switching to a sibling session shows the same job, with the same Pause / Cancel / edit controls.

That is what makes the scope useful: the job is the project's, so it shouldn't vanish because you moved to another chat. Concretely, a session's schedule list matches four ways — the schedules aimed at it (`session_id`), the ones created from it (`source_session_id`), the ones whose last fire landed in it (`last_session_id`), and **the project jobs of its own project** (`project_id`). The by-id actions admit exactly the same set, so a row that is listed can always be acted on.

One consequence worth knowing: **re-pointing a job at another project moves it out of the current one**, so it disappears from the list you were looking at. The edit dialog warns before saving that change.

### Template placeholders

Rendered against the fire time in **UTC**, so a schedule's session names don't shift with the server's timezone. The rendered name must be a legal session id (`[A-Za-z0-9._-]`), and that is checked at **create** time by rendering the pattern once — a pattern that could never work fails immediately rather than on some later fire.

| Token | Expands to | Example |
|---|---|---|
| `{date}` | `2006-01-02` | `2026-08-13` |
| `{datetime}` | `2006-01-02-1504` | `2026-08-13-0900` |
| `{ym}` | `2006-01` | `2026-08` |
| `{run}` | fire number | `7` |
| `{id}` | short schedule id | `a1b2c3d4` |

A template with **no** placeholder (e.g. `nightly-build`) is legal and means "always this one session" — the way to express "reuse an existing session" without pinning a session id.

### Concurrency

No locking is added. With `new`, a slow run and the next fire land in different sessions and simply run in parallel, bounded by the pool's own slots. With `template` (or `existing`), the second fire FIFO-queues behind the first in the same session — the pool's normal behavior for any inbound message.

### Changing scope later

`reschedule` can repoint a project-scoped schedule (another project, `new`↔`template`, a fixed pattern) **and** move a schedule between session and project scope — name a `project_id` to make a nudge into a job, or a `session_id` to pin a job to one conversation.

A scope move re-homes the row, so it is authorized like a fresh create against the new target and the row's owner is re-stamped to match (a session's owner vs a project's). Without that re-stamp the schedule would keep the old scope's owner and drop out of its own listings. A target you can't reach is refused, so reschedule can't be used to park work inside someone else's project.

The one thing that still cannot change is the **kind**: a one-shot cannot become recurring, or vice versa. Cancel and create a new one.

::: tip Moving to session scope needs a session
The global **Scheduled** page has no session in context, so moving a job *into* session scope is only possible from a session's Scheduled tab (which supplies the target). Project-side edits work from either surface.
:::

## Testing a schedule: `run_now`

Waiting for the clock is a poor way to check that a schedule does what you meant. `run_now` fires a live schedule immediately:

```json
{ "action": "run_now", "id": "sm_..." }
```

It makes the row due and nudges the runner, so the fire lands in seconds rather than at the next poll.

A manual run is a **dry run**: an extra fire that leaves the schedule alone.

| | Manual run (`run_now`) | Scheduled fire |
|---|---|---|
| Message delivered | yes | yes |
| `run_count` (what `max_runs` caps) | **not** incremented | incremented |
| `manual_runs` | incremented | — |
| Next fire | **unchanged** | advances to the next slot |
| `last_run_at` | stamped (it did happen) | stamped |

So a `max_runs: 1` schedule still gets its one scheduled fire after you have tested it, and a one-shot still fires on its own schedule after a manual run. A paused schedule is resumed by the call, since asking to run it now plainly means "run it".

Implementation note: it moves `run_at` to now (parking the real next fire in `pending_run_at`) rather than delivering directly, so a manual run goes through the same atomic claim as every other fire — it can't double-fire against a concurrent tick. The **Scheduled** page and a session's Scheduled tab both expose it as a **Run now** action on live rows.

## Cadence is absolute

For an interval schedule, fire N is `anchor + N × interval`, where the anchor is the first fire. The series is fixed when the schedule is created, and **nothing that happens to an individual fire moves it**:

- A fire that lands a few seconds late (see [Delivery timing](#delivery-timing)) does not push the next one later.
- Pausing and resuming lands on the slot the schedule would have hit anyway. Resume an hourly schedule at 10:50 and the next fire is 11:00, not 11:50.
- A manual run doesn't shift anything.

Each of those used to re-anchor the series permanently: an hourly schedule fired 4 seconds late drifted 4 seconds every hour, and a single pause could move a "9am" job to 9:47 forever. Cron schedules never had the problem — the expression *is* the series.

Rows created before the anchor existed have none, and keep the old behavior (step from the last fire) rather than being retrofitted onto a guessed series.

## Delivery timing

The runner polls every 30 seconds, so a fire lands **anywhere from ~0 to ~30 seconds after its nominal time** — the lag is the distance to the next tick, not a constant. A schedule due just before a tick fires almost immediately; one due just after waits nearly a full interval. It is not a real-time scheduler; don't build anything needing second-level precision on it.

`run_now` pokes the poller directly, so a manual fire lands within a second or two rather than waiting out the interval.

Note that lateness does **not** accumulate: the next fire is computed from the schedule's series, not from when the last one landed. See [Cadence is absolute](#cadence-is-absolute).

## Statuses

| Reported `status` | Meaning |
|---|---|
| `pending` | one-shot, hasn't fired yet |
| `active` | recurring, live |
| `paused` | live but suspended — still holds its place in the series, resumable |
| `done` | finished (one-shot fired, or recurring hit `max_runs` / `ends_at`) |
| `cancelled` | stopped for good |
| `failed` | delivery failed (see `last_error`) |

`paused` is a **reported** status, not a stored one: internally a paused schedule stays `active` with a `paused` flag, because it keeps its slot in the cadence and resumes into it. The API folds the flag in so a caller reading only `status` never sees "active" for something that won't fire, and can filter on `status=paused`. Treat it as live — it is resumable, and `pending`/`active`/`paused` together are the live set.

## Timing grammar

Exactly one of three fields decides the cadence — the same parser (`schedule.ParseWhen`) backs the tool, the UI, and reschedule:

| Field | Kind | Format | Example |
|---|---|---|---|
| `run_at` | one-shot | RFC3339, or a relative duration (`+` prefix optional) | `2026-07-09T12:40:00Z`, `+30s`, `+90m`, `2h`, `1d` |
| `every` | recurring, fixed interval | Go duration + `d` for days | `30s`, `5m`, `90s`, `1h30m`, `1d` |
| `cron` | recurring, cron schedule | 5-field (`min hour dom mon dow`) | `0 9 * * 1` (every Monday 09:00) |

Durations accept seconds through days (`30s`, `5m`, `2h`, `1d`) — handy for testing, where a `+30s` one-shot beats waiting out a realistic delay.

::: warning Cron runs in the server's timezone
A cron expression is matched against the **wick server's local wall clock**, not UTC and not the viewer's timezone. `0 9 * * 1` means 09:00 wherever wick runs, so a server in UTC and a user in WIB are 7 hours apart on the same expression. The parser has no `tz` parameter yet.

You don't have to guess which zone that is: every cron schedule reports it. The create response names it in its note, and the row carries `cron_timezone` (e.g. `Asia/Jakarta (UTC+07:00)`), which the detail dialog shows under the cadence. The offset is rendered for the schedule's own fire time, so it reflects DST where that applies.

Template placeholders are the exception — those render in UTC (see below), so a schedule's session names don't shift with the server's zone.
:::

A bare duration in `run_at` (no `+`) is treated as "from now" — the forgiving path, since a user typing `1m` almost always means "1 minute from now," not a literal timestamp. Setting more than one of `run_at`/`every`/`cron` (when the extra one is `every`/`cron`) is rejected.

For a recurring schedule, `run_at` can still be paired with `every` to pick an explicit first fire; without it, the first fire is `now + every` (interval) or the next matching cron minute.

## Lifecycle

```
once:      pending ──deliver──▶ done
                    └─fail────▶ failed

recurring: active ──deliver──▶ active (rescheduled) … ──▶ done (max_runs / ends_at)
                   └─fail─────▶ failed
active ──pause──▶ active(paused) ──resume──▶ active
any live state ──cancel──▶ cancelled
```

| Action | Applies to | Effect |
|---|---|---|
| `create` | — | New row, `pending` (once) or `active` (recurring). |
| `cancel` | any live schedule | Permanently stops it. Terminal. |
| `pause` | recurring only | Suspends firing without deleting the row. |
| `resume` | recurring only | Clears pause, recomputes the next `run_at` from now. |
| `reschedule` | any live schedule | Changes timing / message / `max_runs`. Cannot change kind — cancel and recreate to switch once ↔ recurring. |

A recurring schedule stops on its own when `RunCount` reaches `max_runs`, or when the next `run_at` would pass `ends_at` (when set). Either path lands on `done`, same terminal state a one-shot reaches after its single fire.

## The runner

One goroutine, started from `Server.Run` when both the schedule store and the agent pool exist:

- **Poll interval**: 30 seconds (matches the channel-config hot-reload cadence).
- **Boot recovery is implicit** — the runner fires once immediately on start, so anything that came due while wick was down gets delivered on the first tick. No separate catch-up pass.
- **Claim batch**: up to 50 due rows per tick, so a long-downtime backlog drains in bounded chunks instead of one burst.
- **Atomic claim**: `ClaimDue` guarantees each row fires at most once even across overlapping ticks or a second wick instance pointed at the same DB.
- **Delivery failure** (send error, or — in `existing` mode — the target session no longer exists) marks the row `failed` with the error text in `last_error` and does **not** retry. This applies to recurring schedules too: a session that's gone stops the whole schedule rather than spinning forever. The project-scoped modes create their target, so they never fail this way.
- **Project resolution is live** for `existing`: the target session's `ProjectID` is read fresh at delivery time, not cached at create time, so a session that changed projects still lands in the right `cwd`. Project-scoped fires use the schedule's own `project_id`.
- **`last_session_id`** records where the most recent fire landed, so the UI can link a project job to its actual run.

## `wick_schedule_message` (MCP tool)

Access-gated the same way as the UI. `action` selects the operation; parameters vary by action.

| `action` | Required | Optional |
|---|---|---|
| `create` | `message`, one of `run_at`/`every`/`cron`, and a target: `session_id` **or** `project_id` | `session_mode`, `session_template`, `max_runs`, `agent_name`, `created_by`, `source_session_id` |
| `list` | — | `session_id`, `project_id` (filters), `status`, `limit` |
| `cancel` | `id` | — |
| `pause` | `id` | — |
| `resume` | `id` | — |
| `reschedule` | `id` | `run_at`/`every`/`cron`, `message`, `max_runs`, and target: `session_id`, `project_id`, `session_mode`, `session_template` |
| `run_now` | `id` | — |

`list` returns **live schedules only** by default (`pending,active`) and truncates each row's message to `message_preview`, so asking "what is scheduled?" doesn't drag back every cancelled schedule from months ago with its full prompt. Pass `status=all` (or a comma-separated subset like `done,failed`, or `paused` for just the suspended ones) and `limit` (default 50, max 500) to widen it; the response carries a `note` when the cap was hit, so a truncated list never reads as complete.

Ordering is three tiers, so the top of the list is always the useful part:

1. **live**, soonest fire first — what happens next
2. **finished that actually ran**, most recent first — real history
3. **terminal that never fired** (cancelled before its first run) — last

Tier 3 exists because cancelling a never-fired schedule leaves its future fire time on the row; sorting on that once ranked "cancelled, never ran" above schedules that had just fired, and with a small `limit` pushed the real history out entirely.

### Two session filters

| Arg | Matches |
|---|---|
| `session_id` | everything **related** to that session: schedules targeting it, project jobs created from it, project jobs of its project, and schedules whose last fire landed in it |
| `target_session_id` | only schedules that **deliver into** it |

The broad one is the useful default for "show me this conversation's schedules", but it can't answer "what will actually land here?" — a project job created from a session matches it while delivering somewhere else entirely. Use `target_session_id` for that.

Nudge the conversation you're already in:

```json
{
  "action": "create",
  "session_id": "9b7e-...",
  "run_at": "+20m",
  "message": "Check the deploy status and report back."
}
```

A recurring job in a project, fresh session each run (`session_mode` defaults to `new` when only `project_id` is given):

```json
{
  "action": "create",
  "project_id": "proj-reports",
  "cron": "0 9 * * 1",
  "message": "Write this week's report from the repo activity."
}
```

Fires within the same day sharing one session:

```json
{
  "action": "create",
  "project_id": "proj-ops",
  "session_mode": "template",
  "session_template": "daily-digest-{date}",
  "every": "4h",
  "message": "Append anything new since the last check."
}
```

An agent typically passes its own `session_id` from conversation context — "check back at 12:40" is the agent scheduling itself. When it creates a *project* job instead, passing `source_session_id` keeps the job traceable to (and manageable from) the conversation that set it up. `message` is capped at 8000 characters. `created_by` defaults to `"ai"` for this tool; the dashboard's own create path stamps `"user"` directly.

### List scope

`list` is scoped per-caller: a plain user (or admin) sees only schedules they own. Only the app super-user (`CanSeeAllSessions`) sees every owner's schedules over this transport. A cross-user *admin* view is the **Scheduled** monitor page's job — it additionally reads the `admin_see_all` config knob (see below), which this MCP transport does not carry.

## Scheduled tab (session UI)

Every session detail page has a **Scheduled** rail tab, next to Context / Process / Workspace / Source. The tab badge shows the count of `pending` schedules on that session.

- **Create** — pick **Once** (preset offsets: 20 min / 1 hour / 5 hours / tomorrow / custom) or **Repeat** (preset intervals, custom interval, or a raw cron string), choose **Runs in** (this session / new session each run / named session), write the message, submit. The **Runs in** block only appears when you can reach at least one project, and pre-selects this session's own project.
- **List** — every schedule on this session, plus project jobs created from it. Recurring rows show cadence (`every 5m` / `cron 0 9 * * 1`), next/last fire time, and run count (`3/10×` when `max_runs` is set). A project job carries a badge naming where it delivers, since it does *not* land in this conversation.
- **Actions** — Pause/Resume (recurring only) and Cancel, inline per row.

## Scheduled monitor (global page)

The **Scheduled** sidebar item (`/tools/agents/scheduled`) lists every schedule the caller can see — the cross-session view the per-session tab doesn't give you.

- **Stat tiles**: Live, Recurring, Project jobs, Failed counts (computed over the full set, before filtering).
- **Filter tabs**: Live / Done / Failed / Cancelled / All, plus a **scope** selector (All scopes / Project jobs / Session nudges).
- **Grouped by target** — project jobs group under their project (listed first), session nudges under their session with a link to it. A project job's row links to the session its last run landed in.
- **Inline actions** — Pause / Resume / Cancel, and **Edit scope** on a project job (change project, switch between new-session-per-run and a named-session pattern, with a live preview of the resulting session name).
- **Auto-refresh** every 15 seconds.

### Access

Visibility branches on scope, because a project job has no target session to check:

- **Session-scoped** rows reuse the exact session-visibility filter the sidebar uses (`callerProjectAccess.allowSession`): a user sees schedules for sessions they own or reach via a project.
- **Project-scoped** rows are gated on the project itself (`allowProject`) — the project *is* the access boundary.

Either way, the same check backs both the listing and the by-id actions, so a row the page can show is a row the page can act on. An admin sees **all** schedules only when the `admin_see_all` config (`Configs` → `agents` group) is enabled — otherwise an admin is scoped like a regular user, matching the rest of the agents surface's "admins don't see everything by default" rule.

## Delivery shows up live

A schedule firing into a session that's open in the web UI appears immediately, not just after a refresh — see [Channels ▶ SSE event vocabulary](./channels#sse-event-vocabulary) for the `user_message` event that carries it, and the "⏰ Scheduled" badge rendered above the bubble.

## See also

- [Channels](./channels) — the same pool send path a schedule delivery takes; `source="schedule"` badges the same way a channel's `source="slack"` does.
- [Pool & Sessions](./pool) — spawn/queue/resume mechanics a delivered schedule triggers.
- [MCP for LLMs](../mcp) — the meta-tool pattern `wick_schedule_message` sits alongside.
