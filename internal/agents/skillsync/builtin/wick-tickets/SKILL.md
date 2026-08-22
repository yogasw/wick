---
name: wick-tickets
description: Use when the session's prompt names a ticket (T-XXXX), when the user talks about tickets, the board, statuses, assignees, or when a conversation has gone off the rails and the work should continue in a fresh session on the same ticket. Covers the ticket model (one ticket, many sessions), keeping status honest, and attaching or detaching sessions. Notes live in the separate wick-notes skill.
---

# Tickets

A **ticket** is a unit of work. A **session** is one conversation about it, and a
ticket can hold several. That separation is the point: a conversation that has
gone wrong can be abandoned and replaced without losing the work, because the
ticket keeps the status, the assignee, the fields, and the notes.

Use the `tickets` connector. Notes are a different connector — see the
**wick-notes** skill for those.

## Know what you are on

`ticket_get` with no arguments returns the ticket this session is attached to,
including its other sessions:

```
ticket_get
```

Your prompt already names the ticket and its note count, so call this when you
need the detail — the fields, who it is assigned to, how many other sessions
are on it.

`ticket_list` shows a project's tickets, newest first, and takes an optional
`status` filter. Use it to answer "what is open?" without opening the board.

## Keep the status honest

The status is what a person scanning the board reads, so move it when reality
moves:

```
ticket_update status=in_progress     # you started work
ticket_update status=waiting         # blocked on a human or something external
ticket_update status=done            # finished
```

Those are the built-in defaults — a project can rename or replace its columns
entirely. Call `ticket_settings_get` or `ticket_list` if you are unsure what
this board's statuses are; `ticket_update` only accepts this project's own.

Every update also resets the project's stale-follow-up timer. **If a follow-up
message woke you, update the ticket before you finish**, or you will be woken
again for the same reason.

`ticket_update` also takes `title`, `assignee` (an empty string unassigns), and
`fields` as a JSON object of the project's own field values. Only what you pass
changes.

## Several sessions on one ticket

This is normal, not an edge case. When this conversation has run out of road —
you are going in circles, or your last conclusions turned out to be wrong — do
not try to salvage it quietly. Record what is actually known (see wick-notes),
then say plainly that a fresh session on this ticket would do better. The notes
travel; the confusion does not.

To move sessions around explicitly:

```
ticket_attach_session ticket_id=T-4F2A          # attaches THIS session
ticket_detach_session ticket_id=T-4F2A          # detaches THIS session
```

Attaching is idempotent, and both take an explicit `session_id` when you mean a
different one.

## Turning a loose chat into tracked work

A conversation that turns out to be real work can become a ticket in place:

```
ticket_create title="Payment webhook returns 401" attach_current_session=true
```

Its notes then live on the ticket, so the next session inherits them.
