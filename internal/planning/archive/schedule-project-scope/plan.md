# Schedule: project scope + per-fire session target

Extends the shipped scheduled-messages feature (`internal/agents/schedule/`,
`wick_schedule_message`) so a schedule is no longer forced to nudge one
pre-existing session. A schedule may instead be bound to a **project**, and a
new `session_mode` config decides where each fire lands: the same session as
today, a **freshly generated** session per fire, or a **templated** session id
(reused when it already exists).

Deliberately NOT the workflow engine — this stays the simple "check back
later / run this every Monday" primitive. Same table, same runner, same MCP
tool, same UI; only the target-resolution step is new.

## Known follow-ups (not done)

Nothing here blocks the feature; it all shipped. These are the loose ends worth
picking up next.

- **No retention/prune for terminal schedules.** They accumulate forever. The
  UI hides them behind a Finished tab and `list` defaults to live-only, so it
  isn't visible day-to-day — which is exactly why it will be forgotten.
- **`run_now` has no confirmation** — it fires on click. Fine for a nudge;
  revisit if a schedule can ever do something destructive.
- **The claim park sentinel is a magic ~100-year offset**, duplicated as
  `Store.ClaimDue`'s `AddDate(100, 0, 0)` and `entity.claimParkYears`. Tested
  from both ends, but a nullable `claimed_at` column would express "a fire is
  in flight" honestly instead of encoding it in a fake future date.
- **No `tz` for cron** — deliberate, see Deliberately out of scope. The zone is
  now REPORTED per schedule, which removes the "have to probe it" problem
  without adding per-schedule zones.
- **Legacy interval rows have no `anchor`** and keep the old drift-prone
  stepping. A backfill (anchor = last_run_at or run_at) would fix them, but
  guessing an origin for a row already mid-series risks moving a live
  schedule, so it was left alone deliberately.

## Status: SHIPPED

Implemented and green across four rounds: the original project-scope work, then
three follow-up rounds from live testing (see
[Follow-up round](#follow-up-round-from-live-testing),
[Follow-up round 2](#follow-up-round-2-second-live-test-pass-s1-s7) and
[Follow-up round 3](#follow-up-round-3-third-live-test-pass--reporting-bugs)).

Rounds 2 and 3 were driven by written test reports; round 3 confirmed every
behavior fixed in round 2 still held, and found only reporting defects.

Tests: Go `internal/agents/schedule`, `internal/entity`, `internal/mcp/...`,
`internal/tools/agents`. FE `common/ui` 190/190 (incl. 32 modal),
`agents/scheduled` 22/22, `SchedulePanel` 31/31.

Pre-existing and unrelated, verified by stashing this branch's changes and
re-running on HEAD: 3 conversation-suite failures in `browser.test.ts` /
`SubAgentModal.tokens.test.ts`, and 4 a11y build warnings in
`BrowserPanel.svelte`.

- [x] `entity.ScheduledMessage`: `ProjectID`, `SessionMode`,
      `SessionTemplate`, `LastSessionID` + mode constants, `Mode()`,
      `IsProjectScoped()`, `BeforeCreate` default. `SessionID` lost its
      `not null` so project-scoped rows can omit it.
- [x] `schedule.ResolveTarget(m, firedAt)` in `target.go` — pure, plus
      `RenderTemplate`, `NormalizeTargetSpec`, `ValidateTargetSpec`.
      Table-driven tests incl. per-fire uniqueness, template grouping, UTC.
- [x] Runner: resolves the target, `EnsureSession` for the minted modes,
      stamps `LastSessionID` through `Finalize`. `existing` keeps the
      `session.Load` pre-check and live project resolution.
- [x] Store: `ListFiltered` (project filter + session match — 3 ways in this
      round, 4 after the follow-up added `project_id`), `Finalize` persists
      `last_session_id`, `Reschedule` accepts target edits.
- [x] MCP create/list/reschedule accept the target args; access switches to
      `project.CanAccess`; `scheduleVM` carries the new fields (omitted when
      session-scoped, so existing output is unchanged).
- [x] MCP tool description + arg schema teach the session-vs-project choice.
- [x] HTTP: session routes accept the target on create; `/scheduled/all` and
      the by-id actions are scope-aware; **`/scheduled/{sid}/reschedule`
      added**.
- [x] FE: `SchedulePanel` target selector (this session / new per run /
      named) with live preview; `scheduled` page scope filter, project
      grouping, last-run link, inline scope repointing (later replaced by the
      shared modal — see "UI, second pass").
- [x] Tests: Go (resolve-target matrix, runner mint/reuse/failure paths,
      monitor access matrix, `scheduleBelongsToSession`) + FE (panel target
      modes, row scope rendering, editor save/validate).
- [x] Docs `docs/guide/agents/scheduled-messages.md` + changelog.

### Follow-up round (from live testing)

Nine findings came out of a real end-to-end test run; all valid ones are fixed:

- [x] **Project jobs were session-bound.** Listing matched only
      `session_id` / `source_session_id` / `last_session_id`, so a job vanished
      when you switched to a sibling session. `Store.ListFiltered` now takes a
      `SessionScope{ID, ProjectID}` and matches the session's own
      `project_id` too; `scheduleBelongsToSession` admits the same set so
      listed rows are always actionable.
- [x] **Claim sentinel leaked.** `ClaimDue` parks `run_at` ~100 years out;
      terminal transitions didn't unpark it, so a done row reported
      "next run 2126" and sorted first. `Finalize` / `MarkFailed` / `Cancel`
      now restore `COALESCE(last_run_at, run_at)`, and `entity.NextRunAt()`
      refuses to publish a parked/terminal time at all (belt and braces).
      Both VMs send `next_run_at` (+ `run_at` alias) only while live.
- [x] **`list` was a token bomb.** New `Store.List(ListQuery)` with status /
      since / limit; MCP defaults to live-only, caps at 50 rows (max 500),
      truncates each message to `message_preview`, and notes when the cap hit.
- [x] **Scope move allowed** (user's call, over the original "cancel and
      recreate"). `scheduleTargetPatch` re-authorizes against the NEW target
      and re-stamps `owner_user_id`; `SchedulePatch` gained `SessionID` +
      `OwnerUserID`. Kind flip stays refused. The global page passes `""` as
      the move-to-session target, so moving INTO session scope is only
      possible from a session tab (nothing else would define the target).
- [x] **`run_now`.** `Store.RunNow` sets `run_at=now` + un-pauses, then
      `WakeRunner()` collapses the poll delay. Deliberately NOT a direct
      deliver call: reusing the atomic claim means a manual run can't
      double-fire against a concurrent tick, and there's no second copy of
      the delivery path. Runner gained a buffered `wake` channel and a
      package-level `running` pointer (set in `Run`, not `NewRunner`, so a
      test runner never becomes the process-wide one).
- [x] **Docs: seconds + cron timezone.** `run_at`/`every` always took `30s`;
      now documented. Cron matches the SERVER's wall clock (`t.Hour()` on
      `time.Now()`) — stated explicitly in the tool schema and the docs page,
      with no `tz` param added.
- [x] **Create note renders the template.** `scheduleCreateNote` shows the
      resolved session name (`sched-test-2026-08-13`), not just the pattern,
      so a bad pattern is visible before the first fire.
- [x] Found while writing tests: `scheduleCadence` rendered a 30s interval as
      "every 1m" (rounded to minutes first). Extracted as `formatIntervalMs`
      and fixed — it had been copy-pasted into three components.
- [x] **Session panel buried live rows** under finished ones. Added Live /
      Finished / All tabs (default Live, with counts) and collapsed the create
      form. See "UI, second pass".

### Follow-up round 2 (second live-test pass, S1–S7)

A second end-to-end run produced a written issue list (S1–S7). All of it is
fixed. Two of the findings changed how the feature behaves, not just what it
reports:

- [x] **S2+S3 — cadence is now ABSOLUTE.** These were one decision, not two
      bugs. `advance` added the interval to the time a fire *actually landed*,
      so every source of lateness re-anchored the series: a fire 4s late (poll
      granularity) drifted 4s per hour, and one pause/resume moved a "9am" job
      to 9:47 forever. Now `nextInSeries` steps from a stored `anchor`
      (fire N = anchor + N×interval), computed by division so a month-long
      pause doesn't loop a million times. `NextFrom` (resume) uses the same
      series, so resuming lands on the slot the schedule would have hit
      anyway. Legacy rows have no anchor and keep the old stepping — a wrong
      series is worse than the old behavior. Cron never had the problem.
- [x] **S2 — `run_now` is dry.** It was consuming `max_runs` and shifting the
      cadence while the create note claimed it changed nothing. Now the claim
      carries a `manual_fire` flag: `ClaimDue` counts `manual_runs` instead of
      `run_count`, and the runner restores `pending_run_at` instead of
      advancing. Chose this over rewriting the note because the note described
      the behavior everyone actually wants from a test button.
- [x] **S1 — list ordering.** `run_at DESC` is incoherent across a mixed set
      (live run_at is future, terminal is past) and, with `limit`, cut the
      imminent fires while keeping old history. Replaced by `listOrder`: live
      first by soonest fire, then terminal newest-first. Verified against the
      real ordering in a test, not just eyeballed.
- [x] **S4 — scope move.** Already fixed in round 1; the report caught a
      server running the older build. Added handler tests through
      `scheduleTargetPatch` (both directions, plus refusal for an unreachable
      or nonexistent project) so a regression can't silently restore the
      doc/behavior mismatch.
- [x] **S5 — cron timezone reported, not probed.** Documented in round 1, but
      the only way to *learn* a schedule's zone was to fire a probe. Now
      `schedule.ServerZoneLabel` renders it from the schedule's own fire time
      (so the offset is the one in force, DST included) and both VMs publish
      `cron_timezone`; the create note names it too.
- [x] **S6 — system prompt.** Now mentions seconds (`+30s`), project scope,
      `run_now`, the cron zone caveat, and the poll lag.
- [x] **S7 — `source_session_id` exposed.** It was already recorded and used
      for access; a project job has no `session_id`, so it is the only link
      back to the creating conversation. No new column.
- [x] **Baseline documented, not "fixed".** Fires land 3–6s late because
      delivery is polled every 30s. That is by design, so it is stated in the
      tool description and the docs page rather than chased.

New columns from this round: `manual_runs`, `anchor`, `manual_fire`,
`pending_run_at`. `Finalize` also took a `kind` argument, replacing a
`recurring bool` that could no longer express "a one-shot that still has a
pending fire" (which is what a manually-fired one-shot is).

### Follow-up round 3 (third live-test pass — reporting bugs)

The third pass confirmed every behavior from round 2 (all target modes, all
timing modes, `max_runs`, drift-free cadence, `manual_runs` separation, scope
move, template reuse without leakage) and turned up seven issues, all in what
the API *reports* rather than what it does. All fixed:

- [x] **`run_now` response looked like it moved the schedule.** The manual run
      borrows `run_at` to become due; `store.Get` then returned that borrowed
      value, so the response said "next run: now". `NextRunAt()` now answers
      from `PendingRunAt` while a manual fire is in flight, a
      `manual_fire_pending` field states the extra run separately, and the
      action returns a note. Worth fixing properly rather than documenting: an
      agent reading "the schedule moved" will try to repair what isn't broken.
- [x] **`paused` unreadable / unfilterable.** Stored as a flag beside
      `status=active` (correct — a paused row keeps its slot in the series),
      but callers reading `status` saw "active" for something that won't fire.
      Added `EffectiveStatus()`, reported by both VMs, plus `status=paused` in
      `list` (translated to active + `paused=true`, since it is never stored).
- [x] **Cancelled-never-fired outranked real history.** `Cancel` uses
      `COALESCE(last_run_at, run_at)`, which for a never-fired row keeps a
      FUTURE timestamp — and `listOrder`'s tie-break sorted on it. Re-tiered:
      live → fired-terminal → never-fired. Fixed at the sort key rather than by
      rewriting stored timestamps, which would lose "when was this due".
- [x] **`session_id` filter semantics.** It matches everything RELATED to a
      session (target, creator, last-fire, project sibling) — deliberate, but
      undocumented, and it can't answer "what lands HERE". Documented, and
      added `TargetSessionID` / `target_session_id` for the strict question.
      Access-checked identically so it can't be used to probe.
- [x] **Manual session named `-0`.** The scheduled series is 1-based and a
      manual fire doesn't advance the count, so `strconv.Itoa(RunCount)` gave
      `sch-<id>-0`. Manual fires now use `sch-<id>-manual-<n>`.
- [x] **Lifecycle errors were stateless.** Now name the state:
      "schedule is done — only a live (pending/active) schedule can be changed".
- [x] **Latency claim was optimistic.** "typically 3-6s" was measured off runs
      that happened to be near a tick; the real lag is the distance to the next
      30s tick (~0–30s). Corrected in the tool description and docs, together
      with the fact that lateness doesn't accumulate.

**Regression caught while fixing #4:** reporting `status: "paused"` broke five
FE `status === "pending" || "active"` checks — a paused schedule would have
rendered as terminal and lost its Resume button, i.e. no way to un-pause from
the UI. All five now treat `paused` as live, with tests in each of the three
components.

### UI, second pass

The row is now click-to-open rather than carrying an inline editor:

- `ScheduleEditModal` + `schedule-edit-types.ts` live in
  `@wick-fe/common-ui` (both SPAs render it — the dedup rule). It doubles as
  the detail view: facts first, then the editable fields, read-only once the
  schedule is terminal.
- `fe/agents/scheduled/src/lib/ScopeEditor.svelte` was **deleted**, superseded
  by that shared modal.
- Modal state is keyed by id and re-resolved from the list, so the 15s
  auto-refresh can't remount the dialog mid-edit.
- The clickable summary is a real `<button>`, with the action row and the
  last-run `<a>` kept outside it (no nested interactive elements).
- Moving a job to another project warns first, since it disappears from the
  list being viewed.

The session panel also needed triage, from the same screenshot that prompted
the modal — a session that had run a few schedules showed one long undivided
list, live rows buried under finished ones (the list is newest-fire-first, and
a done row's fire time is in the past, so anything still actionable sank to the
bottom):

- **Live / Finished / All tabs with counts**, default Live. The counts are what
  make the default honest: an all-finished session shows an empty Live list, so
  it has to be visible that history EXISTS rather than looking broken.
- **The create form collapses** (open only on an empty panel, where the form is
  the content) so it stops pushing the list off-screen.

And a bug the extraction surfaced: `cadence()` existed in **three** copies
(panel, scheduled row, and the new modal), all carrying the same defect —
`Math.round(ms / 60000)` rendered a 30-second interval as "every 1m". Replaced
by one `scheduleCadence` / `formatIntervalMs` in `common-ui`, which now formats
in the largest unit that divides the interval exactly (so it also round-trips
back into an edit as valid `ParseWhen` input).

### Deviations from the original plan

- **No `/projects/{id}/schedules` routes.** The existing
  `/tools/agents/scheduled` page covers project jobs via its scope filter, so
  a second listing surface would have been dead weight. Create still happens
  through the session route (the panel lives in a session), and the by-id
  actions were already path-agnostic.
- **`session_mode` is per-schedule config, not a hardcoded rule.** The
  original plan leaned toward always-fresh; the shipped design lets each
  schedule choose, with `template` covering "reuse a session" as a degenerate
  no-placeholder pattern rather than a fourth mode.
- **`source_session_id` participates in access.** A project job created from a
  conversation stays listed *and* actionable in that conversation's panel
  (`scheduleBelongsToSession`), which the original plan didn't account for —
  without it the panel could list a row whose buttons 404. The follow-up round
  then generalized this to the project (see Cross-session visibility);
  `source_session_id` remains the fallback for a session with no project.
- **`session.OriginSchedule` const not added.** `EnsureSession` already turns
  the `source` string into an `Origin`, so passing `"schedule"` was enough; a
  const with no consumer would have been noise.
- **Scope is mutable after all.** The plan's "cancel and create a new one" rule
  survived the first round, then live testing showed it just forced manual
  copy-paste of long messages. Reversed on the user's call: a scope move is now
  allowed, paid for with re-authorization against the new target and an owner
  re-stamp. Only the kind flip stays refused.
- **No inline row editor.** The first round put a `ScopeEditor` under each row;
  it was replaced by a shared modal opened on row click, and the file deleted.

## Why

Before this work `session_id` was required and immutable per schedule
(`internal/mcp/handlers/schedule_message.go:75`). Consequences:

- A recurring schedule pours every fire into one session, so context grows
  without bound and each run sees all prior runs' history.
- "Run this daily in project X" has no expression at all — the user must first
  create a session by hand, then schedule into it.
- If the target session is reaped (session TTL) the schedule hard-fails and
  auto-cancels (`runner.go` `deliver` → `MarkFailed`), which is exactly wrong
  for a long-lived recurring job.

Project scope fixes all three without introducing a second scheduling
subsystem. The third is fixed only for the project-scoped modes — they create
their target, so they cannot be orphaned; `existing` keeps the fail-fast
behavior deliberately, so nothing changes for schedules written before this.

## Data model

Four columns added to `scheduled_messages`. GORM auto-migrate handles it; all
are nullable/defaulted so existing rows keep working untouched.

| Column | Type | Holds |
|---|---|---|
| `project_id` | varchar(64), indexed | project the fire runs in. Required when `session_mode != existing`; optional (informational) otherwise. |
| `session_mode` | varchar(16), default `existing` | `existing` \| `new` \| `template` — how the target session id is resolved at fire time. |
| `session_template` | varchar(128) | id pattern for `session_mode=template`, e.g. `daily-report-{date}`. |
| `last_session_id` | varchar(128) | the session the most recent fire actually landed in. Read-only, for the UI link. |

`session_id` stays as-is and keeps its meaning for `existing`; it is empty for
project-scoped rows. `owner_user_id` for a project-scoped row is copied from
the **project's** owner (falling back to the calling user), not a session.

### Mode semantics

```
session_mode=existing   (default, today's behavior — nothing changes)
  session_id  required at create, must exist and be manageable by caller
  fire        deliver into session_id; vanished session -> failed + stop

session_mode=new        (fresh context every fire)
  project_id  required
  session_id  NOT given at create — GENERATED per fire
  fire        id = "sch-<short(scheduleID)>-<runCount>"
              EnsureSession(id, "schedule", project_id) -> deliver

session_mode=template   (deterministic id, reuse when it already exists)
  project_id  required
  session_template required, e.g. "daily-report-{date}"
  fire        id = render(template, firedAt)
              EnsureSession is idempotent: reuse if the id exists,
              create if not -> deliver
```

`short(scheduleID)` = the uuid's first 8 hex chars from `sm_<uuid>`, so
`sm_a1b2c3d4-...` run 3 → `sch-a1b2c3d4-3`. `RunCount` is already incremented
by `ClaimDue` before the runner sees the row, so it is a stable per-fire
counter and the generated id is collision-free without extra state.

### Template placeholders

Rendered against the fire time, UTC. Everything else is copied literally, then
the whole id is validated with `storage.ValidateSessionID` (`[A-Za-z0-9._-]`)
so a bad pattern fails loudly at **create** time, not at 3am on fire 40.

| Token | Expands to | Example |
|---|---|---|
| `{date}` | `2006-01-02` | `2026-08-13` |
| `{datetime}` | `2006-01-02-1504` | `2026-08-13-0900` |
| `{ym}` | `2006-01` | `2026-08` |
| `{run}` | `RunCount` | `7` |
| `{id}` | `short(scheduleID)` | `a1b2c3d4` |

Validation at create: render the template against `now` and reject if the
result fails `ValidateSessionID`. A template with no time-varying token (e.g.
`nightly-build`) is legal and means "always the same session" — that is the
"reuse an existing session" case, expressed as a degenerate template rather
than a fourth mode.

## Target resolution

New pure function, `internal/agents/schedule/target.go`:

```go
// ResolveTarget maps a claimed schedule + its fire time to the concrete
// session id the message is delivered into. Pure: no DB, no filesystem, so
// the mode matrix is unit-testable. The runner is responsible for actually
// materialising the session (EnsureSession) for the non-existing modes.
func ResolveTarget(m entity.ScheduledMessage, firedAt time.Time) (sessionID string, mint bool, err error)
```

- `existing` → `(m.SessionID, false, nil)`; error when empty.
- `new` → `("sch-"+short(m.ID)+"-"+itoa(m.RunCount), true, nil)`.
- `template` → `(render(m.SessionTemplate, m, firedAt), true, nil)`, then
  `ValidateSessionID`.
- unknown mode → error (runner stops the schedule rather than spinning).

`mint=true` tells the runner to call `EnsureSession` instead of requiring the
session to pre-exist.

## Runner change

`deliver()` in `internal/agents/schedule/runner.go` gains a target step ahead
of the existing send. Sender interface grows one method so the runner can
materialise a session:

```go
type Sender interface {
	SendWithProject(ctx context.Context, sessionID, agentName, source, role, text, projectID string) error
	EnsureSession(ctx context.Context, sessionID, source, projectID string) error // NEW
}
```

`*pool.Pool` already satisfies both (`pool.go:1308`).

```
target, mint, err := ResolveTarget(m, firedAt)
err != nil                      -> MarkFailed(bad target) ; stop
mint:
    EnsureSession(target, "schedule", m.ProjectID)
    err -> MarkFailed ; stop
    projectID = m.ProjectID
!mint (existing):
    sess, err := session.Load(layout, m.SessionID)   // unchanged
    err -> MarkFailed("target session not found") ; stop
    projectID = sess.Meta.ProjectID                   // live resolve, unchanged
SendWithProject(target, m.AgentName, "schedule", "user", m.Message, projectID)
Finalize(..., lastSessionID: target)
```

Session `Origin` for minted sessions: reuse `OriginREST`? No — add
`session.OriginSchedule = "schedule"` so the sidebar/monitor can tell a
scheduler-spawned session from a REST one. `EnsureSession` passes `source`
straight into `session.Origin(source)`, so passing `"schedule"` is enough;
the const is added for the FE's origin switch and for grep-ability.

The pool's own idle-reap and session retention already clean up accumulated
`sch-*` sessions — no new GC here.

## Access control

Project-scoped rows can't use `canManageSession` (there is no session). Rule:

- **MCP** — `scheduleAuthorizeTarget` resolves the target and returns the owner
  to stamp: session scope goes through `canManageSession`, project scope
  through the EXISTING `project.CanAccess` (admin / owner / tag share /
  shared-untagged) rather than a new helper. `scheduleCanManage` gates the
  by-id actions, admitting a project row anyone the project is shared with.
  A missing/unauthorized target returns `project not found: <id>` /
  `session not found: <id>` so existence doesn't leak.
- **UI** — reuse `callerProjectAccess(c).allowProject(projectID)`
  (`internal/tools/agents/handler.go:595`), which already encodes
  owner / tag-grant / ownerless-if-admin.
- **Global monitor** — one function, `scheduleMonitorVM`, decides visibility
  AND builds the row, and both the listing and the by-id actions call it. That
  is deliberate: when the two rules were separate, a row could render with
  buttons that 404.
- **Session panel** — `scheduleBelongsToSession(m, sid, sessionProjectID)`
  must admit exactly what `Store.ListFiltered` lists, for the same reason.

Ownership follows the scope, and a **scope move re-stamps it**
(`SchedulePatch.OwnerUserID`): a session row belongs to the session's owner, a
project row to the project's owner (falling back to the caller for an
ownerless/shared project). Without the re-stamp a moved row would keep the old
scope's owner and vanish from its own listings.

Mode/scope validation at create (fail fast, single place — a shared
`ValidateTargetSpec` used by both the MCP and UI create paths):

- `session_mode=existing` + `session_id` empty → error.
- `session_mode` in {`new`,`template`} + `project_id` empty → error.
- `session_mode=template` + empty/invalid-rendering template → error.
- `session_id` AND `project_id` both set with mode `existing` → project_id is
  ignored (session's own project wins at fire time); no error, but the create
  response notes it.

### Cross-session visibility (a project job is the project's)

A project job is listed and actionable from **every session in its project**,
not only the one that created it. `Store.ListFiltered` takes a
`SessionScope{ID, ProjectID}` and matches four ways:

| Match on | Why a session sees it |
|---|---|
| `session_id` | it is the fixed delivery target |
| `source_session_id` | the schedule was created from it |
| `last_session_id` | the last fire landed in it |
| `project_id` | it is a project job of THIS session's project |

The last one is the point: without it a job was visible only from the
conversation that happened to create it, which contradicts project scope
(this was the bug found in live testing). `project_id` is only ever set on
project-scoped rows, so the extra OR can't pull in a sibling session's nudge.

Consequence worth stating: re-pointing a job at another project moves it OUT of
the current one, so it disappears from the list being viewed. The edit dialog
warns before saving that change.

## MCP surface

`wick_schedule_message` — args added, none removed:

```
action=create
  # target (exactly one shape)
  session_id       string   existing session to nudge (mode=existing)
  project_id       string   project to run in       (mode=new|template)
  session_mode     string   existing|new|template   (default: existing,
                            or "new" when only project_id is given)
  session_template string   id pattern for mode=template
  # timing + payload (unchanged)
  run_at / every / cron / message / max_runs / agent_name / created_by
```

Defaulting rule keeps the simple case one arg: `project_id` alone with no
`session_mode` means `new`. That is the "next bisa buat sesi baru tiap jalan"
default.

`action=list` gains `project_id`, plus `status` and `limit` (see
[Follow-up round](#follow-up-round-from-live-testing) — an unfiltered list was
returning every schedule ever created with its full message text). Default is
live-only (`pending,active`), 50 rows, message truncated to
`message_preview` + `message_truncated`; `status=all` or a comma-separated
subset widens it, `limit` caps at 500. The result carries a `note` when the cap
was hit, so a truncated list never reads as a complete one.

`action=reschedule` may change `session_mode` / `session_template` /
`project_id` / `session_id` — **including moving between session and project
scope**, which the original plan refused. See the follow-up round for why that
changed and what it costs (re-authorization against the new target plus an
owner re-stamp). Changing the *kind* (one-shot ↔ recurring) is still refused.

`action=run_now` fires a live schedule immediately without redefining it —
added so a schedule can be TESTED without waiting for the clock.

`scheduleVM` gains `project_id`, `session_mode`, `session_template`,
`last_session_id` (each omitted when empty, so session-scoped output is
byte-identical to today) and publishes `next_run_at` (with `run_at` kept as an
alias) **only while the schedule can still fire** — see the claim-sentinel fix
in the follow-up round.

Tool description gets the decision rule spelled out, since the agent picks:

> Use `session_id` to nudge a conversation you are already in ("check back in
> 20m"). Use `project_id` for a standalone recurring job that should start
> clean each time ("every Monday 9am, write the weekly report") — each fire
> opens a new session in that project. Use `session_template` when repeated
> fires within the same day/month should share one session.

## HTTP + FE

Backend routes — as shipped (the planned `/projects/{id}/schedules` pair was
dropped; see Deviations):

- `POST /sessions/{id}/schedules` — create, now accepting the target fields
  (`project_id` / `session_mode` / `session_template`).
- `POST /sessions/{id}/schedules/{sid}/{pause,resume,reschedule,run-now}` —
  `run-now` added.
- `POST /scheduled/{sid}/{cancel,pause,resume,reschedule,run-now}` — global
  page, by id; `reschedule` and `run-now` added.
- `GET /scheduled/all` + `GET /sessions/{id}/schedules` — both return the new
  target fields and a `next_run_at` that is empty on a terminal row.

FE:

- **`ScheduleEditModal` + `schedule-edit-types.ts` in `@wick-fe/common-ui`** —
  the detail/edit dialog both SPAs open by clicking a row (dedup rule: two
  consumers → `common/*`). Facts first (status, cadence, next/last run,
  provenance, id, last error), then the editable fields; read-only once the
  schedule is terminal. Warns before moving a job to another project.
- `SchedulePanel.svelte` (in-conversation) — "Runs in" selector on create
  (**This session** default / **New session each run** / **Named session**,
  with the project picker pre-filled from this session's project and a live
  preview of the rendered id). List gained **Live / Finished / All** tabs with
  counts (default Live), and the create form now collapses so the list isn't
  pushed off-screen. Rows are click-to-open, plus a **Run now** action.
- `ScheduleRow.svelte` (global monitor) — scope badge, link to
  `last_session_id`, **Run now**, click-to-open. Falls back to `last_run_at`
  when a terminal row has no `run_at`.
- `App.svelte` (global monitor) — scope selector (All / Project jobs / Session
  nudges), **Project jobs** stat tile, grouping by project for project-scoped
  rows (project groups first), and it owns the single modal instance.
- `api/schedules.ts` / `api.ts` — new fields, `reschedule`, `run-now`.

Two FE details that matter and are easy to get wrong:

- **Modal state is keyed by id and re-resolved from the list**, not held as a
  row copy — otherwise the 15s auto-refresh remounts the dialog and wipes a
  half-typed edit.
- **The clickable summary is a real `<button>`**, with the action row and the
  last-run `<a>` kept OUTSIDE it: nesting interactive elements is invalid, and
  a `<div>` + click handler loses keyboard/screen-reader access.

## Deliberately out of scope

- Per-fire prompt templating (`{date}` inside `message`) — separate ask. Note
  the session-id template is NOT this: it names the target session, not the
  prompt.
- Retry/backoff on delivery failure: unchanged fail-fast, except that
  `new`/`template` modes no longer fail on "session not found" because they
  mint it.
- Concurrency guard for a slow job whose next fire arrives before the previous
  finished. With `mode=new` each fire has its own session so they simply run in
  parallel (bounded by the pool's own slots); with `template` the second fire
  FIFO-queues behind the first in the same session, which is the pool's normal
  behavior. No new locking.
- Workflow-engine integration. Still separate on purpose.
- **A `tz` parameter for cron.** Cron is matched against the SERVER's local
  wall clock (`cronMatchesMinute` reads `t.Hour()` on `time.Now()`). Live
  testing showed this is a real trap — a morning report set in the wrong zone
  is hours off — so it is now stated explicitly in the tool schema and the docs
  page, but no per-schedule timezone was added. Template placeholders are the
  exception and render in UTC, so session names don't drift with the server's
  zone.
- **Pruning finished rows.** They accumulate; the UI hides them behind a
  Finished tab and `list` defaults to live-only, but nothing deletes them yet.
