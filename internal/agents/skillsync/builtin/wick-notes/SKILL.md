---
name: wick-notes
description: Use when the session's prompt says notes are waiting, when picking up work someone (or some earlier session) already started, when you learn something a later session would otherwise have to rediscover, or when the user mentions notes, handover, or "what did we find last time". Notes work on a plain session too — no ticket required. Tickets themselves are the separate wick-tickets skill.
---

# Notes

Notes are the running record of what has been learned about a piece of work:
findings, decisions, dead ends, and what to do next. They outlive the
conversation that produced them, which is what lets a fresh session pick up
where a derailed one left off.

Use the `notes` connector. It works with or without a ticket — a session
attached to a ticket reads and writes the **ticket's** notes (shared with every
other session on it); a session attached to nothing keeps its own.

## Read them before you work

Your prompt tells you how many notes are waiting. The **bodies are not in your
prompt** — deliberately, because a long-lived ticket would otherwise charge its
whole history on every turn.

So when the pointer says there are notes and you are about to do real work
rather than answer a passing question:

```
notes_list
```

No arguments needed: the scope defaults to this session, which resolves to the
ticket's notes when it belongs to one. Reading them is cheaper than
rediscovering what they say, and much cheaper than contradicting it.

## Write one when you learn something

The test: **would a later session, or a person, have to work this out again?**
If yes, write it down.

```
notes_add body="Retries fire every 30s because the queue re-enqueues on 5xx.
The 401 comes from the token refresh, not the webhook itself."
```

Worth a note: a root cause, a ruled-out hypothesis, a decision and why, a
constraint you discovered, what you would do next, or a warning that a path
leads nowhere. Not worth a note: a summary of what you just said in chat, or
anything already obvious from the code.

Write one **before** handing off, and before saying a fresh session should take
over — that is the moment the note is worth the most.

## Checkable notes

For something with a done state, make it checkable and close it when it is
done:

```
notes_add body="Confirm the fix on staging" checkable=true
notes_check note_id=<id> done=true
```

This is not a todo list with deadlines. It is a note that happens to have a
box, so a reader can see what has and has not been confirmed.

## Audience

Every note carries an `audience`, which says who it was **written for**:

| Value | Means |
|---|---|
| `ai` | A hint for whoever picks this up next |
| `human` | A handover message for a person |
| `both` | Useful to either — the default |

It is a label, not a permission. You read every audience, including notes
written for people, and improving a vague handover note is a real
contribution — use `notes_update` rather than piling another note on top.

Some notes are **hidden** by their author. Those never reach you at all: you
will not see them in `notes_list`, and there is no way around that by design.

## Editing and deleting

```
notes_update note_id=<id> body="sharper wording"
notes_update note_id=<id> audience=human
notes_delete note_id=<id>     # destructive, not reversible
```

Prefer editing a note that is merely wrong. Leave notes that are simply old
alone — they are the history of the work, and a reader wants to know what was
believed at the time.

## Scope, when you need it explicitly

```
notes_list ticket_id=T-4F2A          # a specific ticket's notes
notes_list session_id=<id>           # a specific session's notes
```

Passing nothing is almost always right. Reach for these only when working on a
ticket other than your own.
