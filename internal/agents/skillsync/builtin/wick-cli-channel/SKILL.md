---
name: wick-cli-channel
description: Use when work will outlive the turn that started it — a build, a deploy, a migration, a long test run — and you want to be TOLD how it went instead of guessing when to check. Also use when a script, cron job, or CI step on this host should be able to speak into this conversation. Covers minting a session-bound token (wick_cli_token), the `support-tools agent send` command, its exit codes, and why this beats scheduling a wake-up.
---

# Talking to this session from a shell

Some work does not fit in a turn. A build takes four minutes, a deploy
takes twenty, a migration takes as long as it takes. You start it, and
then you have a problem that is not about the work at all: **how do you
find out it finished?**

The old answer was to schedule a wake-up and look. That is a guess about
somebody else's clock — too early and you look at a half-finished build,
too late and the person waiting has been waiting for nothing. And a build
that dies at 03:00 stays silent until the next poll.

The better answer: let the WORK do the telling.

```
mint a token  →  run the job detached with the token in its env  →  end the turn
                                     ↓
                 the job finishes (or fails) and sends a message
                                     ↓
                 this session wakes on it, like any other message
```

## 1. Mint a token

```
wick_cli_token            # action defaults to issue
wick_cli_token {"ttl": "90m", "note": "build 0.1.255"}
```

You get back the token, the `base_url` to hit, when it expires, and a
ready-to-paste command. It is bound to **this** session — the endpoints
take no session id, so there is nothing to point elsewhere — it expires
(30 minutes default, 2 hours max), it dies with the daemon, and everything
sent with it is attributed to you.

**There is no CLI command to mint one, deliberately.** Session ids are not
secret; a shell that could mint from one would be a way to speak into
anybody's conversation on this host.

## 2. Hand it to the work

```bash
systemd-run --user --unit=my-build \
  --setenv=WICK_CLI_TOKEN="$TOKEN" \
  --setenv=WICK_BASE_URL="http://127.0.0.1:9424" \
  /path/to/build-and-report.sh
```

…where the script ends with the report:

```bash
if make build; then
  support-tools agent send --text "build ok: $(git rev-parse --short HEAD)"
else
  support-tools agent send --text "build FAILED
$(tail -20 build.log)"
fi
```

`--file <path>` and `--file -` (stdin) work too; anything over 8000
characters is truncated with a note rather than refused. A build log is
not a message — send the verdict and the last few lines, not the log.

## 3. Then end your turn

Say what you started and stop. The message wakes the session when it
arrives; there is nothing to poll and nothing to schedule.

## Check the token before you rely on it

```bash
support-tools agent whoami     # which session, how long left
```

Run this at the START of the job, not at the end: it separates "my token
expired" from "wick is down" before the work begins, rather than after it
has already finished and has nowhere to report.

## Exit codes — branch on these

| Code | Meaning | What the script should do |
|---|---|---|
| 0 | delivered | carry on |
| 2 | usage: no token, empty message | fix the invocation; nothing was sent |
| 3 | token expired/revoked, or the session is gone | do not retry — mint a new token next turn |
| 4 | wick unreachable on that host | retry, or write the result to a file for later |
| 5 | wick answered and refused (e.g. no agent in the session) | report it; a retry will fail the same way |

A reporting failure must never fail the build:

```bash
support-tools agent send --text "done" || echo "could not reach wick ($?)"
```

## When NOT to use this

- **Work that finishes inside the turn.** Just do it and say the answer.
- **A cadence** ("check every morning"): that is `wick_schedule_message`,
  which is about a clock rather than an event.
- **Sending to a session that is not yours.** There is no way to, and that
  is the design, not a gap to work around.
