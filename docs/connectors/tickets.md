---
outline: deep
---

# Tickets

`tickets` exposes wick's **ticket entities** as a fixed connector. A ticket is the unit of work; a session is one conversation about it, and a ticket can hold several — that is what lets an agent abandon a session that has gone off the rails and continue on a fresh one without losing status, assignee, fields, or notes.

| | |
|---|---|
| **Source** | [`internal/connectors/tickets/`](https://github.com/yogasw/wick/tree/master/internal/connectors/tickets) |
| **Key** | `tickets` |
| **Icon** | 🎫 |
| **Fixed** | ✅ — single row, tickets live on wick's own disk, not behind an external API |
| **Default tags** | `tags.Connector`, `tags.Platform` |

::: tip Separate from Notes
Notes are a **different** connector ([`notes`](./notes)) so note-taking can be granted without any access to the ticket board. See [Projects → Tickets](/guide/agents/projects#tickets) for the feature overview — per-project enable, custom fields, stale-followup and auto-resolve windows, and the kanban board.
:::

## Configs

Intentionally empty (`type Configs struct{}`). Tickets are stored at `projects/<id>/tickets/<T-XXXX>/ticket.json`, not behind credentials.

## Operations

All ops accept an implicit `project_id` (defaults to the calling session's project) and, where relevant, an implicit ticket/session (defaults to the calling session's own).

### `ticket_list` — List Tickets

List a project's tickets, newest first: id, title, status, assignee, fields, and session count. Optional `status` filter — the accepted keys are that project's own (see [Board columns](#board-columns)).

### `ticket_get` — Get Ticket

Return one ticket in full, including its session list. Omit `ticket_id` to get the ticket the calling session belongs to.

Alongside `assignee` (the wick user id — what `ticket_create`/`ticket_update` accept and what filters match on), every ticket in the response also carries `assignee_name`: the assignee's display name, resolved per call. It is omitted when the ticket has no assignee or the id cannot be resolved.

### `ticket_create` — Create Ticket

Create a ticket in a project. Status defaults to the board's first column.

| Input | Notes |
|---|---|
| `title` | Required. |
| `status` | One of the project's status keys. Defaults to its first column. |
| `assignee` | Optional wick user id. |
| `fields` | Optional JSON object of project-defined field values. |
| `attach_current_session` | Attaches the calling session to the new ticket — how an ad-hoc chat becomes tracked work. |

### `ticket_update` — Update Ticket

Update title, status, assignee, or fields. Partial update — only what you pass changes. Status must be one of the project's own [board columns](#board-columns). **Every update resets the project's stale-followup and auto-resolve timers**, so an agent acting on a followup should call this afterward.

### `ticket_attach_session` / `ticket_detach_session` — Attach / Detach Session

Attach or detach a session from a ticket. Attaching is idempotent and moves the session's notes onto the ticket's scope; detaching moves them back. Both default to the calling session when `session_id` is omitted.

### `ticket_settings_get` / `ticket_settings_set` — Ticket Settings

Read or change a project's ticket configuration: whether ticket mode is on, the custom field schema, the stale-followup and auto-resolve windows, and the [auto-create rules](/guide/agents/projects#auto-create-rules).

`ticket_settings_set` takes `auto_create` as a JSON array of `{origin, channel_kind, match, title, enabled}` rules. Rules are tried in order and the **first match wins** — a disabled narrow rule placed above a broad one carves an exception out of it (e.g. "everything from Slack except DMs"). A regex `match` that does not compile is refused rather than stored inert.

It also takes `statuses`, a JSON array of the project's board columns — see below.

## Board columns

Statuses are **per project**: a team names its own stages. `ticket_settings_get` returns the list, and `ticket_settings_set` replaces it:

```json
[
  { "key": "triage",  "label": "Triage" },
  { "key": "coding",  "label": "Coding" },
  { "key": "shipped", "label": "Shipped", "terminal": true }
]
```

- **`key`** is what a ticket stores and what `ticket_create` / `ticket_update` accept. Lowercase `a-z0-9_`, because it gets typed and quoted.
- **`label`** is display only, and safe to reword at any time.
- **`terminal`** marks the stage that means the work is finished. **Exactly one** status must carry it: auto-resolve moves finished tickets there, and the stale-followup timer treats it as "leave this alone". A list without it is refused.

The order is the board's column order. Passing `[]` returns the project to the built-in set (`open`, `in_progress`, `waiting`, `done`), which is what an unconfigured project uses.

A status that still holds tickets cannot be dropped — the call is refused and names the statuses in question, so those tickets are moved deliberately rather than losing their column behind your back.

::: warning No delete op
Deleting a ticket (with the choice to keep or delete its chats) is a UI/admin action only — there is no `ticket_delete` op on this connector.
:::

## See also

- [Notes connector](./notes) — the paired connector for ticket/session notes.
- [Projects → Tickets](/guide/agents/projects#tickets) — the board, auto-create rules, and settings UI.
- [Ticket Integrations](/guide/agents/ticket-integrations) — for a system outside wick: outbound webhooks and a token-authed REST API, as an alternative to this MCP connector.
