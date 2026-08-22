---
outline: deep
---

# Notes

`notes` exposes wick's **notes** as a fixed connector — the running record of what has been learned about a piece of work, so a later session (or a person) can pick it up without re-deriving it.

| | |
|---|---|
| **Source** | [`internal/connectors/notes/`](https://github.com/yogasw/wick/tree/master/internal/connectors/notes) |
| **Key** | `notes` |
| **Icon** | 📝 |
| **Fixed** | ✅ — single row, notes live on wick's own disk |
| **Default tags** | `tags.Connector`, `tags.Platform` |

::: tip Not a ticket feature
Notes work on a chat that belongs to no ticket at all. When a session belongs to a ticket, it reads and writes the **ticket's** notes (shared with every other session on it); a session attached to nothing keeps its own. Notes travel with a session when it is attached to, moved between, or detached from a ticket. Bodies are never injected into the system prompt — a session gets a fixed-size pointer naming the ticket and counting its notes, and reads them on demand through this connector. Kept as a separate connector from [`tickets`](./tickets) so note-taking can be granted without board access.
:::

## Configs

Intentionally empty (`type Configs struct{}`).

## Scope

Every op takes the same three optional scope fields: `ticket_id`, `session_id`, `project_id`. Precedence is `ticket_id` first, then `session_id`, then the calling session — which resolves to its ticket's notes when it belongs to one. Passing nothing is the common case.

## Operations

### `notes_list` — List Notes

List the scope's notes, oldest first. Each carries id, `body` (markdown), `audience` (`ai` / `human` / `both`), `checkable`/done, author, and timestamps. Notes hidden by a user are never returned.

### `notes_add` — Add Note

Add a note to the scope.

| Input | Notes |
|---|---|
| `body` | Required, markdown. |
| `checkable` | Render as a checkbox with a done state. |
| `audience` | `both` (default) / `ai` / `human` — who the note was written for. |

### `notes_update` — Update Note

Edit an existing note's body, audience, or checkable flag.

### `notes_check` — Check Note

Mark a checkable note done, or clear it. Only works on notes created with `checkable=true`.

### `notes_delete` — Delete Note

**Destructive**, off by default. Deletes a note for good — not reversible.

## Hidden notes

A note can carry a `hidden` flag set by its author. A hidden note is kept away from the agent entirely — it never appears in `notes_list` — but is still shown to people in the UI (blurred until revealed).

## See also

- [Tickets connector](./tickets) — the paired connector for the ticket board.
- [Projects → Tickets](/guide/agents/projects#tickets) — how a session's ticket scope is decided.
