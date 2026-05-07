---
outline: deep
---

# Desktop Tray

Every binary built with `wick build` ships a system tray UI. Run the binary with no arguments and an icon appears in the OS tray with controls to start/stop the HTTP server, start/stop the background worker, install the app's MCP entry into Claude Desktop / Cursor / Gemini / Codex, and self-update from a GitHub release.

The tray is one binary, not a separate executable. The same `./bin/<app>` runs as a tray (no args), HTTP server (`./bin/<app> server`), worker (`./bin/<app> worker`), or MCP server over stdio (`./bin/<app> mcp serve`).

## Menu

```
<app> v<version>  (wick v<wickVersion>)
─────────────────────────────────────
Start server  /  Stop server  (running on :9425)
Open server URL                       (visible while server is up)
Open default password                 (visible while INITIAL_CREDENTIALS.txt exists)
─────────────                         (separator only when server is up)
Start worker  /  Stop worker  (running)
Check for updates
Restart to apply v1.2.4              (hidden until a download is staged)
─────────────────────────────────────
MCP ▶
  Install all detected
  Uninstall all
  Show example config
  ─────────
  Claude Desktop  ✓ installed       ▶
  Cursor — not installed            ▶
  Gemini CLI — not configured yet   ▶
  Codex CLI                         ▶
─────────────────────────────────────
Preferences ▶
  ── Launch ──
  ☐ Auto-start app at login
  ☑ Auto-start server on launch
  ☐ Auto-start worker on launch
  ── Updates ──
  ☑ Auto-update
  ── Config ──
  Open config file
About ▶
  App / Wick / Commit / Built
  Open logs
  Open initial credentials            (hidden after first-login setup)
  Wick Repository
  Wick Documentation
─────────────────────────────────────
Quit
```

The tray icon background color signals state at a glance: gray (idle), blue (server running), orange (worker running), green (both running). A corner badge adds a glyph for higher-DPI tray slots.

## Initial admin credentials

On first boot wick generates a 5-word passphrase for the seed admin user (when `APP_ADMIN_PASSWORD` is unset / left as the historical `"admin"`) and writes it to `~/.<app>/INITIAL_CREDENTIALS.txt` (mode `0600`). The tray surfaces it three ways:

- **Tray menu — Open default password** appears beneath `Start/Stop server` while the file exists. Clicking opens it in the default text editor for copy-paste.
- **Toast on first boot** points at the menu so the operator notices.
- **About → Open initial credentials** is the same file, kept around as a fallback.

The first time the operator logs in, wick force-redirects to `/profile/setup` (email + password rotation). Once that's done, `admin_password_changed` flips, the file is deleted, and all three surfaces disappear on the next tray refresh.

Headless / CLI runs (`wick server`) print the credentials to stdout instead — useful for `docker logs` or `journalctl`.

## Open server URL

Visible only when the server is running. Opens `http://localhost:<port>` in the user's default browser. The handler reads `serverPort` live, so port changes between starts are picked up without re-rendering the menu.

## Server / worker toggles

Both the HTTP server and the background worker run in-process as cancellable goroutines spawned from the tray binary. The toggle starts the goroutine; clicking again cancels its context and waits for clean shutdown.

A crash logs the error and resets the menu to "stopped" — the tray itself does not exit.

`Auto-start server on launch` (default `true`) and `Auto-start worker on launch` (default `false`) decide what runs when the tray opens. Toggling these from the menu only takes effect on the **next launch** — they don't start or stop a running process.

## MCP install

Each detected MCP client gets its own submenu. The label after each client name shows status:

| Label | Meaning |
|---|---|
| `<client>  ✓ installed` | Entry exists in the client's config |
| `<client> — not installed` | Config file exists, no entry yet |
| `<client> — not configured yet` | Client's config directory does not exist |

`Install all detected` walks every detected client and writes the binary's MCP entry. `Uninstall all` removes them. Each submenu also has per-client `Install / update`, `Uninstall`, and `Open config`.

`Show example config` writes the JSON snippet to a temp file and opens it in the default editor — for manual paste or reference.

The same `internal/mcpconfig` package backs both the tray menu and the headless [`<app> mcp install`](/reference/cli#wick-mcp) subcommand.

## Self-update

The tray ships with a GitHub release self-updater. It is opt-in at build time — pass `--release-github-pat` and `--release-github-repo` to [`wick build`](/reference/build) (or set `RELEASE_GITHUB_PAT` / `RELEASE_GITHUB_REPOSITORY` in CI). When unconfigured, About shows `Updates: not configured` and `Check for updates` is hidden.

Behavior with `auto_update` enabled (default):

1. On launch, if a binary was staged in the previous session, apply it and re-exec — before the tray menu appears.
2. Otherwise spawn a background check against `<owner>/<repo>/releases/latest`.
3. If a newer version is found, download the matching `<app>-<os>-<arch>` asset to `~/.<app>/updates/`, verify SHA256 against the `.sha256` sibling, and stage it.
4. The menu shows `Restart to apply vX.Y.Z` — clicking restarts the binary; quitting and relaunching applies it automatically.

Failures are silent — the menu title surfaces the state (`Up to date (vX.Y.Z)`, `Update check failed (see logs)`, `Update check failed — PAT expired (see logs)`). Detail goes to the log file, not a popup.

`Check for updates` always runs the same flow, even when `auto_update` is off.

For the build-time setup that wires the updater (PAT scopes, releases repo strategy, CI workflow), see the [Build reference](/reference/build).

## OS-level autostart

`Preferences ▶ Auto-start app at login` registers the binary with the OS so it launches when the user logs in. Default off (opt-in).

| OS | Mechanism |
|---|---|
| Windows | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\<app>` |
| macOS | `~/Library/LaunchAgents/<app>.plist` (launchd, `RunAtLoad=true`) |
| Linux | `~/.config/autostart/<app>.desktop` (XDG autostart) |

Everything is user-scoped — no admin / root needed.

When the tray launches with autostart already enabled, it re-writes the entry with the current `os.Executable()` path. Move or rename the binary and the next launch silently fixes the entry.

## File locations

The tray keeps app-owned files in one hidden home directory. All paths are namespaced by the binary's `BuildAppName` (set at build time from `wick.yml`'s `name:` field).

### Config

| OS | Path |
|---|---|
| Windows | `%USERPROFILE%\.app-name\config.json` |
| macOS | `~/.app-name/config.json` |
| Linux | `~/.app-name/config.json` |

`Preferences ▶ Open config file` opens it in the default editor. Toggles in the menu write back atomically.

Schema:

```json
{
  "auto_start_app": false,
  "auto_start_server": false,
  "auto_start_worker": false,
  "auto_update": true,
  "port": 0,
  "log_retention_days": 0,
  "database_path": "",
  "staged_update_path": "",
  "staged_update_version": ""
}
```

`port: 0` means use `PORT` env or the built-in default (9425). `log_retention_days: 0` means keep 7 days. `database_path: ""` means auto-detect (see below). `staged_update_*` are managed by the updater.

### Database

The tray resolves the SQLite path before the app config loads. First non-empty wins:

1. `DATABASE_URL` env (already set explicitly — never overridden)
2. `database_path` in `config.json` (manual override)
3. `<binary_dir>/wick.db` if `wick.yml` sits next to the binary (project mode)
4. `~/.<app>/wick.db` (standalone for downloaded releases)

Standalone mode keeps the DB path stable when the user moves or renames the binary. Project mode keeps the DB next to your source tree during development.

| Scenario | Resolved DB path |
|---|---|
| `wick build` in `myapp/` then run `./bin/myapp.exe` | `~/.myapp/wick.db` (binary in `bin/`, no `wick.yml` sibling) |
| `go build .` in project root then run `./myapp.exe` | `<projectroot>/wick.db` (project mode) |
| User downloads release binary, double-clicks anywhere | `~/.<app>/wick.db` |
| User edits `database_path: "D:\\custom\\my.db"` | `D:\custom\my.db` |
| CI / Docker sets `DATABASE_URL=postgres://...` | Pass-through to that URL |

The `server` and `worker` subcommands run the same resolver, so headless invocations stay consistent with the tray.

### Port

Resolution mirrors the DB path:

1. `PORT` env (untouched if already set)
2. `port` in `config.json` (when `> 0`)
3. Built-in default `9425`

`9425` spells "WICK" on a T9 keypad. Picked to avoid collisions with common dev ports (3000 React, 5173 Vite, 5432 Postgres). Pin a custom port from `config.json` — no `.env` edit needed.

### Logs

zerolog writes to a per-day file in addition to stderr. Filename rolls over at the next launch on a new day; on startup, files older than `log_retention_days` (default 7) are deleted.

| OS | Path |
|---|---|
| Windows | `%USERPROFILE%\.app-name\logs\wick-YYYY-MM-DD.log` |
| macOS | `~/.app-name/logs/wick-YYYY-MM-DD.log` |
| Linux | `~/.app-name/logs/wick-YYYY-MM-DD.log` |

Co-located with `config.json` and `wick.db` under `~/.<app>` so everything an app owns lives in one easy-to-find tree. `os.Stdout` and `os.Stderr` are also piped through, so `fmt.Print` calls and third-party library writes land in the same file.

`About ▶ Open logs` opens today's file. Headless subcommands (`server`, `worker`, `mcp serve`) write to stderr only — no file redirect.

## SQLite cross-process safety

The tray binary, the MCP stdio subprocess, and ad-hoc CLI runs can all touch the same `wick.db`. SQLite is opened with three settings to make this safe:

```go
db.Exec("PRAGMA journal_mode=WAL")
db.Exec("PRAGMA busy_timeout=5000")
sqlDB.SetMaxOpenConns(1)
```

WAL gives reader/writer concurrency across processes. `busy_timeout=5000` makes contending writers wait up to 5 seconds instead of returning `SQLITE_BUSY`. `MaxOpenConns(1)` serializes writers within a single Go process so the connection pool doesn't open two writers against one file.

PostgreSQL connections are unaffected.

## Headless builds

Embedded `fyne.io/systray` requires user-session APIs that don't exist in Docker images or remote SSH sessions. For server-side deploys, build with `--headless`:

```bash
wick build --headless -o myapp-server
```

This adds `-tags headless` to the underlying `go build`. The tray subcommand becomes a stub that prints `tray not available in headless build` and exits non-zero. `server`, `worker`, `mcp serve`, `mcp install`, and `mcp uninstall` keep working.

## Single instance

The tray acquires a per-app PID-file lock at `~/.<app>/instance.pid` and verifies the recorded PID is still alive and points at the same executable basename. A second invocation of the same binary finds the lock held and exits silently. Two different wick-built binaries (`acme-tools` vs `widget-tools`) live in their own files and don't lock each other out. A crashed instance leaves a stale PID; the next launch detects the dead PID and reclaims the slot.

## See also

- [`wick build` reference](/reference/build) — flags, CI templates, PAT scopes for the self-updater
- [Environment Variables](/reference/env-vars) — `PORT`, `APP_NAME`, `RELEASE_GITHUB_PAT`, etc.
- [CLI Reference](/reference/cli) — full subcommand list including `<app> tray`, `<app> mcp serve / install / uninstall`
