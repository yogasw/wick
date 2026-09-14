---
name: wick-silent-replies
description: Use when finishing a turn nobody needs to read — a monitor or poll that found nothing new, a scheduled run mid-sequence, bookkeeping between steps. Also use when the session sits in a thread other people read as a work artifact — a triage card, an incident thread, a report thread — and you are about to answer something that is not the deliverable. Covers the exact `[silent]` marker that keeps a reply out of Slack/Telegram and push notifications, where it must sit, what breaks it, and when to reply normally instead.
---

# Silent replies (`[silent]`)

If nobody needs to read this turn's reply, make the literal marker `[silent]`
the very first thing you output, then continue on the same line:

```
[silent] run 3/5: 200 OK, nothing new
```

That line is the entire mechanism — no tool call, no flag. Reading this skill
does nothing by itself; the marker must appear in the text you emit.

**Effect:** the reply reaches no channel (Slack, Telegram, …) and raises no
push notification. It still records to the conversation — the web session
shows it with a muted-bell chip, marker stripped — so the trace survives.

## The marker is a prefix test

Checked against the start of the whole reply. Case ignored, leading
whitespace and newlines tolerated. Anything else in front breaks it:

| Reply starts with | Result |
|---|---|
| `[silent] …` or `[SILENT] …` | silent ✅ |
| `Checked the endpoint. [silent]` | ❌ marker mid-text |
| any opener, marker on a later line | ❌ whole turn hits Slack — with `[silent]` leaked into it |
| `**[silent]**` or `` `[silent]` `` | ❌ decorated ≠ literal |

The opener case is the one that actually happens: even "Okay, checking…"
before the marker defeats it. No preamble, no bold, no backticks — plain
`[silent]`, first characters.

## Go silent when

- a monitor/poll round found nothing new
- a scheduled run is mid-sequence, not the final one
- ticket bookkeeping with nothing to decide — the sweeper's follow-up prompt
  asks for exactly this
- an intermediate step of work you were told to just do

Keep the text after the marker informative: `[silent] run 3/5: 200 OK` is a
useful trace, `[silent] ok` is not.

## Reply normally when

- the watched thing changed — succeeded, failed, crossed a threshold
- you are blocked, or need a decision, approval, or credential
- it is the final summary of a loop, schedule, or chain
- anything with a cost, a risk, or a surprise in it
- **the user asked a direct question** — a person waiting on you always gets a
  visible answer, even a short one, *unless* the channel is a shared work
  thread and the answer is not the deliverable (next section)

When in doubt on a user-initiated turn, reply normally. Silence is for turns
you or a timer started.

## When the channel is a shared work thread

Some sessions live where other people read the thread as a work artifact — a
triage card, an incident thread, a review thread whose visible output IS the
deliverable. There, the channel is not your workspace: it is someone's record.
Only deliverables belong in it — the answer, the report, the status change.

Everything else goes `[silent]`, **even when the operator asked you directly**:
questions about format or process, which approach you picked, progress notes,
clarifications back to them. They read it in the web session; the thread stays
readable for the people who only came for the result. This is the one case
that overrides "the user asked a direct question" above.

Two tests before replying loud in such a thread:

- would someone who only wants the outcome want this message in the thread?
- is this the deliverable, or the making-of?

If a process reply already went out loud, edit that message down to a one-line
note instead of posting a correction under it — a thread of corrections is
worse than the original noise.

## What silence does not do

It does not hide the reply — the web UI still shows it, so never stash
anything there you would not want read. And it is per-turn: the next reply is
loud again unless it also opens with the marker.
