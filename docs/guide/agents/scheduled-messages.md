---
outline: deep
---

# Scheduled Messages

A **scheduled message** injects a message into an agent session at a future time — one-shot or recurring — without spinning up the [workflow engine](/workflow/). Use it for "check back in 20 minutes," a daily standup nudge, or any cadence a chat session should just remember on its own.

::: info Source
Store + runner + recurrence: [`internal/agents/schedule/`](https://github.com/yogasw/wick/blob/master/internal/agents/schedule) — [`store.go`](https://github.com/yogasw/wick/blob/master/internal/agents/schedule/store.go) (CRUD + atomic claim), [`runner.go`](https://github.com/yogasw/wick/blob/master/internal/agents/schedule/runner.go) (poll + deliver), [`recurrence.go`](https://github.com/yogasw/wick/blob/master/internal/agents/schedule/recurrence.go) (timing grammar).
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

All three enforce the same access rule: **owner-or-admin**. `OwnerUserID` on the row is copied from the target session's `Meta.UserID` at create time.

## Timing grammar

Exactly one of three fields decides the cadence — the same parser (`schedule.ParseWhen`) backs the tool, the UI, and reschedule:

| Field | Kind | Format | Example |
|---|---|---|---|
| `run_at` | one-shot | RFC3339, or a relative duration (`+` prefix optional) | `2026-07-09T12:40:00Z`, `+90m`, `2h`, `1d` |
| `every` | recurring, fixed interval | Go duration + `d` for days | `5m`, `90s`, `1h30m`, `1d` |
| `cron` | recurring, cron schedule | 5-field (`min hour dom mon dow`) | `0 9 * * 1` (every Monday 09:00) |

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
- **Delivery failure** (send error, or the target session no longer exists) marks the row `failed` with the error text in `last_error` and does **not** retry — this applies to recurring schedules too: a session that's gone stops the whole schedule rather than spinning forever.
- **Project resolution is live**: the target session's `ProjectID` is read fresh at delivery time, not cached at create time, so a session that changed projects still lands in the right `cwd`.

## `wick_schedule_message` (MCP tool)

Owner+admin gated, same as the UI. `action` selects the operation; parameters vary by action.

| `action` | Required | Optional |
|---|---|---|
| `create` | `session_id`, `message`, one of `run_at`/`every`/`cron` | `max_runs`, `agent_name`, `created_by` |
| `list` | — | `session_id` (filter to one session) |
| `cancel` | `id` | — |
| `pause` | `id` | — |
| `resume` | `id` | — |
| `reschedule` | `id` | `run_at`/`every`/`cron`, `message`, `max_runs` |

```json
{
  "action": "create",
  "session_id": "9b7e-...",
  "run_at": "+20m",
  "message": "Check the deploy status and report back."
}
```

An agent typically passes its own `session_id` from conversation context — "check back at 12:40" is the agent scheduling itself. `message` is capped at 8000 characters. `created_by` defaults to `"ai"` for this tool; the dashboard's own create path stamps `"user"` directly.

### List scope

`list` is scoped per-caller: a plain user (or admin) sees only schedules they own. Only the app super-user (`CanSeeAllSessions`) sees every owner's schedules over this transport. A cross-user *admin* view is the **Scheduled** monitor page's job — it additionally reads the `admin_see_all` config knob (see below), which this MCP transport does not carry.

## Scheduled tab (session UI)

Every session detail page has a **Scheduled** rail tab, next to Context / Process / Workspace / Source. The tab badge shows the count of `pending` schedules on that session.

- **Create** — pick **Once** (preset offsets: 20 min / 1 hour / 5 hours / tomorrow / custom) or **Repeat** (preset intervals, custom interval, or a raw cron string), write the message, submit.
- **List** — every schedule on this session, grouped by status. Recurring rows show cadence (`every 5m` / `cron 0 9 * * 1`), next/last fire time, and run count (`3/10×` when `max_runs` is set).
- **Actions** — Pause/Resume (recurring only) and Cancel, inline per row.

## Scheduled monitor (global page)

A new **Scheduled** sidebar item (`/tools/agents/scheduled`) lists every schedule across every session the caller can see — the cross-session view the per-session tab doesn't give you.

- **Stat tiles**: Live, Recurring, Failed counts (computed over the full set, before filtering).
- **Filter tabs**: Live / Done / Failed / Cancelled / All.
- **Grouped by session** — each section header links to that session, with a per-group row count.
- **Inline actions** — Pause / Resume / Cancel directly from the row, no need to open the session.
- **Auto-refresh** every 15 seconds.

### Access

Visibility reuses the exact session-visibility filter the sidebar itself uses (`callerProjectAccess.allowSession`): a user sees schedules for sessions they own or reach via a project. An admin sees **all** schedules only when the `admin_see_all` config (`Configs` → `agents` group) is enabled — otherwise an admin is scoped like a regular user, matching the rest of the agents surface's "admins don't see everything by default" rule.

## Delivery shows up live

A schedule firing into a session that's open in the web UI appears immediately, not just after a refresh — see [Channels ▶ SSE event vocabulary](./channels#sse-event-vocabulary) for the `user_message` event that carries it, and the "⏰ Scheduled" badge rendered above the bubble.

## See also

- [Channels](./channels) — the same pool send path a schedule delivery takes; `source="schedule"` badges the same way a channel's `source="slack"` does.
- [Pool & Sessions](./pool) — spawn/queue/resume mechanics a delivered schedule triggers.
- [MCP for LLMs](../mcp) — the meta-tool pattern `wick_schedule_message` sits alongside.
