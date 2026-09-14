---
name: wick-silent-replies
description: Use when finishing a turn nobody needs to read — a monitor or poll that found nothing new, a scheduled run mid-sequence, bookkeeping between steps, progress notes nobody asked for. Also use when you are about to go silent on a thread where somebody typed a question and is waiting for the answer there, to check whether you should. Covers the exact `[silent]` marker that keeps a reply out of Slack/Telegram and push notifications, where it must sit, what breaks it, and when to reply normally instead.
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
  visible answer, even a short one, in the channel they asked from. There is
  no thread where this stops being true (next section)

When in doubt on a user-initiated turn, reply normally. Silence is for turns
you or a timer started.

## Shared work threads: still answer what you were asked

Some sessions live where other people read the thread as a work artifact — a
triage card, an incident thread, a review thread whose visible output IS the
deliverable. That shapes what you may say UNPROMPTED. It does not decide
whether you answer a person.

The test is **is somebody waiting for this reply**, not what kind of thread it
is:

- **Somebody typed it, somebody is waiting → reply loud.** A question or an
  instruction in the thread was typed there by a person expecting the answer
  in that same place. Marking it `[silent]` leaves them watching a thread that
  never replies while the answer sits in a web session they are not looking
  at. Answer where the question came from.
- **Nobody asked → `[silent]`.** Progress notes, bookkeeping, "report
  delivered in N messages", a routine check that found nothing new. No one is
  waiting for those and they bury the record the thread exists to hold.

What does NOT relax: **unprompted loud text is still only the deliverable.**
When nobody has asked you anything, the only things that may appear in the
thread on your own initiative are the work products that thread is for.
Spontaneous narration — "I'll start by reading the skill", "let me check the
logs" — stays out of it, silent or not.

If a note nobody asked for already went out loud, edit that message down to a
one-line note instead of posting a correction under it — a thread of
corrections is worse than the original noise.

## What silence does not do

It does not hide the reply — the web UI still shows it, so never stash
anything there you would not want read. And it is per-turn: the next reply is
loud again unless it also opens with the marker.
