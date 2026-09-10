---
name: wick-zero-downtime-upgrade
description: Use when replacing the wick binary on a running host, or when an update caused downtime — a 502/503 during deploy, killed agent turns, an interrupted workflow run, two wick processes at once, "upgrade failed: parent hasn't exited", or a reload that did nothing. Covers reload vs restart, the systemd unit a handover needs, what the drain does and does not wait for, and how to prove there was no downtime.
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
- `TimeoutStartSec=infinity` — **not `0`**. In systemd, `TimeoutStartSec=0` means *time out immediately*, and the unit fails the moment it starts. Use the word `infinity`. Wick reports ready as soon as it holds the socket (before the slow boot steps finish), but a big restore should never be racing a start timeout.
- `ExecReload` — makes `systemctl reload <app>` the upgrade command.

After editing the unit: `systemctl --user daemon-reload`. A host already running an older binary needs **one** last `restart` to get onto a binary that understands SIGHUP; every upgrade after that is a reload.

## Doing the upgrade

Install the new binary, then reload:

```bash
cp <new-binary> /usr/bin/<app>.new && chmod +x /usr/bin/<app>.new
mv -f /usr/bin/<app>.new /usr/bin/<app>      # atomic rename
<app> reload
```

The rename matters. Copying **onto** a running binary fails with `ETXTBSY` ("Text file busy"); a rename replaces the directory entry while the running process keeps its old inode, so it can finish its work on the code it started with.

`<app> reload` routes itself: a systemd unit gets `systemctl reload`; a unit with no `ExecReload` is signalled by MainPID; a PID-file daemon is signalled directly. `kill -HUP <pid>` works too.

## What happens, in order

1. SIGHUP → the old process forks the successor and passes it the listening socket.
2. The successor boots and starts serving on that same socket. **This takes as long as a normal boot** — the old process is still answering throughout, which is why nothing 502s.
3. The successor reports ready. The old process stops serving HTTP (the successor now answers every new request) and begins to **drain**.
4. When the drain is done the old process releases the **intake baton** and exits. The successor then starts the channel listeners, cron, and the scheduled-message runner.

The baton (a lock file in the data dir) is what keeps exactly one process accepting new work. Without it, two Slack socket-mode connections would split inbound events between an old and a new binary at random, two cron loops would fire every job twice, and two schedule runners would deliver every message twice. None of that shows up in a test that only checks the port stayed open — it shows up in production as duplicated work.

## What the drain waits for

Two categories, two deadlines, because interrupting them costs different things:

- **Not resumable** — a workflow run stopped mid-node, a cron job mid-write, a connector call, a plugin request, a scheduled-message delivery. Interrupting these loses work, so they are waited for properly: `WICK_DRAIN_TIMEOUT`, default 20 minutes.
- **Resumable** — agent turns. A turn cut short is not lost work: the session is on disk and the next message resumes it. These get a short grace: `WICK_DRAIN_AGENT_GRACE`, default 45 seconds.

The split exists because an interactive session is "in flight" for as long as somebody keeps talking to it. Waiting for it kept the old process alive for hours, which is visible and confusing — two wick processes, only one of them holding intake, so the browser talks to one while the agents run in the other — and it blocks the next upgrade. Set `WICK_DRAIN_AGENT_GRACE=20m` when you would rather let a long debug run finish, and accept the longer overlap; set `0` to hand over immediately.

Every 15 seconds a drain logs what is still outstanding, by name, so a long wait is legible rather than a silent hang.

**Adding a new background subsystem to wick?** Register it, or no drain will ever wait for it:

```go
upgrade.Register("workflow runs", func() int { return r.ActiveRuns() + r.QueuedRuns() })
upgrade.RegisterResumable("agent turns", p.ActiveCount, p.ActiveSessions)   // survives interruption
```

## Proving there was no downtime

Claiming zero downtime from the server's own logs proves nothing — probe from outside, through whatever sits in front of wick, and count:

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

Correlate with the daemon log lines `upgrade: successor is serving`, `upgrade: waiting for …`, and `upgrade: drained, exiting`, and with `systemctl show <app> -p MainPID` before and after — the pid must change while the unit never leaves `active`.

Watch out for two things that look like handover downtime but are not: a **build running on the same host** (compiling saturates a small box and the app degrades under it — build elsewhere, or accept the dip), and a **database that throttles logins** while two processes briefly hold two connection pools.

## Troubleshooting

- **`upgrade failed: parent hasn't exited`** — a previous upgrade's old process is still draining, and only one handover can be in flight at a time. Look at what the drain is waiting for in the log; it exits on its own when that finishes, or at the timeout.
- **Reload did nothing / SIGHUP killed the daemon** — the running binary predates graceful upgrade, or `WICK_GRACEFUL_UPGRADE` is not set. Restart once onto the new binary with the env var in place.
- **Unit went `activating` and then failed at once** — `TimeoutStartSec=0`. Change it to `infinity`.
- **The successor dies seconds after a clean handover** — something is handing the child a pipe whose reader dies with the parent. Wick forks with the process's *real* stdio for exactly this reason: a write to fd 1 or 2 on a broken pipe raises SIGPIPE, and Go makes that fatal.
- **The unit starts deactivating right after a reload** — a draining parent must never send `STOPPING=1`; it is no longer the unit's main process, and systemd reads that as the whole service going down and kills the successor.
- **An agent turn loses its wick tools mid-run** — known gap: an agent's MCP credential is minted per spawn and is only known to the process that minted it, so after a handover the successor answers `401`. Non-MCP tools keep working and MCP returns on the next spawn. Finish connector-heavy work before reloading, or raise the agent grace so the turn completes first.
