---
outline: deep
---

# Projects

A **project** bundles a folder with its defaults. One project = **1 folder + defaults + pinned sessions + a display name and icon**. Sessions belong to a project; the project's folder is the `cwd` the agent subprocess runs in.

The agent runs as a subprocess; the project's folder is the `cwd` it gets. Whatever you (or the agent itself, via Bash) put in the folder is the agent's world.

::: info Source
Code: [`internal/agents/project/project.go`](https://github.com/yogasw/wick/blob/master/internal/agents/project) (`Meta`, CRUD, `ResolvePath`).
Migration from the old workspace model: [`internal/agents/project/migrate.go`](https://github.com/yogasw/wick/blob/master/internal/agents/project/migrate.go).
Layout math: [`internal/agents/config/layout.go`](https://github.com/yogasw/wick/blob/master/internal/agents/config/layout.go).
:::

::: tip Renamed from "Workspace"
Projects replace the old **Workspace** concept (familiar term from Codex/Claude). On first boot after upgrade, every existing workspace is migrated 1:1 into a project with the same folder + defaults, and sessions are re-linked automatically — no data loss. The old `workspaces/` directory is kept on disk as a defensive backup.
:::

## Two kinds: managed vs custom

| Kind | Where the files live | Created by | Wick deletes on project delete? |
|---|---|---|---|
| **Managed** | `~/.<app>/agents/projects/<id>/files/` | Wick (`MkdirAll` at create time) — empty folder | Yes |
| **Custom path** | Any absolute path you point at (e.g. `D:/code/myproject`, `~/scratch`) | You (must already exist before you create the project) | No — wick never owned it, wick never deletes it |

The custom-path requirement that the directory **must already exist** is enforced at create time — typos surface immediately, not at first spawn.

A project can hold zero, one, or many repos. There's no git worktree, no auto-clone, no master-branch model. The agent does the cloning itself via Bash if you ask it to.

The folder is part of the project's identity: there's no multi-folder project. Want a different folder? Create another project, or move the session to one.

## Built-in `default` project

Every fresh install has a project named `default`. It's created by `EnsureDefault` at boot, can't be deleted, and is what the pool falls back to when a session doesn't specify a project.

This is what makes "first-message-creates-session" work without any pre-setup: a fresh install + a Slack message in a thread = a session bound to `default`, agent spawned in `~/.<app>/agents/projects/<default-id>/files/`.

Personal projects (auto-created per user, tagged `personal`) are protected from deletion the same way — the delete button is hidden and the API rejects the request for any project where `project.IsProtected` returns true (built-in `default`, or tagged `personal`).

## Defaults

Each project carries defaults that new sessions inherit when you don't override them per-session:

| Default | Effect |
|---|---|
| **Preset** | Preset bound at session-create time. Falls back to `default`. |
| **Provider** | Provider instance (`type/name`, e.g. `claude/work`) used when a session doesn't specify one. The dropdown lists every healthy instance, not just base types. Bare type values from older projects are promoted to the canonical default instance automatically. |
| **System prompt addon** | Free-text appended to the preset's system prompt for every session in this project. |

In the New Session composer, picking a project pre-fills the provider + preset from these defaults; you can still override either per session. The provider dropdown in the composer also shows full `type/name` instances — selecting a project auto-selects its saved default provider when that instance is available.

If the saved default provider instance is renamed or deleted after a project is created, the settings form shows it as `type/name (unavailable)` so the value isn't silently overwritten.

## Web UI

Projects live in the **left sidebar** — there's no separate list page. The `PROJECTS` section lists every project with its session count; clicking one opens the project (a Claude-style landing: compose box on top, the project's chats below). Hover a row to reveal a 📌 pin toggle.

- **+ New** (sidebar) → `/tools/agents/projects/new` — the create page.
- **⚙ Settings** (on a project's landing) → `/tools/agents/projects/<id>` — the full settings page: icon + name, folder (managed/custom radio), defaults, pinned sessions, a `meta.json` preview, and delete.

The settings form fields:

| Field | Notes |
|---|---|
| **Icon** | One emoji. Optional; defaults to 📁. |
| **Name** | Required. Display name (mutable — the id never changes). |
| **Folder** | Radio: Custom path (absolute, must exist) or Managed (`projects/<id>/files/`). |
| **Default Preset / Provider** | Inherited by new sessions. |
| **System prompt addon** | Appended to the preset system prompt for every session. |
| **Description** | UI-only metadata. |
| **Widget permissions** | Per-project override of the [HTML artifact CSP](../agents#html-artifact-content-security-policy). See below. |

## Widget permissions

Each project can override the global [Widget CSP config](../agents#widget-group-html-artifact-csp) for its own HTML artifacts. A 3-way toggle — **Secure** / **Unsecure** / **Custom** — mirrors the global `widget_mode` knob; leaving the override off inherits the global policy verbatim.

Under **Custom**, per-directive controls (frame/img/media/connect/script-src, popups, allowlist) work the same as the global ones. The project's own allowlist **appends** to the global allowlist rather than replacing it — the settings page shows the inherited (global) hosts read-only alongside the project's own, so it's clear what a widget here can actually reach. Because it only appends, a project can't narrow the global allowlist to be *more* restrictive — a project that needs to be stricter than global should pick **Secure** instead.

## Pin a project as your default

Each user can **pin one project** as their personal default (stored in their user metadata, `pinned_agent_project_id`). When set, opening the Agents tool lands you straight in that project's compose page.

Pin/unpin from the 📌 toggle on the sidebar row or the `📌 Pin as default` button on the project landing. One pin per user — pinning another replaces it.

## Meta on disk

`projects/<id>/meta.json`:

```json
{
  "id": "01J...",
  "name": "Wick Backend",
  "icon": "📁",
  "description": "Main wick repo work",
  "custom_path": "/d/code/work/wick",
  "defaults": {
    "preset": "engineer",
    "provider": "claude",
    "system_addon": ""
  },
  "pinned_sessions": ["01J..."],
  "tags": [],
  "widget": {
    "override": false
  },
  "created_at": "2026-06-01T...",
  "updated_at": "2026-06-01T..."
}
```

`widget.override: false` (the zero value, and what every `meta.json` written before this field decodes to) means "inherit the global Widget policy". Set `override: true` plus `mode` / per-directive fields to customize — see [Widget permissions](#widget-permissions) above.

`custom_path` is omitted for managed projects. Atomic write (tmp file + rename) on every save; `updated_at` is bumped automatically.

## Resolving the cwd at spawn time

The pool calls `project.ResolvePath` when it's about to spawn an agent:

1. Session has a `project_id` set → load that project's meta.
2. Custom path? Return it as-is.
3. Managed? Return `~/.<app>/agents/projects/<id>/files/`.
4. Session has no project → per-session temp dir at `sessions/<id>/cwd/`.

The pool `MkdirAll`s managed paths before passing them to `exec.Cmd.Dir`. Custom paths are assumed to still exist; if you deleted yours out from under wick, spawn surfaces a clean error.

## Moving a session between projects

A session stores its binding as `meta.project_id`. Moving is **metadata-only** — no filesystem work, the session id and path stay stable so workflows / channels / spawn references don't break. Two ways:

- **Drag** a chat row (sidebar or list) onto a project in the sidebar.
- The **Move to project** menu on the session detail page.

The new project's folder becomes the cwd at the next spawn. A live subprocess keeps its old cwd until it's killed and respawned. Deleting a project doesn't delete its sessions — they're just unscoped (`project_id` cleared).

## Multi-session sharing

Multiple sessions can share the same project and run in parallel. Wick does not lock — coordination is your concern. Most agent traffic is read, so two sessions touching the same folder is rarely a problem in practice; two sessions both editing `package.json` is on you.

## Slack / Telegram / REST default project

Each channel ([Slack](https://github.com/yogasw/wick/blob/master/internal/agents/config/slack.go), [Telegram](https://github.com/yogasw/wick/blob/master/internal/agents/config/telegram.go), [REST](https://github.com/yogasw/wick/blob/master/internal/agents/config/rest.go)) has its own `project_id` config field. When set, every session auto-created from that channel binds to it. When **only one project exists**, the channel uses it without asking.

Wick can host several instances of the same transport in one process (one per owning user — see [Per-user instances](./channels#per-user-vs-app-owner-rows)). Each instance dispatches through the same pool `SendFunc`, but every instance stamps **its own** configured `project_id` onto the dispatch — so two Slack bots with different `ProjectID` settings correctly land in their own projects, and changing the project in the UI takes effect on the next message without a restart. This only affects **new** sessions; an existing session keeps the project it was created with.

Precedence, highest first: a per-request override (REST body `project` field) > the originating channel instance's configured project > the sole project on the box (when exactly one exists).

The REST (OpenAI-compatible) channel additionally lets a request **override** the channel default per call with a top-level `"project": "<id>"` field (or `metadata.project` / `metadata.project_id`). See the [REST channel docs](./channels).

## Tickets

A project can turn its sessions into a **ticket board**. Off by default — enabling it changes nothing about how sessions work, it just adds tracking on top.

::: info Source
Code: [`internal/agents/ticket/`](https://github.com/yogasw/wick/blob/master/internal/agents/ticket) (entity, sweeper, auto-create rules), [`internal/agents/project/ticket.go`](https://github.com/yogasw/wick/blob/master/internal/agents/project/ticket.go) (per-project config). MCP surface: [Tickets connector](/connectors/tickets) and [Notes connector](/connectors/notes).
:::

A **ticket** is the unit of work; a **session** is one conversation about it, and a ticket can hold several. Tickets live at `projects/<id>/tickets/<T-XXXX>/ticket.json` with a short, quotable id (`T-4F2A`) rather than a UUID — it appears on board cards and gets typed into chat. A ticket carries a title, a markdown **description**, a status drawn from the project's own board columns, an assignee, project-defined custom fields, and its session list.

### The board

The project landing page shows a kanban board — one column per status, cards are **tickets**, not sessions, laid out as fixed-width columns in a single horizontally scrollable row. The filter bar's **Untracked** chip (off by default) adds a rail of chats that belong to no ticket alongside the columns; its count is always shown even while off. Dragging:

- a ticket card between columns changes its status;
- a chat from the Untracked rail onto a ticket card attaches it to that ticket;
- a chat onto a column turns it into a new ticket of its own.

A card shows only the custom fields marked **Card** in the schema (see [Per-project settings](#per-project-settings)) — everything else stays on the ticket's own page.

Filters — statuses, assignee, and the Untracked chip — decide what the board asks the server for, not just what it draws: switching a column or the Untracked rail off stops the server building those cards at all, so a project with hundreds of chats costs the same to poll as a small one.

Deleting a ticket (the trash icon on its page) asks whether its chats survive as untracked or are deleted with it.

Opening a ticket's page puts the title, description, and sessions (capped at 5, with "show more") in the main column and its status/assignee/fields in a rail alongside; it also lists sessions **most recently active first**, not in the order they were attached — the chat someone was just in surfaces at the top rather than wherever it happened to land on a long-running ticket. The open ticket is reflected in the URL (`?ticket=<id>`), so back/forward and sharing a link both work.

### Starting a chat on a ticket

Selecting a ticket on the board — not only pressing **+ New session** — scopes the next chat you start to it: the composer names the ticket and the placeholder reflects it (clipped if the title runs long), and a **Start without a ticket** link backs out of the selection. The new session is attached to the ticket as soon as it opens, so it reads the ticket's notes from its first turn.

A session attached to a ticket is told, in its system prompt, to treat that ticket as the default subject of the conversation: read the ticket (`ticket_get`) and its notes (`notes_list`) before anything else, then follow whatever skill matches the ask. Unrelated digging through other sessions or data is a last resort, not a first move.

From inside a chat, the conversation header's view menu offers a jump: a chat already attached to a ticket jumps straight to it, one that isn't opens the board instead. It only appears when the project has ticket mode on.

### Per-project settings

Configured from the project settings page (or via `ticket_settings_get` / `ticket_settings_set` on the [Tickets connector](/connectors/tickets#operations)):

| Setting | Effect |
|---|---|
| **Enabled** | Turns the board and automation on for this project. Off by default. |
| **Board columns** | The statuses this board uses, in order — see [Board columns](#board-columns). |
| **Custom fields** | A schema of `{key, label, type, options, required, show_on_card}` fields shown on the ticket's own page (and available to `fields`). Only fields with **Card** checked also appear on the board card — off by default, so a card stays a glance rather than growing with the schema. |
| **Stale-followup window** | A ticket not on the board's finished column, untouched for this long, gets a follow-up turn sent to its most recently attached session's agent, using the project's follow-up prompt. The agent decides what to do (update the ticket, ping someone, close it) rather than wick messaging anyone. Repeats once per window while still stale. |
| **Auto-resolve window** | A ticket untouched for this long is moved to the board's finished column automatically, no agent spawn. Auto-resolve wins over follow-up when both are due. |

Any ticket update (status, title, assignee, or fields) resets both timers — that is what makes "the agent acted, so stop nagging" work.

### Board columns

Statuses are per project, so a team names its own stages — `triage → coding → review → shipped` is as valid as the defaults. Each column has a **key** (what tickets store, and what agents pass over MCP: lowercase `a-z0-9_`), a **label** (display only, safe to reword), and one column is marked the **finished stage**.

That last part is structural, not decoration: auto-resolve moves finished work to that column, and the stale-followup timer treats it as "leave this alone". A list without exactly one finished stage is refused on save.

The list order is the column order. Leaving it untouched uses the built-in set — `open`, `in_progress`, `waiting`, `done` — which is what every project starts with.

Renaming a label is free. **Removing a column that still holds tickets is refused**, and the error names the columns in question: those tickets get moved deliberately, rather than a rename quietly leaving them without a place on the board.

### Auto-create rules

A project can auto-create a ticket for new sessions without anyone asking, via a list of rules: `{origin, channel_kind, match, title, enabled}`.

- `origin` — `ui` / `slack` / `telegram` / `rest` / `*` (any).
- `channel_kind` — narrows a channel origin to `dm` / `channel` / `thread`.
- `match` — empty (origin alone decides), `contains:<text>`, or `regex:<expr>`, tested against the session's first message.

Rules are tried **in order and the first match wins**, so a disabled narrow rule placed above a broad one carves an exception out of it — that is how "everything from Slack except DMs" gets expressed as two rules. Evaluated on the session's first user message; a session already on a ticket is always left alone.

### Notes

Notes are a separate subsystem from tickets, not a ticket-only feature — a session with no ticket still has its own notes. See the [Notes connector](/connectors/notes) for the full model (audience, hidden notes, and how notes travel when a session is attached, moved, or detached).

### Integrations

A project's board can also be wired to the outside world: outbound webhooks that fire on ticket events, and a Personal Access Token-authed REST API for another system to create and move tickets. Both are off by default and configured under **Ticket system → Integrations**. See [Ticket Integrations](./ticket-integrations) for setup, the full endpoint reference, and the webhook event catalogue.

## See also

- [Pool & Sessions](./pool) — how the cwd is actually wired into `exec.Cmd`.
- [Providers](./providers) — the `provider` default on project meta.
- [Channels](./channels) — per-channel default project config.
- [Tickets connector](/connectors/tickets) / [Notes connector](/connectors/notes) — the MCP surface for both.
- [Ticket Integrations](./ticket-integrations) — outbound webhooks and the token-authed REST API.
