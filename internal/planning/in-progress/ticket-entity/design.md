# Tickets as first-class entities, with notes

Tickets move out of `session.Meta` and become their own on-disk entity under
a project, so one ticket can hold many sessions. Every ticket (and every
session outside a ticket) also gets **notes**: markdown items, optionally
checkable, whose visibility to the agent is a per-note toggle. A `tickets`
connector exposes both surfaces over MCP.

## TODO

- [x] Layout paths + `internal/agents/ticket` store (ticket.json, sessions list)
- [x] Notes store (`internal/agents/notes`) — markdown items, checkable, visibility
- [x] Sweeper rewrite against the new store
- [x] HTTP API: ticket CRUD, session attach/detach/new, notes CRUD
- [x] `internal/connectors/tickets/` — MCP ops for tickets
- [x] `internal/connectors/notes/` — MCP ops for notes (usable without tickets)
- [x] Built-in skill + one-line session-start pointer
- [x] FE: board of ticket cards, ticket detail with session list, notes panel
- [x] Drop `session.Meta.Ticket` and the old per-session ticket handlers

**Status:** design approved in chat 2026-08-22. The previous per-session
ticket (`session.Meta.Ticket`, committed on `feat/ticket-cards`) was a test
run and is **removed outright** — no migration, no compatibility shim.

## Round 2 (same day): moving work around, and auto-create

Added after the first pass, because creating a ticket was the only path in —
there was no way to put an existing chat on one.

**Notes follow their session.** `ticket.AttachSession` / `DetachSession` now
move the session's notes to the new scope. Attaching to a second ticket
detaches from the first, so a session is never on two — that is what makes
dragging one between cards a move, not a copy. A DETACH takes back only the
notes that session wrote (`Note.SourceSessionID`); notes another session left
on the ticket describe the ticket's work and stay. Moved notes keep their id
and timestamps and gain `MovedAt`, so a reader can see one arrived from
elsewhere. Wired through `ticket.SetNotesMover` because
`internal/agents/notes` already imports `internal/agents/ticket`.

**Auto-create rules** on `project.TicketConfig.AutoCreate`:

```go
type AutoCreateRule struct {
    Origin      string // "ui" | "slack" | "telegram" | "rest" | "*"
    ChannelKind string // "" | "dm" | "channel" | "thread"
    Match       string // "" | "contains:<text>" | "regex:<expr>"
    Title       string // template; {message} / {origin}
    Enabled     bool
}
```

Rules are tried in order and the **first match wins**, so a disabled narrow
rule above a broad one is how an exception is written — a disabled
`slack`+`dm` rule above an enabled `slack` rule means "all of Slack except
DMs". A regex that will not compile is refused on save rather than sitting in
the config looking active.

Evaluated on the session's **first user message** (`pool.OnFirstUserMessage`),
not at create time: `Match` needs the text. Sub-agent sessions never qualify.
DM vs channel comes from the channel id (Slack `D…` / `C…`, negative Telegram
chat ids) rather than a new field every transport would have to remember.

**MCP** `ticket_settings_get` / `ticket_settings_set` — the agent can read and
rewrite these rules. Durations are minutes/days on that surface, seconds on
disk, because an agent writing "60" for an hour is likelier to be right than
one converting.

**Board** is now three zones: an *Untracked* rail (project chats on no
ticket), status columns, and per-card session rows. Two drag types share the
board, told apart by payload: a **column** only accepts a ticket (status
change), a **card** only accepts a session (attach/move). Each untracked chat
also has a "+ ticket" shortcut that prefills the title from the chat.

**Conversation rail** gained the same moves from inside a session: create a
ticket from this chat, attach to an existing one, move to another, detach, set
status, and rename in place.

**Tickets and notes are two rail tabs, not one.** A single "Ticket & notes"
panel read as though notes were a ticket feature — they are not: notes work on
a chat with no ticket at all. What a ticket changes is only the notes' SCOPE.
So the rail has a **Ticket** tab (the ticket, its status, moving this chat
between tickets) with a one-line pointer to the note count, and a **Notes** tab
that states which scope is in effect ("shared across every session on T-4F2A"
vs "private to this chat") and then just lists them. The Notes tab needs only a
reachable scope; the Ticket tab additionally needs a project, since a chat
outside one cannot hold a ticket.

## Round 3: rail overflow, and paying only for what is drawn

**Rail overflow.** The rail reached eight tabs and will grow. It now shows
N and folds the rest under **More**, with order and count saved per user
(`entity.UserMetadata.Rail`, `GET/PUT /api/me/rail`). Two rules keep it from
becoming unpredictable:

- A tab carrying a badge, or working, is **promoted** into the strip — a
  count nobody can see is worse than a shifted position. The More button
  carries the hidden tabs' combined badge so nothing is silently buried.
- Promotion changes *which* tabs are visible, never their sequence, so
  "Context before Process" stays true whichever is currently loud. The
  **open** panel's tab is pinned in too: hiding it would leave a panel with
  no way to close it.

The default count is the ceiling, not 4. Folding tabs away is a choice made
when the rail feels long, not something that happens on a first visit —
shipping a smaller default silently hid Source and broke 17 DetailView tests,
which was the design telling the truth about itself.

**Payload caps, not display filters.** The board used to send every session
row and every untracked chat on each poll, and the client threw most away. It
is now bounded server-side:

```
GET /api/projects/{id}/tickets?rows=3&untracked=0&untracked_limit=25
```

`rows` caps session rows per card (0 = counts only), `untracked=0` skips that
list entirely, `untracked_limit` pages it. A count always travels even when
the rows do not, so a card says "+5 more sessions" and the rail says "25/142"
instead of implying they hold everything. Collapsing the untracked rail is
therefore load-bearing: with it shut the client stops asking, so a project
with hundreds of loose chats costs nothing extra to poll. The collapse is
remembered per user (`TicketFilter.HideUntracked`).

**Dropping a chat on a column creates a ticket.** Dragging an untracked chat
onto "In Progress" plainly means "this is work, and it has started", so it
becomes its own ticket at that status rather than being refused for not having
one yet. Dropping on a card still attaches or moves.

## Round 4: ownership, and deleting tickets

**Creating a ticket assigns it to you.** Dragging a chat into a column is
someone saying "I am taking this on", so landing an *unassigned* card in
front of them said the opposite of the gesture. Both surfaces default the
assignee to the caller — HTTP to the logged-in user, MCP to
`CallerUserID` (an agent creates on somebody's behalf). `Assignee` became a
pointer so "not sent" stays distinct from an explicit "" that means nobody.

**Deleting a ticket asks what happens to its chats:**

```
DELETE /api/tickets/{id}?sessions=keep     (default) chats survive, untracked
DELETE /api/tickets/{id}?sessions=delete   chats deleted with the ticket
```

The destructive shape must be named. The confirmation offers the two
outcomes as separate buttons and **spells out the count** — "Delete the
ticket and all 3 conversations · their messages, notes, and files go too" —
because a number is the fact that makes someone stop and read. A ticket is
cheap to recreate; the conversations under it are not.

**A ticket that just lost its last chat is offered for removal.** Attach and
detach report `emptied_ticket` when the ticket the chat left now holds
nothing, and the board prompts. This prompt — and only this one — carries
**"Don't ask again"**, stored as `AutoDeleteEmptyTickets` (`always`/`never`)
with a reset in the profile under "Remembered answers". Safe to make standing
because deleting an *empty* ticket destroys no conversation; the
still-holds-chats case always asks, whatever the preference says.

It is checked on the move, not by the sweeper: a ticket deliberately created
empty and not yet used would otherwise vanish before it could be filled.

## Status: implemented (2026-08-22)

Deltas from the design as written:

- **Two connectors, not one** — `internal/connectors/tickets/` and
  `internal/connectors/notes/`, because notes work on a session with no
  ticket and must be grantable without the ticket board.
- **Two skills, not one** — `wick-tickets` and `wick-notes`, embedded via
  `internal/agents/skillsync/builtin/`.
- **`internal/agents/ticketprompt`** is its own package: it reads both
  stores, and `notes` already imports `ticket`, so putting the pointer in
  either would close an import cycle.
- **`Audience` replaced `Visibility`** on a note: it labels who the note was
  written FOR (the agent reads every audience and can improve any of them),
  while `Hidden` is the actual permission — hidden notes are filtered inside
  the store (`ListForAgent`), so no op can leak one.
- **No prompt injection at all.** The pointer names the ticket and COUNTS
  notes; a test asserts the block does not grow when 30 notes are added, and
  another asserts no note body reaches the prompt.
- **`GET /api/notes` also returns `ticket`** when the scope resolved to one,
  so the conversation rail can name it without a second fetch.
- Sweeper follows up on the ticket's **most recently attached session**, and
  skips a ticket with no sessions (nothing to wake) while still letting it
  auto-resolve.

Verification (after round 2): `go build ./internal/... ./cmd/...` clean; Go
tests pass for ticket, notes, ticketprompt, project, session, both
connectors, skillsync, pool, and mcp. FE: 851/853 in conversation and 45/45
in project-settings (the 2 failures in `browser.test.ts` are a pre-existing
baseline); `svelte-check` at 28 errors in conversation — byte-identical to the
baseline measured by stashing this branch — and 0 in project-settings.
Tailwind rebuilt. The untracked rail, per-card session rows, both drag types,
the no-ticket rail state, and the blurred hidden-note state are covered by
component tests and confirmed by screenshot.

## Why

The per-session design tied a ticket to exactly one session. That breaks the
common recovery move: when an agent goes off the rails, you want a fresh
session on the *same* ticket, keeping its status, assignee, and context. It
also had nowhere to record "what someone should know before continuing" —
knowledge that belongs to the work, not to one conversation.

## Storage

```
projects/<projectID>/
  meta.json                       — project meta (owns TicketConfig)
  tickets/
    <ticketID>/
      ticket.json                 — status, assignee, fields, sessions[], timers
      notes/
        <noteID>.json             — one note: markdown body + state
sessions/<sessionID>/meta.json    — gains TicketID (a back-reference only)
```

Two directions of reference, with one authority: `ticket.json.sessions` is
the list of record; `session.Meta.TicketID` is a denormalised back-pointer so
the sidebar and the pool can answer "which ticket is this session in?"
without scanning every ticket. Writes always go through the ticket store,
which updates both. A back-pointer that disagrees is treated as stale and
ignored — the ticket's list wins.

Notes live in their own directory of one-file-per-note rather than an array
inside `ticket.json`: two agents editing different notes on the same ticket
would otherwise clobber each other on write, and this is a system built for
agents editing concurrently.

### Types

```go
// internal/agents/ticket
type Ticket struct {
    ID        string            `json:"id"`         // short, human-quotable: "T-4F2A"
    ProjectID string            `json:"project_id"`
    Title     string            `json:"title"`
    Status    string            `json:"status"`     // open|in_progress|waiting|done
    Assignee  string            `json:"assignee,omitempty"`
    Fields    map[string]string `json:"fields,omitempty"`
    Sessions  []string          `json:"sessions,omitempty"` // list of record
    CreatedAt time.Time         `json:"created_at"`
    UpdatedAt time.Time         `json:"updated_at"`         // ticket edits — timer basis
    LastFollowupAt time.Time    `json:"last_followup_at,omitempty"`
}

// internal/agents/notes
type Note struct {
    ID        string     `json:"id"`
    Body      string     `json:"body"`                 // markdown
    Checkable bool       `json:"checkable,omitempty"`  // renders as a checkbox
    Done      bool       `json:"done,omitempty"`       // only meaningful when Checkable
    // Audience is who the note was WRITTEN FOR, not an access rule:
    //   "ai"    — guidance meant for the agent
    //   "human" — a message to whoever picks this up next
    //   "both"  — useful to either (the default)
    //
    // The agent sees this label on every note it reads, so it knows how to
    // treat one — and can help improve a note written for humans instead of
    // being locked out of it.
    Audience string `json:"audience"`
    // Hidden takes a note out of the MCP surface entirely: the agent never
    // receives it. The UI keeps showing it, blurred, behind an eye toggle —
    // hiding is not deleting, and a hidden note can be un-hidden.
    Hidden    bool      `json:"hidden,omitempty"`
    Author    string    `json:"author,omitempty"`     // user ID, or "agent"
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}
```

`ID` is a short code, not a UUID: it appears on every board card and gets
typed into chat ("lanjutin T-4F2A"), so it has to be quotable. Generated as
`T-` plus 4 base32 characters of randomness, retried on collision within the
project.

Notes attach to a **scope**, which is either a ticket or a session:

```go
type Scope struct {
    ProjectID string
    TicketID  string // set → notes live under the ticket
    SessionID string // set → notes live under the session
}
```

A session inside a ticket resolves to the ticket's scope, so a note written
from any session in the ticket is seen by all of them. A session with no
ticket keeps its notes under `sessions/<id>/notes/`. This is the rule that
makes "open a fresh session, keep the context" work without the user having
to copy anything.

## How the agent learns about notes

Note bodies are **never** injected into the prompt. A ticket accumulates
notes for as long as the work lasts, and a growing system prompt would push
that cost onto every single turn forever.

Instead, two cheap things:

1. **A one-line pointer** on session start, via the same non-user turn
   channels already use for origin context (`SessionStartHook` → `pool.send`
   with a non-user role, which buffers without spawning):

   ```
   [ticket] T-4F2A "Payment webhook failing" · status in_progress · 3 notes.
   Read them with the tickets connector (notes_list) before continuing.
   ```

   Fixed size regardless of how much is written — a count, not the content.
   Omitted entirely when the ticket has no notes.

2. **A built-in skill** (`tickets`) that teaches the flow: read notes before
   picking up work on a ticket, write one when you learn something the next
   session would need, mark a checkable note done when it is done. The skill
   is the "when", the connector is the "how".

The agent then reads what it needs, when it needs it. A ticket with fifty
notes costs the same per turn as one with none.

Notes are *not* put in `SystemAddon`: that field is a project-level prompt
fragment, and overwriting it per ticket would silently drop the project's own
addon.

## MCP — two connectors

Two fixed, single-instance connectors (the datatables pattern), **not one**:
notes are not a ticket feature. A session with no ticket keeps its own notes,
so an agent can be given note-taking without any access to the ticket board —
and a project that never turns ticket mode on still gets notes.

**`internal/connectors/tickets/`** — `list`, `get`, `create`, `update`
(status/assignee/fields/title), `attach_session`, `detach_session`.

**`internal/connectors/notes/`** — `list`, `add`, `update`, `check` (toggle
done), `hide`, `delete`.

Notes ops take a scope: `ticket_id`, or `session_id`, or neither — in which
case the agent's own session is resolved through `notes.Resolve`, which lands
on the ticket's notes when the session belongs to one. So "add a note here"
needs no arguments and still shares across the ticket.

`notes_list` returns each note's `audience` and `checkable`/`done` state
alongside the body, so the agent can tell a hint written for itself from a
handover message written for a person — and improve either. Hidden notes are
filtered out in the store (`ListForAgent`), not in the handler, so no op can
leak one.

This replaces the `wick_ticket_get` / `wick_ticket_set` meta-tools: a
connector is taggable and auditable per-user, which two hard-coded MCP tools
are not.

## HTTP API

```
GET    /api/projects/{id}/tickets                  → board payload (ticket cards)
POST   /api/projects/{id}/tickets                  → create
GET    /api/tickets/{ticketID}                     → detail + sessions + notes
PATCH  /api/tickets/{ticketID}                     → status/assignee/fields/title
POST   /api/tickets/{ticketID}/sessions            → new session in this ticket
PUT    /api/tickets/{ticketID}/sessions/{sid}      → attach an existing session
DELETE /api/tickets/{ticketID}/sessions/{sid}      → detach
GET    /api/notes?ticket_id=…|session_id=…         → list for a scope
POST   /api/notes                                  → add
PATCH  /api/notes/{noteID}                         → body/visibility/done
DELETE /api/notes/{noteID}
```

## UI

**Board** — one card per ticket: title, `T-4F2A` code, status pill, assignee
chip, type/priority fields, and a session count ("3 sessions"). Drag between
columns sets status, as now. Clicking opens ticket detail.

**Ticket detail** — header (title, code, status, assignee, fields), a session
list with lifecycle badges and a "New session in this ticket" button, and the
notes panel.

**Notes panel** — markdown items, each with a checkbox when checkable and an
audience marker (robot / person / both). Add, edit, check, delete inline. An
eye toggle hides a note from the agent: it stays in the list, rendered
blurred, and can be un-hidden. Reachable from ticket detail and from a
conversation's right rail, where it shows the notes of whichever scope the
session resolves to.

**Sidebar** — sessions belonging to the open ticket are grouped under it, so
switching between a ticket's sessions does not mean hunting through a flat
list.

## What gets deleted

`session.Meta.Ticket`, `session.Ticket`, `ValidTicketStatus` (moves to the
ticket package), the `/api/sessions/{id}/ticket` handlers, `wick_ticket_get`,
`wick_ticket_set`, and their tests. The kanban board, ticket card, and
`TicketConfig` on the project survive — they are re-pointed at the new store.

## Testing

- Ticket store: create/load/save round-trip, short-ID collision retry,
  attach/detach keeping `sessions` and the back-pointer in agreement, delete.
- Notes store: per-file writes not clobbering each other, visibility
  filtering, check toggle.
- Scope resolution: session in a ticket → ticket scope; loose session → its
  own; stale back-pointer ignored.
- Injection: block built from `ai`/`both` only, truncation at the cap,
  re-injection after a change and not before.
- Sweeper: same decision-table tests, re-pointed at tickets.
- API + connector op handlers: happy path, bad status, unknown scope.
- FE: Effect mock-layer tests for the new api module; component tests for the
  ticket card (session count, code) and notes panel (check, visibility).
