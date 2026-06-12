---
outline: deep
---

# App CLI Reference

The binary produced by `wick build` registers its own command tree. Run `./bin/<app> --help` for the live list — this page documents every subcommand.

For the wick host CLI (`wick init`, `wick build`, `wick doctor`, etc.) see [CLI Reference](./cli).

---

## Default behavior — no args

`./bin/<app>` with no arguments routes by environment:

- **GUI desktop** (Windows / macOS / Linux with `$DISPLAY` or `$WAYLAND_DISPLAY`) → launches the [system tray](../guide/desktop-tray). Tray internally starts the HTTP server and the background worker as goroutines and exposes MCP install / uninstall, preferences, and self-update from the menu.
- **Headless** (Termux, SSH session, Linux server without `$DISPLAY`) → prints a hint and exits:

  ```
  Headless environment detected — tray needs a desktop session.
  Use `<app> start` for background daemon mode, or `<app> all` to run in foreground.
  ```

Headless builds (`wick build --headless`) strip the tray symbols entirely; this command exits non-zero with the same hint regardless of `$DISPLAY`.

---

## Run (foreground)

These commands block the calling shell — `Ctrl+C` exits cleanly. Use them for dev runs, Docker `CMD`, or supervisors like systemd / pm2.

### `<app> server`

Start the HTTP server only. Useful for splitting server / worker across containers, or running behind an external process supervisor.

```bash
./bin/myapp server
./bin/myapp server --port 9000              # override the resolved port
./bin/myapp server --host 127.0.0.1         # bind specific interface
./bin/myapp server --host 192.168.1.42      # bind one NIC only (multi-network host)
./bin/myapp server --localhost              # shortcut for --host 127.0.0.1
WICK_HOST=127.0.0.1 ./bin/myapp server      # same, via env
```

**`--host <addr>` / `--localhost` / `WICK_HOST`** — pin the listen interface. Default (all empty / unset) binds `0.0.0.0` so Docker, systemd, and remote-VPS deploys keep working without any flag. Setting `127.0.0.1` makes the kernel drop SYN packets from any non-loopback source — required on Termux phones where unrooted Android has no firewall to keep `:9425` private. Pair with `ssh -L 9425:localhost:9425 -p 8022 user@phone-ip` for remote access.

Precedence when multiple are set: `--host` wins over `--localhost`; both env-overridable via `WICK_HOST` (env only takes effect when no flag is passed).

### `<app> worker`

Start the background job worker only. Pair with `server` in a separate process / container when you want to scale them independently.

### `<app> tray`

Explicit tray subcommand — same as `<app>` with no args on a GUI host. In headless builds this prints `tray not available in headless build` and exits non-zero.

### `<app> all`

Run the HTTP server **and** the cron scheduler in the same process, sharing one `manager.Service`. Use this for single-node deployments where `server` and `worker` can't share a volume (single-container Docker, simple VPS, etc.) — without a shared filesystem a separate worker pod can't see provider-storage files restored on the API pod, so jobs that touch those files silently no-op.

```bash
./bin/myapp all
./bin/myapp all --port 9000
./bin/myapp all --host 127.0.0.1   # bind specific interface
./bin/myapp all --localhost        # shortcut for --host 127.0.0.1
```

Trade-offs vs. running `server` + `worker` as separate processes:

| Aspect | `all` (single-node) | `server` + `worker` (multi-pod) |
|---|---|---|
| Filesystem | One pod, no volume sharing needed | Needs shared volume (or per-pod restore) |
| Scaling | Vertical only (one process) | Horizontal (scale worker independently) |
| Cron double-fire | Impossible (one `manager.Service`) | Possible if you accidentally run two workers |
| Failure isolation | HTTP crash kills cron too | Independent |

The scheduler goroutine auto-respawns with exponential backoff (2s → 30s cap) if it exits unexpectedly. Clean shutdown via `SIGTERM` / `Ctrl+C` stops both.

---

## Daemon (background)

The daemon commands spawn the binary detached from the calling shell so the parent terminal can exit without killing it. A PID file under `~/.<app>/run.pid` tracks the running instance; output goes to `~/.<app>/daemon.log`.

### `<app> start`

Spawn the binary in the background. Mode is chosen at runtime:

- **GUI host** → spawns `<app> tray` detached (the interactive icon, with its own autostart / server / worker toggles)
- **Headless host** → spawns `<app> all` detached (server + worker, no UI)

```bash
./bin/myapp start
./bin/myapp start --host 127.0.0.1   # bind specific interface — child inherits WICK_HOST
./bin/myapp start --localhost        # shortcut for --host 127.0.0.1
# started myapp as `tray` (pid 12345)
#   log: ~/.myapp/daemon.log
#   pid: ~/.myapp/run.pid
```

`--host` / `--localhost` (and `WICK_HOST=...`) propagate to the spawned child via process env, so both `tray` (GUI) and `all` (headless) modes honor the chosen bind interface. See [`server --host`](#app-server) for the full security rationale.

Re-running `start` while a live daemon is recorded is a no-op — prints `already running (pid N)` and exits 0.

### `<app> stop`

Send `SIGTERM` to the daemon, wait up to `--timeout` (default `5s`) for graceful exit, then `SIGKILL` if still alive. Removes the PID file even after a force-kill.

```bash
./bin/myapp stop
./bin/myapp stop --timeout 15s
```

Stale PID files (process no longer alive) are silently cleaned up so the next `start` has a fresh slot.

### `<app> restart`

`stop` + `start` in one command. Uses the same mode selection as `start`.

```bash
./bin/myapp restart
./bin/myapp restart --timeout 15s
./bin/myapp restart --host 127.0.0.1   # rebind specific interface on new daemon
./bin/myapp restart --localhost        # shortcut for --host 127.0.0.1
```

### `<app> status`

Report whether the daemon is alive, its PID, approximate uptime (PID file mtime), and the log / PID file paths. `--log N` tails the last N bytes of the daemon log.

```bash
./bin/myapp status
# myapp: running
#   pid:     12345
#   started: 2026-05-30T13:02:28+07:00 (37s ago)
#   log:     ~/.myapp/daemon.log
#   pidfile: ~/.myapp/run.pid

./bin/myapp status --log 1000
# ... last 1000 bytes of the daemon log appended
```

---

## Auto-start service

`<app> service` registers (or removes) the binary as an OS-level auto-start. Routing depends on the runtime environment:

| Host | Backend | Path |
|---|---|---|
| Windows desktop | HKCU Run registry | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\<app>` |
| macOS desktop | LaunchAgent plist | `~/Library/LaunchAgents/<app>.plist` |
| Desktop Linux (DISPLAY set) | XDG autostart | `~/.config/autostart/<app>.desktop` |
| Headless Linux server / Raspberry Pi | systemd-user unit | `~/.config/systemd/user/<app>.service` |
| Termux on Android | Termux:Boot script | `~/.termux/boot/<app>-start` |

GUI hosts share the same registration the [tray's `Auto-start app at login`](../guide/desktop-tray) toggle uses, and `service install` / `service uninstall` also flip `userconfig.AutoStartApp` so the tray checkbox stays in sync. Headless hosts get a daemon-style unit that runs `<app> all` directly.

All backends install into user scope — no `sudo` / admin required.

### `<app> service install`

```bash
./bin/myapp service install
# installed myapp service
#   backend: autostart-gui
#   path:    HKCU\Software\Microsoft\Windows\CurrentVersion\Run\myapp
```

On headless Linux (`systemd-user` backend), `service install` also attempts to auto-enable systemd lingering for the current user so the daemon survives logout and starts at boot with no one logged in. If the host's polkit allows self-linger (most modern distros do), this happens silently. If it fails, `service status` reports the exact command to run manually (see below).

Re-running over an existing install rewrites the unit / entry — handy after the binary moves.

### `<app> service uninstall`

```bash
./bin/myapp service uninstall
# uninstalled myapp service
```

Returns `service not installed` if nothing was registered.

### `<app> service status`

```bash
./bin/myapp service status
# myapp service: installed
#   backend: systemd-user
#   path:    ~/.config/systemd/user/myapp.service
#   active:  true
#   note:    linger enabled — service survives logout and starts at boot
```

When lingering is off (e.g. the host denied the auto-enable during install), the note reads:

```
note:    linger DISABLED — service stops at logout; run: sudo loginctl enable-linger <user>
```

The username shown is the real login name detected at runtime — never a hardcoded value.

On GUI hosts, `status` also flags drift between the OS entry and `userconfig.AutoStartApp` so you can spot a tray checkbox that's out of sync with reality and re-run install / uninstall to re-align.

::: tip Headless vs. GUI on the same machine
On Termux or a Linux box without `$DISPLAY`, `service install` writes a `<app> all` unit / Termux:Boot script. The moment you SSH into a Mac, the same command refuses with a hint pointing back to the tray's autostart toggle — daemon-style auto-start only makes sense without a usable GUI session.
:::

---

## Config

Manage the app-level runtime configs that drive the manager (app URL, allowed origins, etc.). The same rows are editable from the admin UI; the CLI is here for headless setup and scripting.

### `<app> config list`

Dump every config row (secrets masked). Env overrides are flagged.

```bash
./bin/myapp config list
# allowed_origins          = http://localhost:9425
# app_url                  = http://localhost:9425
# default_admin_password   = ********  (overridden by ADMIN_PASSWORD)
```

### `<app> config get <key>`

Print one value — secrets are **not** masked.

```bash
./bin/myapp config get app_url
```

### `<app> config set <key> <value>`

Update one value. Rejects locked rows and rows currently overridden by an env var.

```bash
./bin/myapp config set app_url https://my-deployment.example.com
```

### `<app> config allowed-origins`

A second-level group for managing the host allowlist (URLs that may reach the manager beyond `app_url`).

```bash
./bin/myapp config allowed-origins list
./bin/myapp config allowed-origins add http://192.168.1.42:9425
./bin/myapp config allowed-origins remove http://192.168.1.42:9425
./bin/myapp config allowed-origins autodetect   # interactive LAN whitelist picker
```

`autodetect` scans the host's private LAN IPv4 addresses, prints each as a candidate URL, and prompts which ones to whitelist:

```
LAN access — detected 2 IPv4 address(es) on this device:
    [1] http://192.168.1.42:9425
    [2] http://10.0.0.5:9425

  Whitelist for browser access from other devices?
    a = all       n = none (default)       1,2,3 = pick by number
  Choice [n]:
```

---

## MCP

The binary ships an [MCP](https://modelcontextprotocol.io) server over stdio so Claude Desktop / Cursor / Gemini / Codex / Claude Code can call wick tools as a model context provider.

### `<app> mcp serve`

Run the MCP server over stdio. Spawned by the AI client based on the entry written by `mcp install`. Rarely invoked by hand.

### `<app> mcp install`

Write the binary's MCP entry into the chosen client's config file. Resolves `os.Executable()` so the entry points at the actual built binary, not at `wick`.

```bash
./bin/myapp mcp install                          # all detected clients
./bin/myapp mcp install --client claude          # Claude Desktop only
./bin/myapp mcp install --client claude-code     # writes ~/.claude.json
./bin/myapp mcp install --name custom-server     # override server name
```

`--client` accepts: `claude`, `cursor`, `gemini`, `codex`, `claude-code`, `all`. The default server name is the basename of the current directory.

### `<app> mcp uninstall`

Remove the entry written by `install`. Same flags as `install`.

---

## Version

Three equivalent forms print the embedded release string in one line and exit. The format is stable — `scripts/install.sh`'s probe greps it against the resolved release tag to decide whether a re-install should be skipped.

```bash
./bin/myapp version
./bin/myapp --version
./bin/myapp -v
# myapp version v0.14.13 (wick v0.14.13)
```

The gate sidecar (`<app>-gate`) accepts the same three forms and prints its own version line:

```bash
./bin/myapp-gate version
./bin/myapp-gate --version
./bin/myapp-gate -v
# myapp-gate version v0.14.13
```

The version handler on `<app>-gate` returns **before** the binary reads stdin, so probing it from a script does not interfere with its normal PreToolUse hook path.

---

## Uninstall

### `<app> uninstall`

Clean up per-user OS integrations before removing the binary. By default removes the autostart entry and every MCP client entry the app installed.

```bash
./bin/myapp uninstall                # autostart + MCP entries
./bin/myapp uninstall --mcp=false    # autostart only, leave MCP entries
```

Run this before deleting the binary so login items, autostart entries, and orphan MCP config rows don't pile up on the user's machine.
