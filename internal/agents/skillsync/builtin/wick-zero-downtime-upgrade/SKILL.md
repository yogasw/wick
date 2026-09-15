---
name: wick-zero-downtime-upgrade
description: Use when deploying or replacing the wick binary on a running host — "how do I ship this build", `reload --binary`, a refused candidate ("refusing to install this binary", sha256 mismatch, "--yes was not given", "cannot replace … as this user") — or when an update caused downtime: a 502/503 during deploy, killed agent turns, an interrupted workflow run, two wick processes at once, "upgrade failed: parent hasn't exited", or a reload that did nothing. Covers reload vs restart, installing the new binary safely, the systemd unit a handover needs, who decides the successor is ready to switch (wick's own boot gate — not an outside probe, and not tableflip), what the drain does and does not wait for, how to keep a running agent turn from being interrupted, how an agent deploys the binary it is itself running inside (detached script + `wick_cli_token`, never polling from the turn), and how to prove there was no downtime.
---

# Upgrading wick without downtime

Wick can replace its own binary while it is serving: a successor process starts, **inherits the listening socket**, and the old process keeps working until it is finished. The port is never closed, so nothing in front of wick (nginx, a load balancer, a browser) ever sees a refused connection.

That is `reload`. It is not the same as `restart`, and the difference is the whole point:

- **restart** — stop, then start. The port is closed for the successor's entire boot (tens of seconds on a loaded host: registry restore, database, connectors), so requests fail for that window, and every in-flight agent turn, workflow run and cron job is killed mid-work.
- **reload** — the successor boots *while the old process keeps serving*, takes over the socket when it is ready, and only then does the old one wind down.

## Turning it on

Graceful upgrade is **opt-in**, because it changes how the process exits:

```
WICK_GRACEFUL_UPGRADE=1
```

Without it, SIGHUP is logged and ignored (better than Go's default, which is to terminate), and `reload` tells you to use `restart` instead.

Supported where descriptor passing exists — **Linux, macOS, BSD** — running as a plain daemon or under a service manager. On **Windows**, and when wick runs inside the tray's in-process supervisor, there is no way to hand a socket to another process: the upgrader degrades to a normal listener and you get the classic stop/start. Nothing needs a build tag or a different command; the same `reload` just reports that it is unavailable.

## Under systemd

A handover means the process systemd forked exits while a *different* process keeps serving. systemd has to be told to follow the new one, or it treats the parent's exit as the service dying and kills the whole cgroup — taking the fresh process with it. Four lines do that:

```ini
[Service]
Type=notify
NotifyAccess=all
TimeoutStartSec=infinity
ExecStart=/usr/bin/<app> all
ExecReload=/bin/kill -HUP $MAINPID
Environment="WICK_GRACEFUL_UPGRADE=1"
```

- `Type=notify` + `NotifyAccess=all` — the successor announces itself with `READY=1` and `MAINPID=<its pid>`. `NotifyAccess=all` is required because that notification comes from a process systemd did not fork itself.
- `TimeoutStartSec=infinity` — **not `0`**. In systemd, `TimeoutStartSec=0` means *time out immediately*, and the unit fails the moment it starts. Use the word `infinity`. Wick sends systemd's `READY=1` as soon as it holds the socket, deliberately before the slow boot steps finish, because a big restore must not blow the start timeout. That is the *service manager's* readiness — it is **not** the signal that flips traffic to the successor, which comes later and from somewhere else entirely (see “Who decides it is ready to switch”).
- `ExecReload` — makes `systemctl reload <app>` the upgrade command.

After editing the unit: `systemctl --user daemon-reload`. A host already running an older binary needs **one** last `restart` to get onto a binary that understands SIGHUP; every upgrade after that is a reload.

## Doing the upgrade

One command installs the new build and hands over to it:

```bash
<app> reload --binary <new-binary>       # --sudo when the target directory is root-owned
```

It inspects the candidate before touching anything, swaps it in atomically, signals the handover, and **returns** — printing `handover started: pid <old> is booting the successor`. It does not block: the boot takes about a minute and a half, the old process serves every request throughout it, so waiting buys a blocked caller and nothing else. An immediate refusal is still caught, because the daemon answers that in milliseconds.

Add `--wait` when a script needs the confirmation — it blocks until the successor is serving, prints `handover done: pid <old> -> <new>, <old version> -> <new version>`, and rolls the binary back if the successor never gets there. `--wait-drain` implies it and also waits for the old process to exit, which is the moment channels, cron and the schedule runner move across.

**From inside an agent turn, do neither.** Install the binary and stop there; the watcher applies it. A reload that waits holds the turn open, and an open turn is itself something the next swap has to reckon with — the agent ends up waiting on a swap that is waiting on the agent. There is a whole section on this below: [Deploying from inside an agent turn](#deploying-from-inside-an-agent-turn).

### Deploying from an agent turn

That trap is wider than `--wait`, and it does not need the agent to be waiting on anything. The drain waits for whatever is running, and **a reply is work** — so a session that keeps answering keeps the predecessor alive, each message restarting the settle window. Two processes sit there, the version never changes, and it reads exactly like a hung handover. It is not hung: end the turn and the swap finishes in seconds. If you are the one deploying, the thing to do after starting the swap is to *stop talking*.

Which leaves the question the waiting was meant to answer — how does the agent learn how it went, if it cannot stay and watch? It gets told. Mint a token with the `wick_cli_token` MCP tool, put the whole chain in a detached script, and end the turn:

```bash
if wick build && support-tools reload --binary ./bin/... --sudo -y; then
  support-tools agent send --text "0.1.x deployed" || true
else
  support-tools agent send --text "build FAILED: $(tail -5 build.log)" || true
fi
```

The report lands as a normal message and wakes the session, whether it arrives in 40 seconds or 40 minutes — and a build that dies at 03:00 says so instead of being discovered on the next check. `agent send` retries an unreachable or still-booting daemon for 90s, so a report sent *during* the handover still gets through; past that it exits 4, which is why a script that must not lose its result writes it to a file as well. Append `|| true`: a deploy that succeeded must not be recorded as failed because its notification could not be delivered.

The token survives the restart it is reporting on because it is **signed, not remembered**. Upgrading *from* a build that predates that — one whose tokens lived in the process's memory — is the one case where the final "deployed" message cannot arrive: the token dies with the process that issued it, at precisely the moment it was for. Expect that report to be missing on the first such upgrade, and read the file instead. See the `wick-cli-channel` skill.

### What it checks

Identity comes from the candidate's embedded build info — the module graph plus the `-X` ldflags `wick build` bakes in. The file is never executed to identify it: running an unknown binary is self-defeating when the whole question is whether it is what you think it is.

- **FATAL, no override** — built for another OS or architecture, or nothing in its module graph depends on wick. A wrong-architecture binary in place is a crash-loop (`status=203/EXEC`) that the service manager retries forever. Also the **same version as the running binary**: a different build wearing the same number makes the update card, `version`, a bug report and the rollback you reach for next all lie about what is serving. The refusal names the smallest version that would be accepted (`0.1.194 -> 0.1.195`). There is no `--force` for it — bump the version.
- **BLOCK, `--force` to proceed** — a different main module, a different `BuildAppName` (that app has its own data dir, unit and paths), or a version older than the one running.
- **WARN** — a wick resolved through a local `replace` tree instead of a released tag.

Two gates sit beside the identity check:

- `--sha256 <sum>` is verified before anything else reads or copies the file. Use it when the binary arrived from CI or from someone else.
- `--yes` is **required when stdin is not a terminal**. A deploy script runs with stdin closed or pointed at `/dev/null`, and a prompt that reads silence as consent is how the wrong binary ships with nobody watching.

If the successor never takes over within `--timeout`, the previous binary is put back and the command exits non-zero. Nothing is down while that happens — the old process only steps aside once a successor reports ready.

### The manual equivalent

Worth knowing, because it is what the command does for you, and what you fall back to on a host whose binary predates the flag:

```bash
cp <new-binary> /usr/bin/<app>.new && chmod +x /usr/bin/<app>.new
mv -f /usr/bin/<app>.new /usr/bin/<app>      # atomic rename
<app> reload
```

The rename matters. Copying **onto** a running binary fails with `ETXTBSY` ("Text file busy"); a rename replaces the directory entry while the running process keeps its old inode, so it can finish its work on the code it started with.

**Install it where the successor will look.** The handover re-execs `os.Args[0]`, resolved through `PATH` — not the inode currently running, which is gone the moment the file is replaced. If the unit says `ExecStart=/usr/bin/<app> all`, that path is the one that must hold the new bytes; anywhere else gives a reload that reports success and brings the old version back. `reload --binary` resolves that path from the running process instead of assuming it.

`<app> reload` routes itself: a systemd unit gets `systemctl reload`; a unit with no `ExecReload` is signalled by MainPID; a PID-file daemon is signalled directly. `kill -HUP <pid>` works too.

## What happens, in order

1. SIGHUP → the old process forks the successor and passes it the listening socket.
2. The successor boots and starts serving on that same socket. **This takes as long as a normal boot** — the old process is still answering throughout, which is why nothing 502s.
3. The successor's **boot gate lifts** — registry restored, sessions and connectors back — and only then does it report ready. The old process stops serving HTTP (the successor now answers every new request) and begins to **drain**.
4. When the drain is done — nothing running, and nothing has run for the settle window — the old process releases the **intake baton** and exits. The successor then starts the channel listeners, cron, and the scheduled-message runner.

The baton (a lock file in the data dir) is what keeps exactly one process accepting new work. Without it, two Slack socket-mode connections would split inbound events between an old and a new binary at random, two cron loops would fire every job twice, and two schedule runners would deliver every message twice. None of that shows up in a test that only checks the port stayed open — it shows up in production as duplicated work.

## Who decides it is ready to switch

**Wick does, about itself.** Nothing outside the process gets a vote, and the library underneath has no opinion: `cloudflare/tableflip` passes a file descriptor and watches a pid — it cannot know whether the registry restored, whether connectors reconnected, or whether a page would render. It waits to be told.

The successor tells it, in two separate steps that are easy to confuse:

| Signal | Sent when | Who listens | What it means |
|---|---|---|---|
| `READY=1` + `MAINPID` (sd_notify) | the moment the inherited socket is being served | systemd | “follow this pid; the unit started” |
| `Ready()` (tableflip) | **after this process's own boot gate lifts** | the predecessor | “I can actually do the work — stand down” |

Only the second one starts the drain. Wick calls it from a goroutine that polls its own `BootGate` until every async boot step has reported in, so traffic is never handed to a process that would still answer the “Booting…” page.

That gate is readable from outside, and it is the honest answer to “is the new one ready yet?”:

```bash
curl -s -H 'Host: <your app_url host>' http://127.0.0.1:<port>/boot-status
# still booting:  {"ready":false,"message":"Restoring sessions and files…"}
# ready to serve: {"ready":true}
```

The `Host` header is not decoration. While the gate is closed the boot-gate middleware answers `/boot-status` before anything else runs, so a bare loopback request works; the moment the gate lifts the request falls through to the normal router and the host allowlist rejects `127.0.0.1` with `Forbidden`. Probing without the header therefore flips from JSON to `Forbidden` at exactly the moment you were waiting for — readable, but only if you know that is what it means.

**Do not use `/health` for this.** `/health` is exempt from the boot gate on purpose — a load balancer must not kill the pod mid-restore — so it answers `200` throughout the entire boot. A probe that only counts `200`s proves the port stayed open (which it did, that is the feature) and tells you nothing about whether the successor can serve. Every other path returns the gate page with `503` until `ready` flips.

That wait has **no deadline**, deliberately. Reporting ready on a timer would do the one thing the handover exists to prevent — move traffic onto a process that cannot serve it. A successor that never finishes booting is bounded from the other side instead, where the failure is harmless: after 3 minutes the predecessor gives up on it, keeps serving, and `reload --binary` puts the old binary back. A slow boot logs its phase every 30s (`boot gate still closed — not reporting ready yet`) so the wait is legible rather than silent.

## What the drain waits for

**Everything this process is doing, with no deadline against it.** One rule for every subsystem — agent turns, workflow runs mid-node, cron jobs mid-write, connector calls, plugin requests, delegations, scheduled deliveries, and the HTTP requests still being handled. Whatever is running is waited for until it is finished, however long that takes, and the moment the last of it settles the process exits.

There used to be two deadlines here, and both did the thing a drain exists to prevent: `WICK_DRAIN_TIMEOUT` (20 minutes) killed a workflow run mid-node, and a fixed 45-second grace cut agent replies off mid-sentence. A ceiling on a drain does not *stop* work, it *kills* it.

### Settled, not expired

The process is settled when both hold:

1. **Nothing is in flight.** Every registered subsystem reports zero. For agent turns that means no session in the `working` or `spawning` lifecycle — provider-agnostic by construction, because the lifecycle is driven by normalised agent events, so claude, codex and anything added later are counted the same way with no provider-specific code in the drain. A turn that runs for three hours is waited for, for three hours; so is a workflow run.
2. **It has stayed at zero for the settle window** — `WICK_DRAIN_QUIET`, default **15 seconds**. Work going quiet for an instant is not work ending: a tool result, a queued message, the next node of a workflow or a sub-agent reporting back lands moments later. Handing over inside that gap interrupts work that had merely paused. Any new work restarts the window.

An **idle** agent subprocess — alive, holding no turn, counting down its auto-kill TTL — is deliberately not a blocker. The next message spawns in the successor, so there is nothing to wait for.

**Requests in flight count; streams do not.** A POST that is still being handled is work, so the HTTP shutdown has no deadline either — it used to be capped at 30 seconds. A stream (`Content-Type: text/event-stream`, or a hijacked websocket) is excluded the moment it identifies itself, because it does not end while a browser is watching it; those connections are dropped at the very end, after everything else has settled, and the client reconnects to the successor.

That last point is the whole reason this used to be a timer. The drain counted `len(active)`, which is *subprocesses*, not turns, and a slot is only released when its process exits. The count stayed non-zero for minutes after everybody had stopped talking, so waiting on it meant waiting on the idle TTL — and the only escape was a deadline, which also cut off the live turns it was supposed to protect. Counting what is actually producing is what makes an unbounded wait terminate on its own.

`WICK_DRAIN_TIMEOUT` is still read, now as an **optional hard cap on the whole drain**, and is **unset by default**. Set it only for an unattended deploy that must finish inside a known time, and accept that it interrupts whatever is still running when it fires. `WICK_DRAIN_AGENT_GRACE` is gone — a host that still sets it gets a warning in the log and nothing else.

Two costs of letting work finish in the old process, both real:

- The old process keeps the MCP credential it minted per spawn, and that credential is known only to the process that minted it. Its agents' own wick tool calls now land on the successor (same socket) and come back `401` — see Troubleshooting. Non-MCP tools are unaffected.
- While the parent is alive the *next* reload is refused with `parent hasn't exited`. Two generations at a time, no more.

One case where the wait genuinely may not converge: the draining process keeps the **intake baton** to the end, so inbound Slack events and cron ticks still land *here* and start new work, which restarts the settle window. On a busy host the handover waits for a real lull. That is the trade the rule asks for — nothing is killed — and the escape is a human forcing the swap, not a timer choosing for them.

Every 15 seconds a drain logs what is still outstanding, by name, so a long wait is legible rather than a silent hang.

**Adding a new background subsystem to wick?** Register it, or no drain will ever wait for it:

```go
upgrade.Register("workflow runs", func() int { return r.ActiveRuns() + r.QueuedRuns() })
upgrade.RegisterResumable("agent turns", p.HandoverBlockerCount, p.HandoverBlockers)   // survives interruption
```

## Deploying from inside an agent turn

An agent deploying the binary it is running inside is the case that breaks
every ordinary instinct, because the agent is part of what the deploy has
to wait for.

**A turn cannot wait for its own handover.** The outgoing process drains
its in-flight work before it exits, and the turn doing the deploying *is*
that work. So a turn that polls for the new version — `support-tools
version` in a loop, waiting for the pid to change, "just checking one more
time" — is waiting for something that cannot happen until it stops. It
does not time out. It waits forever, and from the outside it looks exactly
like the deploy hanging.

The same goes for the conversation around it. **The settle window is a
lull, and a session being talked to never has one**: every message starts
new work and restarts the 15-second window. A handover kicked off in the
middle of a live conversation will sit there, correctly, for as long as the
conversation continues — and each "is it done yet?" is itself another
reason it is not. If a swap must happen while someone is actively chatting,
that is what **Force swap** is for; otherwise the answer is to stop
talking, which is the one thing a waiting agent is least inclined to do.

So put the whole chain — build, install, wait, report — in a detached
script, and let it tell you how it went. That is the
[`wick-cli-channel`](../wick-cli-channel/SKILL.md) skill, and deploying
wick is the reason it exists:

```bash
#!/usr/bin/env bash
set -uo pipefail
D=/path/to/workdir
say() { support-tools agent send --text "$1" || echo "UNDELIVERED: $1" >> "$D/result.txt"; }

"$D/gate.sh" >> "$D/build.log" 2>&1 || { say "[DEPLOY] gate failed — nothing installed"; exit 1; }
wick build    >> "$D/build.log" 2>&1 || { say "[DEPLOY] build failed: $(tail -5 "$D/build.log")"; exit 1; }
support-tools reload --binary bin/support-tools-linux-amd64 --sudo -y >> "$D/build.log" 2>&1 \
  || { say "[DEPLOY] reload refused — the old binary is still serving"; exit 1; }

for _ in $(seq 1 40); do                       # wait for the old process to finish draining
  sleep 10
  [ "$(pgrep -cf '^/usr/bin/support-tools')" = "1" ] && break
done
say "[DEPLOY] done: $(support-tools version | tail -1)"
```

Mint the token with `wick_cli_token`, pass it to the script as
`WICK_CLI_TOKEN`, start the script detached, and **end the turn** — that
last step is not politeness, it is what lets the drain finish.

Four details that decide whether the report actually arrives:

- **The token survives the swap by construction.** It is a signed
  statement, not a row in the issuing process's memory, so the successor —
  a process that did not exist when the token was minted — verifies it from
  the app's secret alone. (A build old enough to keep tokens in memory
  cannot do this: its token dies with the process that minted it, and the
  post-swap report is lost. If that is what you are running, this deploy is
  the last one that loses its report.)
- **`agent send` rides out the restart.** An unreachable or still-booting
  daemon is retried for 90 seconds. A handover costs nothing (the port
  never closes); a full restart costs about 80 seconds.
- **Never let the report fail the deploy.** `|| true`, or append it to a
  file the next turn can read. A result on disk beats one lost to a
  connection that was never going to answer.
- **Report the failures too.** A gate that fails at 03:00 should say so;
  the whole point is not having to wonder.

One more trap, learned the hard way: a test that reads the environment can
pass by hand and fail inside the deploy unit, because the unit has
`WICK_CLI_TOKEN` set and your shell did not. Run the gate in the same
environment the deploy will use.

## Watching it, and forcing it, from the UI

`/admin/advanced/software-update` shows the same thing the drain is looking at, because an operator deciding whether to wait needs to see what they would be interrupting:

- **Update waiting to be applied** — appears only when a build is actually waiting: a binary on disk this process is not the image of (someone installed it and has not reloaded yet), or a staged update nobody has applied. It reads the waiting binary's embedded build info *without executing it*, and shows `0.1.123 → 0.1.124`, where it came from, when it was built, and a **Waiting for** list — the live drain outstanding (`agent turns=2 (sess-x)`, `workflow runs=1`, `http requests=1 (POST /api/…)`). With nothing waiting the card stays hidden; there is nothing to say about a process that is simply running.
- `GET /admin/advanced/software-update/serving` is where that comes from — serving `pid`, app `version`, `graceful`, `forced`, the settle window, `busy[]`, and the `swap` block. It is exempt from the in-flight count, so polling it never shows up as work.
- **Force swap** — the one control on the card, and deliberately the impatient one: it hands over *now* and tells the outgoing process not to wait, with a confirm dialog naming the work that would be cut off. `POST …/software-update/swap?force=1`. It is the only thing that interrupts running work — a person, looking at the list, not a timer. The patient path needs no button (see auto-swap below); the same endpoint without `force` starts a normal handover for anything that wants it. (The updater's own **Apply & restart** button still exists for a staged *download*, on builds that have a release source configured.)

### Auto-swap: a binary installed is a binary applied

Installing a file tells the daemon nothing — it does not watch its own path, which is why a build could sit there indefinitely while the old process kept serving and the version you thought you deployed was not the one answering. A watcher closes that gap: every 15s it checks the exec path (a `stat` first, re-reading the build info only when size or mtime moved) and hands over on its own once **all three** hold:

1. the file differs from the running image — version or build timestamp, so a rebuild of the same version counts;
2. it looked identical one interval ago, so a copy still in flight is never executed;
3. nothing is in flight. The handover would not interrupt anything either way, but firing it mid-turn keeps two processes alive for as long as that work runs. Waiting for idle keeps the swap boring.

A file that fails to take over is remembered and **not retried** — otherwise a broken build is handed over to every 15 seconds forever. A newer build at the same path is a different file and gets its own attempt.

So the normal deploy is: install the binary (`reload --binary` does it in one step; a plain atomic `mv` also works), and it applies itself at the next idle moment. Force is for when "the next idle moment" is not soon enough.

The page detects the swap by **pid**, polling that endpoint: a zero-downtime handover never drops a connection, so there is nothing to detect by going down and coming back. Nothing needs reloading by hand — the poll runs whether or not anything is waiting.

A compact version of the same thing sits in the **admin layout**, so it shows on every `/admin` page: hidden while nothing is waiting, appearing on its own within ~10s when a build lands, and disappearing once the swap completes. A handover that was *attempted and failed* is called out there too (`last_handover.ok=false`), because the failure mode is otherwise invisible — the old process simply keeps serving, which looks exactly like nobody having tried.

## Proving there was no downtime

Two different questions, and they need different evidence.

**“Did the switch actually happen, onto the build I meant?”** — ask wick. `reload --binary` tells you it started, and with `--wait` it tells you it finished:

```
handover started: pid 41233 is booting the successor (v1.9.0 -> v1.9.1)
handover done:    pid 41233 -> 41871, v1.9.0 -> v1.9.1     # only with --wait
```

and the daemon log carries the same story from both sides: `upgrade: SIGHUP received — starting successor process`, `upgrade: successor is serving — draining this process (nothing is killed)`, `upgrade: waiting for non-resumable work to finish`, `upgrade: intake baton handed to the successor`, `upgrade: drained, exiting`. Under systemd, `systemctl show <app> -p MainPID` must show a changed pid while the unit never leaves `active`. `/boot-status` says whether the pid now serving is done booting.

**“Did anything in front of wick see a failure?”** — that one the server genuinely cannot answer about itself. Probe from outside, through whatever sits in front of it, and count:

```bash
while true; do
  printf '%s %s\n' "$(date -Is)" "$(curl -s -o /dev/null -m 5 -w '%{http_code}' https://<host>/health)"
  sleep 0.5
done | tee probe.log
```

Then check that the failures are zero **and** that the log covers the moment of the handover:

```bash
grep -v ' 200$' probe.log        # anything here is real downtime
```

Probe a real page, not `/health` — `/health` is boot-gate-exempt, so it answers `200` even while the successor would still show the “Booting…” page to a user. A probe of `/` catches that window; a probe of `/health` hides it.

Watch out for three things that look like handover downtime but are not: a **build running on the same host** (compiling saturates a small box and the app degrades under it — build elsewhere, or accept the dip); a **database that throttles logins** while two processes briefly hold two connection pools; and a **probe whose own log sits somewhere wick watches**. That last one is the measurement eating the host: an agent session's file tree is polled for changes, so a probe appending a line every half-second can turn that watcher into the busiest process on the box — 150% CPU on a 2-vCPU host, with the app's slowdown looking for all the world like the upgrade's fault. Write probe and watchdog logs outside the watched tree.

## Troubleshooting

- **The test gate passed by hand and failed inside the deploy** — the deploy unit's environment is not your shell's, and the most likely difference is the one the deploy needs: a `WICK_CLI_TOKEN` passed in so the script can report back is also visible to every test the gate runs, so a test covering "no token configured" quietly exercises the opposite case. Run the gate inside the same unit the deploy uses, and have tests that read the environment clear it themselves rather than inheriting whatever is around.
- **A gate that fails on code you did not think you had changed** — it compiles the working tree as it is at that instant, so editing files while it runs tests a state that never existed. Start the gate, then keep your hands off the tree until it reports.
- **`refusing to install this binary`** — the preflight rejected the candidate; the reason is the `FATAL` or `BLOCK` line above it. FATAL is final. BLOCK is a judgement call — read which one fired before reaching for `--force`, because "different app" and "downgrade" fail very differently.
- **`sha256 mismatch`** — the file is not the one the checksum was issued for. Nothing was touched.
- **`stdin is not a terminal and --yes was not given`** — a script or CI step is driving. Pass `--yes` deliberately rather than wiring a terminal in.
- **`cannot replace <path> as this user`** — the binary sits in a root-owned directory. Re-run with `--sudo`, which elevates only the file swap; the handover signal still goes through your own session, where the user service manager lives.
- **`successor did not take over in time`** — the new binary failed to boot. It has been rolled back and the old process is still serving; the daemon log says why the successor died.
- **`upgrade failed: parent hasn't exited`** — a previous upgrade's old process is still draining, and only one handover can be in flight at a time. Look at what the drain is waiting for in the log; it exits on its own when that finishes, or at the timeout.
- **Reload did nothing / SIGHUP killed the daemon** — the running binary predates graceful upgrade, or `WICK_GRACEFUL_UPGRADE` is not set. Restart once onto the new binary with the env var in place.
- **Unit went `activating` and then failed at once** — `TimeoutStartSec=0`. Change it to `infinity`.
- **The successor dies seconds after a clean handover** — something is handing the child a pipe whose reader dies with the parent. Wick forks with the process's *real* stdio for exactly this reason: a write to fd 1 or 2 on a broken pipe raises SIGPIPE, and Go makes that fatal.
- **The unit starts deactivating right after a reload** — a draining parent must never send `STOPPING=1`; it is no longer the unit's main process, and systemd reads that as the whole service going down and kills the successor.
- **The Update page sits on “Restarting…” after a successful upgrade** — an older build. The page used to poll `/health` and wait for it to go **down** before reloading, and a zero-downtime handover never takes it down, so it waited out its ~5-minute cap after a perfectly good swap. It now compares the serving pid instead. If you see this, the upgrade itself still finished — reload the tab.
- **An agent turn loses its wick tools mid-run (`401`)** — was a real gap: the MCP credential is minted per spawn and known only to the process that minted it, so an agent still running in the old process authenticated against a successor that had never issued its token. The live grants now travel with the handover — written 0600 next to the intake baton, adopted by the successor at boot, and the file deleted as it is read. Each grant keeps its original expiry, so nothing is extended; a credential whose spawn died during the handover stays valid in the successor until its TTL rather than being revoked early.
