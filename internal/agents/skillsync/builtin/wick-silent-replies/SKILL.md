---
name: wick-silent-replies
description: Use when finishing a turn nobody needs to read — a monitor or poll that found nothing new, a scheduled run mid-sequence, bookkeeping between steps. Covers the exact `[silent]` marker that keeps a reply out of Slack/Telegram and push notifications, where it must sit, what breaks it, and when to reply normally instead.
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
  visible answer, even a short one

When in doubt on a user-initiated turn, reply normally. Silence is for turns
you or a timer started.

## What silence does not do

It does not hide the reply — the web UI still shows it, so never stash
anything there you would not want read. And it is per-turn: the next reply is
loud again unless it also opens with the marker.
