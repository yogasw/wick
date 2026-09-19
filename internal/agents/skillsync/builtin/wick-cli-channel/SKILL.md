---
name: wick-cli-channel
description: Use when work will outlive the turn that started it — a build, a deploy, a migration, a long test run — and you want to be TOLD how it went instead of guessing when to check. Also use when a script, cron job, or CI step on this host should be able to speak into this conversation, or should show its PROGRESS while it runs. Covers minting a session-bound token (wick_cli_token), `support-tools agent send` (report, wakes the session) and `support-tools agent todo` (progress into the checklist panel, no wake-up), their exit codes, and why this beats scheduling a wake-up.
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
wick_cli_token                                   # 30m
wick_cli_token {"ttl": "90m", "note": "build 0.1.261"}
```

There is no revoke and no list: a token is a signed statement rather than
a row somewhere, and at a two-hour ceiling waiting it out IS the answer.
To cancel every outstanding one at once, rotate the app's session secret.

You get back the token, the `base_url` to hit, the exact `endpoints`, when
it expires, and a ready-to-paste command.

**The channel only answers this machine.** A request from another host is
refused before the token is even read, and so is one that arrived through
a proxy (the forwarding headers give it away). The work this exists for
runs here, so off-box access is not a use case — and a leaked token is
worth nothing anywhere else.

The address is therefore always loopback: `127.0.0.1:<port>`, or
`localhost:<port>`. Both are tried because a host allowlist can name one
and not the other. If neither answers, add it —
`support-tools config allowed-origins add http://127.0.0.1:<port>` — which
is a configuration change, not a hole in the rule.

**The address is verified before you get it.** Minting probes the
candidates with the token it just made and hands back the one that
answered `200`; `verified: true` means that exact token reached that exact
URL a moment ago. If nothing answered you get `verified: false` with the
error and the list of addresses tried — do not hand that token to a job
until it is sorted, because the job would only discover it at the end,
with a result it cannot deliver. It is bound to **this** session — the endpoints
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

## 2b. Progress while it runs — `agent todo`

`send` is for things somebody has to react to. A run that says "3 of 9
packages" every minute is not one of them: each message wakes the session
and costs a turn, for news nobody has to act on.

`agent todo` writes the session's **checklist panel** instead. Nothing
wakes, nothing is charged, and the panel survives a reload — so the person
waiting can look whenever they like and see where the job is.

```bash
support-tools agent todo --item gate --title "Unit gate" --status running \
    --done 3 --total 9 --unit packages
support-tools agent todo --item gate --status running --done 7 --total 9 \
    --detail-file gate.log --format text          # the tail, under the item
support-tools agent todo --item gate --status done
```

Only the item you name is touched, so a script never has to know the rest
of the list — and an item that does not exist yet is created. That is what
makes it usable from a shell: each stage reports itself.

| Flag | What it does |
|---|---|
| `--item` | id (or title) of the item to update; created when new |
| `--status` | `pending` / `running` / `done` / `failed` / `stopped` — the words a script would write, mapped for you |
| `--done` / `--total` / `--unit` | the bar, and what its numbers count |
| `--detail` / `--detail-file` (`-` = stdin) | payload under the item — a log tail, a JSON result |
| `--format` | `text` (default) / `markdown` / `json` / `html` / `xml` |
| `--stop --note "…"` | the run died: the list stops claiming to be in progress |
| `--clear` / `--clear-all` | delete the finished lists (and with `-all`, the live one) |

A payload is clamped to 8000 characters and the **tail** is what is kept —
the end of a log is the part that says what happened.

**Stop the list when the job dies.** A card stuck at "in progress" for a
run that was killed an hour ago is worse than no card: it is the panel
lying about the state of the box.

```bash
trap 'support-tools agent todo --item gate --status failed \
        --detail "$(tail -20 gate.log)" || true' ERR
```

Both commands take the same token, so one export covers them:

```bash
export WICK_CLI_TOKEN=… WICK_BASE_URL=http://127.0.0.1:9424
support-tools agent todo --item build --status running   # progress, silent
support-tools agent send --text "0.1.273 deployed"       # the verdict, wakes
```

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

## Deploying wick itself

This is the case the channel was built for, and the one with a trap in it:
**a turn cannot wait for its own handover.** The outgoing process drains
its in-flight work before exiting, and your turn IS that work — poll for
the new version from inside the turn and you will wait forever.

So put the whole chain in the detached script and end the turn:

```bash
#!/usr/bin/env bash
set -uo pipefail
cd ~/support-tools
if wick build && support-tools reload --binary bin/support-tools-linux-amd64 --sudo -y; then
  # Wait for the successor to actually be serving, then report.
  for _ in $(seq 1 30); do
    sleep 10
    [ "$(pgrep -cf '^/usr/bin/support-tools')" = "1" ] && break
  done
  support-tools agent send --text "deployed $(support-tools version | tail -1)" || true
else
  support-tools agent send --text "BUILD FAILED
$(tail -20 build.log)" || true
fi
```

The token keeps working across the swap by construction: it is a **signed
statement**, not a row in the issuing process's memory, so any process
holding the app's secret can verify it — including a successor that booted
after the token was minted.

Two things decide how long that script sits in its wait loop, and both are
covered by [`wick-zero-downtime-upgrade`](../wick-zero-downtime-upgrade/SKILL.md):
the old process only exits once nothing has been running for the settle
window, and **a conversation that keeps receiving messages never gives it
one** — so ending the turn is part of the deploy, not the end of it.

## When wick is restarting

The likeliest failure is also the most predictable: deploys are when builds
run, so a job often finishes at the exact moment wick is swapping binaries.

`agent send` handles it. An unreachable or still-booting daemon is retried
for **90 seconds** (`--retry 3m`, or `--retry 0` to fail fast), with
backoff, and it says so on stderr while it waits. A handover keeps the port
open and costs nothing; a full restart closes it for the successor's boot,
about 80 seconds on a modest host.

An expired token or a refusal is **not** retried — those are answers, and
repeating them only delays the exit code the script needs.

If the daemon might be down longer than the budget, do not rely on the
message alone:

```bash
echo "$RESULT" > /var/tmp/build-result.txt
support-tools agent send --text "$RESULT" || echo "undelivered; left in /var/tmp/build-result.txt"
```

The next turn reads the file. A report is worth more on disk than lost to a
connection that was never going to answer.

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
- **Progress, via `send`.** Ten "still going" messages are ten wake-ups
  and ten turns; `agent todo` is the same information for free.
- **A cadence** ("check every morning"): that is `wick_schedule_message`,
  which is about a clock rather than an event.
- **Sending to a session that is not yours.** There is no way to, and that
  is the design, not a gap to work around.
