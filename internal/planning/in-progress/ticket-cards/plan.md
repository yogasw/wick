# Ticket Card System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

Sessions in a ticket-enabled project become tickets: status + custom fields on
session meta, a kanban card view on the project landing, manual editing from the
conversation right rail, agent self-service over MCP, and a sweeper that
auto-followups stale tickets (agent turn) and auto-resolves dead ones.

**Mockup:** `mockup.html` (same folder — keep in sync with this doc).

## TODO

- [x] Task 1: session.Meta.Ticket + helpers
- [x] Task 2: project.TicketConfig + defaults
- [x] Task 3: internal/agents/ticket — decision funcs + sweeper
- [x] Task 4: HTTP API (session ticket, project config, user filter prefs)
- [x] Task 5: MCP wick_ticket_get / wick_ticket_set
- [x] Task 6: FE types + api fns
- [x] Task 7: FE KanbanBoard + TicketCard + filter bar
- [x] Task 8: FE ProjectLanding List|Card toggle
- [x] Task 9: FE TicketPanel (right rail)
- [x] Task 10: FE project-settings "Ticket system" section
- [x] Task 11: Sweeper wiring at server boot + build/test pass

## Status: implemented (2026-08-22)

All tasks landed. Deltas from the plan as written:

- **Duration fields are `int64` seconds** — `followup_after_sec` /
  `auto_resolve_after_sec` (no `Duration` wrapper), as decided in Task 2.
- **Adoption instead of a create-path hook**: the sweeper turns any
  ticket-less session in a ticket-enabled project into an `open` ticket
  (clocked from `LastActive`), which makes ticket mode retroactive with no
  migration. Sub-agent sessions (`ParentSessionID != ""`) are excluded.
- **`apiPutE` added to `@wick-fe/common-api`** — the shared client had no PUT
  helper; the ticket endpoints are the first PUT consumers.
- **Extra endpoint `GET /api/sessions/{id}/ticket`** — the conversation rail's
  Ticket panel needs ticket + project schema + user names in one call.
- **`ProjectSettingsResponse.ticket`** carries the stored config to the
  settings SPA; saving still goes through the dedicated ticket-config PUT.
- **Assignee is a built-in field** (not part of the custom schema), and the
  board's saved filter (statuses / assignee / view mode) lives per user in
  `entity.UserMetadata.TicketFilters`.

Verification: `go build ./internal/... ./cmd/...` clean; Go tests pass for
session, project, ticket, mcp, login, entity; FE `npm run build` + `npm run
test` pass (826 tests — the 2 failures in `browser.test.ts` are a
pre-existing baseline, confirmed by stashing this branch); Tailwind rebuilt;
boot smoke test showed `component=ticketsweep msg=started` and all three
ticket routes reaching their handlers.

**Goal:** Ticket system on top of sessions — kanban cards, per-project field
schema, per-user saved filters, agent-driven followup, auto-resolve.

**Architecture:** Session = ticket (`session.Meta.Ticket`, nil = non-ticket).
Project meta owns the schema + timers (`TicketConfig`, default off). A sweeper
in `internal/agents/ticket` scans ticket-enabled projects: stale → inject
system-info turn + spawn agent (followup prompt decides the action, e.g.
forward to Slack); dead → set status done. Filters (status/assignee/view mode)
saved per user in `entity.UserMetadata`.

**Tech Stack:** Go (file-based meta JSON), Svelte 5 SPA (`fe/agents/conversation`,
`fe/agents/project-settings`), Effect API client, MCP JSON-RPC handlers.

**Spec:** decisions captured in this doc (brainstorm 2026-08-22, chat approval):
- Session = ticket, no separate entity.
- Followup = agent auto-turn (info to agent, NOT free-text to user, NO push notif).
- Followup behavior configured as per-project prompt.
- Field schema per project, seeded defaults (`type`, `priority`); status + assignee built-in.
- Timers per project, default OFF.
- Card mode = kanban grouped by status, drag between columns = change status; only when `TicketConfig.Enabled`.
- Filters: status + assignee (mine / all), flexible, saved per user (server-side profile).
- Manual edit in conversation right rail; also editable over MCP.

## Global Constraints

- Fixed status set v1: `open`, `in_progress`, `waiting`, `done`.
- UI copy English. No "qiscus" in samples/placeholders.
- Ticket disabled ⇒ zero behavior change (nil Ticket, no sweeper work, no toggle).
- No DB migration: session/project meta are JSON files; `UserMetadata` is jsonb — additive fields only.
- Zerolog: `l := log.With().Str("component", "ticketsweep").Logger()`.
- FE follows fe-module skill: Effect `apiGetE/apiPostE`, tests per layer, design-system tokens.

---

### Task 1: session ticket meta

**Files:**
- Modify: `internal/agents/session/session.go` (Meta struct)
- Test: `internal/agents/session/session_test.go`

**Produces:**

```go
// Ticket turns this session into a ticket card (project ticket mode).
// nil = plain session (project has ticket mode off, or created before enable).
type Ticket struct {
    Status         string            `json:"status"`                // open|in_progress|waiting|done
    Assignee       string            `json:"assignee,omitempty"`    // user ID
    Fields         map[string]string `json:"fields,omitempty"`      // project schema values
    UpdatedAt      time.Time         `json:"updated_at"`            // last ticket edit — stale timer basis
    LastFollowupAt time.Time         `json:"last_followup_at,omitempty"`
}

// session.Meta gains:
Ticket *Ticket `json:"ticket,omitempty"`

const (
    TicketOpen       = "open"
    TicketInProgress = "in_progress"
    TicketWaiting    = "waiting"
    TicketDone       = "done"
)

func ValidTicketStatus(s string) bool
```

Steps: failing test for JSON round-trip (Meta with/without Ticket, old meta.json
without field decodes to nil) + ValidTicketStatus table test → implement → pass → commit.

### Task 2: project TicketConfig

**Files:**
- Modify: `internal/agents/project/project.go`
- Test: `internal/agents/project/project_test.go`

**Produces:**

```go
// TicketField is one custom field in a project's ticket schema.
type TicketField struct {
    Key      string   `json:"key"`      // snake_case identifier
    Label    string   `json:"label"`
    Type     string   `json:"type"`     // "text" | "select"
    Options  []string `json:"options,omitempty"` // select only
    Required bool     `json:"required,omitempty"`
}

// TicketConfig turns a project's sessions into ticket cards. Zero value =
// feature off — every meta.json written before this field decodes to off.
type TicketConfig struct {
    Enabled          bool          `json:"enabled"`
    Fields           []TicketField `json:"fields,omitempty"`
    FollowupAfter    Duration      `json:"followup_after,omitempty"`     // 0 = off
    FollowupPrompt   string        `json:"followup_prompt,omitempty"`
    AutoResolveAfter Duration      `json:"auto_resolve_after,omitempty"` // 0 = off
}

// project.Meta gains:
Ticket TicketConfig `json:"ticket,omitempty"`

// DefaultTicketFields returns the seed schema used when a project enables
// ticket mode with no fields yet: type (select) + priority (select).
func DefaultTicketFields() []TicketField
```

`Duration`: JSON as seconds (int64) — simplest cross-FE contract (`json:"...,omitempty"`,
`time.Duration` wrapper or plain `int64` seconds; use plain `int64` seconds named
`FollowupAfterSec`/`AutoResolveAfterSec` if wrapper adds noise).
Decision: **plain int64 seconds** (`followup_after_sec`, `auto_resolve_after_sec`).

Steps: failing test (old meta decodes off; seed fields; round-trip) → implement → commit.

### Task 3: ticket decision funcs + sweeper

**Files:**
- Create: `internal/agents/ticket/ticket.go` (decisions)
- Create: `internal/agents/ticket/sweeper.go`
- Test: `internal/agents/ticket/ticket_test.go`

**Interfaces:**

```go
// NeedsFollowup: enabled, not done, followup timer on, ticket stale
// (now-UpdatedAt > FollowupAfter) and not recently followed up
// (now-LastFollowupAt > FollowupAfter).
func NeedsFollowup(cfg project.TicketConfig, t *session.Ticket, now time.Time) bool

// NeedsAutoResolve: enabled, not done, auto-resolve timer on,
// now-UpdatedAt > AutoResolveAfter.
func NeedsAutoResolve(cfg project.TicketConfig, t *session.Ticket, now time.Time) bool

// FollowupMessage renders the system-info turn body: ticket snapshot
// (session id, title, status, assignee, fields, last update) + the
// project's FollowupPrompt.
func FollowupMessage(sess session.Session, cfg project.TicketConfig) string

// Sweeper deps injected — no pool import (avoids cycle):
type SweeperDeps struct {
    Layout       agentconfig.Layout
    ListProjects func() ([]project.Project, error)
    ListSessions func(projectID string) ([]string, error) // session IDs
    // SendSystem injects a system-origin turn and spawns the agent
    // (wired to pool.Send at boot).
    SendSystem   func(sessionID, text string) error
    Interval     time.Duration // default 1m tick
}
func StartSweeper(ctx context.Context, d SweeperDeps)
```

Sweep pass per tick: for each ticket-enabled project → each session with
non-nil Ticket → auto-resolve first (set `Status=done`, save, log; no spawn),
else followup (SendSystem(FollowupMessage), set LastFollowupAt, save).
Auto-resolve wins over followup when both due.

Steps: table tests for both decision funcs (off/enabled/done/fresh/stale/guard)
+ sweep pass test with fake deps → implement → commit.

### Task 4: HTTP API

**Files:**
- Create: `internal/tools/agents/api_tickets.go`
- Modify: `internal/tools/agents/handler.go` (routes)
- Modify: `internal/entity/user_metadata.go` (saved filters)
- Test: `internal/tools/agents/api_tickets_test.go`

**Routes (all under existing agents tool base, auth like sibling APIs):**

```
GET  /api/projects/{id}/tickets        → { config: TicketConfig, tickets: []TicketItem }
PUT  /api/projects/{id}/ticket-config  → save TicketConfig into project meta (admin/owner)
PUT  /api/sessions/{id}/ticket         → { status?, assignee?, fields? } partial update,
                                          bumps Ticket.UpdatedAt, validates status + required fields
GET  /api/me/ticket-filters/{projectID}
PUT  /api/me/ticket-filters/{projectID} → saved per user
```

```go
// entity.UserMetadata gains:
// TicketFilters stores each user's saved board filter per project,
// keyed by project ID. Opaque to the backend — FE owns the shape.
TicketFilters map[string]TicketFilter `json:"ticket_filters,omitempty"`

type TicketFilter struct {
    Statuses []string `json:"statuses,omitempty"` // empty = all
    Assignee string   `json:"assignee,omitempty"` // "" = all, "me", or user ID
    ViewMode string   `json:"view_mode,omitempty"` // "list" | "card"
}

// TicketItem (response shape for the board):
type TicketItem struct {
    SessionID  string            `json:"session_id"`
    Title      string            `json:"title"`
    Status     string            `json:"status"`
    Assignee   string            `json:"assignee,omitempty"`
    Fields     map[string]string `json:"fields,omitempty"`
    UpdatedAt  time.Time         `json:"updated_at"`
    LastActive time.Time         `json:"last_active"`
    Stale      bool              `json:"stale"` // NeedsFollowup-style staleness for badge
    OwnerID    string            `json:"owner_id,omitempty"`
}
```

Sessions without Ticket in an enabled project appear as `status=open` with
zero UpdatedAt (lazy default — written back on first edit).

Steps: failing handler tests (update ticket happy path, bad status 400,
config save, filter save round-trip) → implement → commit.

### Task 5: MCP tools

**Files:**
- Create: `internal/mcp/handlers/ticket.go`
- Modify: `internal/mcp/agent_tools.go` + `internal/mcp/handler.go` (register, follow wick_set_title wiring)
- Test: `internal/mcp/tools_test.go` (extend)

```go
// wick_ticket_get  args: session_id → ticket snapshot + project schema
// wick_ticket_set  args: session_id, status?, assignee?, fields? (object)
//   validates like the HTTP handler, bumps UpdatedAt, refreshSession after save
```

Pattern copy of `handlers/title.go` (canManageSession, session.Load/SaveMeta).

### Task 6: FE types + api

**Files:**
- Modify: `fe/agents/conversation/src/lib/types/agents.ts`
- Create: `fe/agents/conversation/src/lib/api/tickets.ts`
- Test: `fe/agents/conversation/src/lib/api/__tests__/tickets.test.ts` (Effect mock layer)

```ts
export type TicketField = { key: string; label: string; type: "text" | "select"; options?: string[]; required?: boolean };
export type TicketConfig = { enabled: boolean; fields?: TicketField[]; followup_after_sec?: number; followup_prompt?: string; auto_resolve_after_sec?: number };
export type TicketItem = { session_id: string; title: string; status: string; assignee?: string; fields?: Record<string, string>; updated_at: string; last_active: string; stale: boolean; owner_id?: string };
export type TicketFilter = { statuses?: string[]; assignee?: string; view_mode?: string };

export const getProjectTickets = (base: string, projectID: string) => apiGetE<{ config: TicketConfig; tickets: TicketItem[] }>(...);
export const updateSessionTicket = (base: string, sessionID: string, patch: Partial<Pick<TicketItem, "status" | "assignee" | "fields">>) => apiPostE(...); // PUT via apiPostE-equivalent helper
export const saveTicketConfig = (base: string, projectID: string, cfg: TicketConfig) => ...;
export const getTicketFilter = / saveTicketFilter = ...;
```

### Task 7: FE KanbanBoard

**Files:**
- Create: `fe/agents/conversation/src/lib/components/KanbanBoard.svelte`
- Create: `fe/agents/conversation/src/lib/components/TicketCard.svelte`
- Test: `fe/agents/conversation/src/lib/components/__tests__/KanbanBoard.test.ts`

- 4 columns (open / in_progress / waiting / done), native HTML5 drag & drop
  (`draggable`, `ondragstart/ondragover/ondrop`), drop → `updateSessionTicket`
  optimistic + toast on error.
- Card: title, short session id, status pill, assignee chip, custom fields,
  relative last update, `stale` badge.
- Filter bar above columns: status chips (multi), assignee select (All / Mine),
  changes persisted via `saveTicketFilter` (debounced).
- Click card (not drag) → `onSelect(sessionID)`.

### Task 8: ProjectLanding toggle

**Files:**
- Modify: `fe/agents/conversation/src/lib/components/ProjectLanding.svelte`
- Modify: parent route/page that feeds it (pass ticket config + current user id)

- When `config.enabled`: segmented toggle `List | Card` next to search; value
  from saved filter `view_mode` (default list); card → render KanbanBoard,
  list → existing SessionList. Toggle hidden when disabled.

### Task 9: TicketPanel (right rail)

**Files:**
- Create: `fe/agents/conversation/src/lib/components/TicketPanel.svelte`
- Modify: `fe/agents/conversation/src/lib/components/DetailView.svelte` (~L1329 tab list)

- New rail tab "Ticket" (icon: tag), only when session's project has ticket
  mode on. Form: status select, assignee ("Assign to me" + user id text),
  schema fields (text/select per type, required marked), Save → PUT,
  shows UpdatedAt. Data manual-set — user's "ganti data" case.

### Task 10: project-settings section

**Files:**
- Modify: `fe/agents/project-settings/src/...` (settings SPA — locate main form component during task)
- Modify: its api module + Go save handler if TicketConfig not covered by generic meta save

- Section "Ticket system": enable toggle; when on → field editor rows
  (key/label/type/options/required, add/remove), followup interval (minutes,
  0=off) + prompt textarea, auto-resolve window (hours/days, 0=off).
  Helper copy: "When a ticket goes stale the agent is woken with this prompt —
  it decides what to do (e.g. escalate to a channel)."

### Task 11: boot wiring + verification

**Files:**
- Modify: server boot (where pool + layout live — follow existing background
  loop wiring, e.g. connector TTL sweeper call site)
- Modify: `internal/docs`/root docs via doc-sync at PR time

- `ticket.StartSweeper(ctx, deps)` with `SendSystem` = pool system-turn send
  (same mechanism as reap-notify/system-injected turns but WITH spawn).
- `go build ./...`, `go test ./internal/...`, `cd fe && npm run test && npm run build`.
- `graphify update .`

## Testing summary

- Go: JSON round-trips (Tasks 1–2), decision-table tests + fake-deps sweep (Task 3), handler tests (Tasks 4–5).
- FE: Effect mock-layer api tests (Task 6), component test drag→status callback + filter (Task 7).
- Manual smoke: enable ticket mode on a project, create sessions, drag cards, edit from rail, set 1-minute followup and watch agent turn arrive; kill server on 8080 after.
