# Headless Server

Run wick as a long-lived process on a remote VPS, bare-metal Linux box, or anywhere without a desktop session. No tray icon, no GUI.

Two ways to run, pick one:

| Mode | Command | When |
|---|---|---|
| **Daemon** | `wick start` / `wick stop` / `wick restart` / `wick reload` | Quick install on a single box — wick manages its own PID file at `~/.wick/run.pid`, logs to `~/.wick/daemon.log` |
| **Foreground** | `wick server` (or `wick all`) | Supervised by systemd / Docker / pm2 — the supervisor handles restart + logs |

Full subcommand list: [App CLI Reference](/reference/app-cli).

## Quickstart (daemon)

```bash
chmod +x wick
./wick setup     # first-boot init — generates credentials, SQLite DB
./wick start     # spawn detached, web UI + worker
./wick status    # PID, uptime, log path
```

Web UI at `http://localhost:9425`. Initial credentials print to the daemon log and land in `~/.wick/INITIAL_CREDENTIALS.txt` (mode `0600`, deleted after first login).

Stop / restart:

```bash
./wick stop
./wick restart
```

To replace the binary **without** closing the port, use `reload` instead of `restart` —
`wick reload --binary <new-binary>` checks the candidate, installs it and hands over in one
step. See
[Updating without downtime](#updating-without-downtime) below.

## Quickstart (foreground)

For Docker, systemd, or any process supervisor:

```bash
./wick setup
./wick server     # web UI only
./wick all        # web UI + worker in one process
```

Blocks the calling shell; `Ctrl+C` exits cleanly. Logs go to stdout.

## Tips & tricks

### Bind beyond localhost

Default is localhost-only. To expose on all interfaces:

```bash
HOST=0.0.0.0 ./wick start    # or ./wick server for foreground
```

Don't ship that to the open internet without a reverse proxy + TLS in front.

### Reverse proxy (Caddy)

Minimal `Caddyfile`:

```
wick.example.com {
  reverse_proxy 127.0.0.1:9425
}
```

Set `APP_BASE_URL=https://wick.example.com` so OAuth callbacks and Slack request signing line up. Mismatch breaks Slack Socket Mode handshakes.

### systemd unit (Linux)

`/etc/systemd/system/wick.service`:

```ini
[Unit]
Description=Wick agent host
After=network.target

[Service]
Type=notify
NotifyAccess=all
TimeoutStartSec=infinity
User=wick
WorkingDirectory=/opt/wick
ExecStart=/opt/wick/wick server
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=5
Environment=WICK_GRACEFUL_UPGRADE=1
Environment=APP_BASE_URL=https://wick.example.com
Environment=HOST=0.0.0.0

[Install]
WantedBy=multi-user.target
```

Then `systemctl enable --now wick`. Logs via `journalctl -u wick -f`.

The last four lines are what make a zero-downtime handover possible, and each one earns its
place:

- `Type=notify` + `NotifyAccess=all` — during a handover the process systemd forked exits while
  a *different* one keeps serving. The successor announces itself with `READY=1` and
  `MAINPID=<its pid>`, so systemd follows it instead of treating the parent's exit as the
  service dying and killing the whole cgroup. `NotifyAccess=all` is required because that
  notification comes from a process systemd did not fork itself.
- `TimeoutStartSec=infinity` — **not `0`**. In systemd, `TimeoutStartSec=0` means *time out
  immediately*, and the unit fails the moment it starts.
- `ExecReload` — makes `systemctl reload wick` the upgrade command.
- `WICK_GRACEFUL_UPGRADE=1` — graceful upgrade is opt-in, since it changes how the process
  exits.

Leave them out and wick still runs; `reload` just reports that it is unavailable and you use
`restart`.

### Updating without downtime

```bash
./wick reload --binary ./wick-new --sudo     # add -y from a script: stdin is not a terminal
```

One command: it checks that the candidate is a wick binary for this app and this architecture,
installs it at the path the daemon will exec, hands over, and waits to confirm the successor
took over — rolling the old binary back if it never does.

By hand, which is the same thing and the fallback on an older binary:

```bash
# 1. install the new binary — rename, never copy over a running one
cp ./wick-new /opt/wick/wick.new && chmod +x /opt/wick/wick.new
mv -f /opt/wick/wick.new /opt/wick/wick

# 2. hand over
systemctl reload wick        # or: ./wick reload, or: kill -HUP $(pidof wick)
```

Install it at the path in `ExecStart`: the handover re-execs `os.Args[0]`, so putting the new
bytes anywhere else gives a reload that succeeds and brings the old version back.

The successor boots — restoring the registry, reconnecting the database and connectors, which
takes as long as any boot — **while the old process keeps answering on the same socket**. Only
when it is ready does the old one stop serving, finish its outstanding work, and exit.

`cp` onto the running binary fails with `ETXTBSY`; the rename is what avoids that, and it lets
the draining process keep running the code it started with.

See [`<app> reload`](/reference/app-cli#app-reload) for what the drain waits for, the two
timeout knobs, and how to prove from outside the host that nothing was dropped.

### Headless build (no GUI libs)

If you're building from source on a server that doesn't have `libgl` / `libx11`, use the headless build flag to drop `fyne.io/systray`:

```bash
wick build --headless -o wick-server
```

The `tray` subcommand becomes a stub; `server`, `worker`, `mcp serve`, `mcp install/uninstall` keep working.

### Postgres instead of SQLite

For multi-instance deploys or higher write throughput:

```bash
DATABASE_URL=postgres://wick:pass@db.local:5432/wick ./wick start
```

SQLite WAL mode handles single-host concurrency fine, but Postgres is the move once you have multiple wick processes hitting one DB.

Restarts skip the full schema migration pass when the model shape hasn't changed since the last boot — a fingerprint is checked against a single-row `wick_schema_state` table instead of re-inspecting the catalog. This matters most over a network hop to Postgres, where the full pass can otherwise take tens of seconds. No config needed; it falls back to a full migration automatically if the fingerprint is missing or stale.

### Log location

zerolog writes per-day files at `~/.wick/logs/wick-YYYY-MM-DD.log` plus stderr. Rotation kicks in on the next launch on a new day; files older than `log_retention_days` (default 7) get deleted.

### Port pinning

`9425` is the default (spells "WICK" on T9). Override via `PORT=8080` env or `port:` in `~/.wick/config.json`.

### Auto-start on boot (no systemd unit)

If you don't want to hand-roll a systemd unit:

```bash
./wick service install     # registers systemd-user unit running `wick all`
./wick service status
./wick service uninstall
```

User-scoped (no `sudo` needed). On a Linux server, `service install` automatically tries to enable systemd lingering for the current user so the daemon survives logout and starts at boot. On most modern distros this succeeds silently. If the host denies it, run `wick service status` — the note field prints the exact `sudo loginctl enable-linger <user>` command for your user. Details: [App CLI — Auto-start service](/reference/app-cli#auto-start-service).

## See also

- [App CLI Reference](/reference/app-cli) — every subcommand (`start`, `stop`, `status`, `service`, `config`, `mcp`)
- [Desktop Tray](/guide/desktop-tray) — for the GUI version
- [Docker](/guide/docker) — containerized headless
- [Environment Variables](/reference/env-vars) — full env list
