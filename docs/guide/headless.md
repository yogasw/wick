# Headless Server

Run wick as a long-lived process on a remote VPS, bare-metal Linux box, or anywhere without a desktop session. No tray icon, no GUI.

Two ways to run, pick one:

| Mode | Command | When |
|---|---|---|
| **Daemon** | `wick start` / `wick stop` / `wick restart` | Quick install on a single box — wick manages its own PID file at `~/.wick/run.pid`, logs to `~/.wick/daemon.log` |
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
Type=simple
User=wick
WorkingDirectory=/opt/wick
ExecStart=/opt/wick/wick server
Restart=always
RestartSec=5
Environment=APP_BASE_URL=https://wick.example.com
Environment=HOST=0.0.0.0

[Install]
WantedBy=multi-user.target
```

Then `systemctl enable --now wick`. Logs via `journalctl -u wick -f`.

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
