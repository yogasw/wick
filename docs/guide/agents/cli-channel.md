# CLI channel — long work reports back

Some work outlives the turn that started it: a build, a deploy, a
migration, a test suite. The agent that kicked it off then has a problem
that has nothing to do with the work — **how does it learn the result?**

Polling is a guess about somebody else's clock. Too early and it reads a
half-finished build; too late and whoever asked has been waiting for
nothing; and a job that dies at 03:00 stays silent until the next check.

The CLI channel turns that around: the **work** reports, and the session
wakes on the message.

```
agent mints a token  →  job runs detached with the token in its env
                                    ↓
                     job finishes and sends a message
                                    ↓
             the session wakes on it, like any other message
```

## Mint a token (MCP only)

```
wick_cli_token                                  # issue, 30m
wick_cli_token {"ttl": "90m", "note": "build 0.1.255"}
wick_cli_token {"action": "list"}               # outstanding, masked
wick_cli_token {"action": "revoke"}             # drop all of this session's
```

The response carries the token, the `base_url` to hit, the exact
`endpoints`, the expiry, and a ready-to-paste command.

Minting **verifies the address first**: it probes the candidates with the
token it just created and returns the one that answered `200`, as
`verified: true`. When none answer you get `verified: false`, the error,
and the addresses tried — a token with an address that does not work is
worse than no token, because the job carries it to the end of the build
before finding out.

::: warning There is no CLI command that mints one
A session id is not a secret — it is in the URL of every web session — so
a `--session` flag on a minting command would let anyone with a shell on
the host speak into anybody's conversation. Minting happens over MCP,
where the call already proves which session it came from.
:::

What a token is:

| | |
|---|---|
| **Bound to one session** | The endpoints take no session id at all, so there is nothing to substitute |
| **Short-lived** | 30 minutes by default, 2 hours maximum |
| **In memory** | A daemon restart voids every outstanding token |
| **Attributed** | It carries the minting user; everything sent is theirs |
| **Narrow** | It reaches `/api/cli/send` and `/api/cli/whoami`. Nothing else — not the ticket API, not the rest of the app |

## Send from a script

```bash
export WICK_CLI_TOKEN="wick_cli_…"       # from the mint call
export WICK_BASE_URL="http://127.0.0.1:9424"

support-tools agent whoami                # which session, how long left

if make build; then
  support-tools agent send --text "build ok: $(git rev-parse --short HEAD)"
else
  support-tools agent send --text "build FAILED
$(tail -20 build.log)"
fi
```

`--file <path>` reads the message from a file, `--file -` from stdin.
Messages over 8000 characters are truncated with a note: send the verdict
and the relevant tail, not the whole log.

Run `whoami` at the **start** of the job. It separates "my token expired"
from "wick is down" before the work begins, rather than after it has
finished and has nowhere to report.

## When wick is restarting

The likeliest failure is also the most predictable: deploys are when builds
run, so a job often finishes at the exact moment wick is swapping binaries.

`agent send` waits it out — an unreachable or still-booting daemon is
retried for **90 seconds** by default (`--retry 3m`, `--retry 0` to fail
fast), with backoff, saying so on stderr meanwhile. A
[handover](/guide/agents/zero-downtime) keeps the port open and costs
nothing; a full restart closes it for the successor's boot, about 80
seconds on a modest host.

An expired token or a refusal is **not** retried: those are answers, and
repeating them only delays the exit code the script needs.

For a result that must not be lost, write it down as well:

```bash
echo "$RESULT" > /var/tmp/build-result.txt
support-tools agent send --text "$RESULT" || echo "undelivered; see /var/tmp/build-result.txt"
```

## Exit codes

| Code | Meaning | What a script should do |
|---|---|---|
| `0` | delivered | carry on |
| `2` | usage — no token, empty message | fix the invocation; nothing was sent |
| `3` | token expired or revoked, or the session is gone | do not retry; the next turn mints a fresh one |
| `4` | wick unreachable on that host | retry, or leave the result in a file |
| `5` | wick answered and refused | report it; a retry fails the same way |

Reporting must never fail the build:

```bash
support-tools agent send --text "done" || echo "could not reach wick ($?)"
```

## When to use something else

- **Work that finishes inside the turn** — just do it and answer.
- **A cadence** ("every morning at 9", "check again in 20 minutes") — that
  is a clock, not an event: use
  [scheduled messages](/guide/agents/scheduled-messages).
- **Reaching a session that is not yours** — there is no way to, by
  design.
