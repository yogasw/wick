# Wick Manager — Implementation Plan System Tray

App system tray cross-platform untuk manage wick service lokal. Tinggal **di dalam** binary user (subcommand `tray`, default kalau jalan tanpa argumen) — bukan binary terpisah. Gak ada UI browser — semua aksi via menu tray; feedback via label menu yg auto-update + icon tray yg ganti per state (zero toast spam).

## Urutan implementasi

Status snapshot 2026-05-05. Click item untuk jump ke section detail.

### ✅ Done

1. ✅ **Bootstrap** — `internal/systemtray/{systray,icon,lock,logs,helpers}.go`, subcommand `tray` (+ default no-arg) di `app.Run()`. Detail: [Project structure](#project-structure)
2. ✅ **MCP install/uninstall** — `internal/mcpconfig` shared CLI ↔ tray; auto-detect, per-client status label, bulk action, show example config. Detail: [2. MCP install / uninstall](#2-mcp-install--uninstall)
3. ✅ **Server / worker toggle** — `api.NewServer().Run(ctx, port)` & `worker.NewServer().Run(ctx)` jalan goroutine in-process; cancel via context. Detail: [Run(ctx) sebagai interface boundary](#runctx-sebagai-interface-boundary)
4. ✅ **Logs ke UserCacheDir** — `setupLogFile()` redirect zerolog tee ke `<UserCacheDir>/<name>/wick-YYYY-MM-DD.log` (per-day file + auto-retention, lihat #19). Detail: [Lokasi log](#lokasi-log)
5. ✅ **Tray icon stateful** — `wickIcon(serverRunning, workerRunning)` runtime-generate PNG/ICO, bg color + corner badge per state. Detail: [Tray icon (stateful)](#tray-icon-stateful)
6. ✅ **Single-instance lock** via TCP `127.0.0.1:47829` (`acquireSingleInstance`). Detail: [Catatan implementasi penting](#catatan-implementasi-penting)
7. ✅ **User config** — `internal/userconfig/config.go`, atomic save (`<path>.tmp` → rename), defaults (`auto_start_server=true`, `auto_start_worker=false`, `auto_update=true`). Detail: [User config](#user-config-machine-wide-1-project--1-file)
8. ✅ **Preferences submenu** — toggle auto-start server/worker/update + Open config file. Detail: [4. Preferences](#4-preferences)
9. ✅ **Build vars** — `app.BuildAppName/AppVersion/WickVersion/Commit/Time/GitHubPAT/GitHubRepo` declared di `app/app.go`; `BuildWickVersion/Commit/Time` auto-fill via `debug.ReadBuildInfo()`. Detail: [3. Self-updater](#3-self-updater) (Variabel build-time)
10. ✅ **`wick build` subcommand** — `cmd/cli/build.go` real cobra cmd. Flag: `--app-name`, `--app-version`, `--github-pat`, `--github-repo`, `-o/--output`, `--headless`. Resolution per value: flag → env (`WICK_APP_NAME` / `WICK_APP_VERSION` / `GITHUB_PAT` / `GITHUB_REPOSITORY`) → `wick.yml` (name/version doang) → `"app"` / `"dev"` fallback. Inject ldflags `BuildAppName/AppVersion` + optional `GitHubPAT/Repo`. Honor `GOOS`/`GOARCH` env; `--headless` tambah `-tags headless`. Default output `bin/<app-name>[.exe]`. Detail: [Build & distribution](#build--distribution)
11. ✅ **wick.yml build task** — root + template `wick.yml` last cmd jadi `wick build` (drop hand-rolled `go build -ldflags ...`). Subcommand baca `name:`/`version:` langsung dari `wick.yml`. Detail: [Build & distribution](#build--distribution)

12. ✅ **Self-updater** — `internal/updater/updater.go`. Komponen:
    - `Updater.CheckNow(ctx)` + tray orchestrate startup apply + background goroutine
    - GitHub API `/releases/latest` + asset download by `runtime.GOOS`/`runtime.GOARCH`
    - SHA256 verify lawan `.sha256` sibling
    - stage di `<UserCacheDir>/<app>/updates/`, simpen path+version ke `userconfig.StagedUpdatePath/Version`
    - apply: Linux/macOS `os.Rename` + `syscall.Exec`; Windows rename current → `.old`, rename staged, restart via `exec.Command`
    - menu tray: `Check for updates` (stateful: Checking… / Up to date / Update check failed — PAT expired / etc.), `Restart to apply vX` (hidden sampai download ready)
    - error 401/403 dari GitHub API surface ke menu item title — bukan cuma di log
    - wire `updater.New(...)` di `systemtray.Run` startup + reuse `userCfg.AutoUpdate` toggle
    - `systemtray.Run` terima 4 param tambahan: `commit, builtAt, repo, pat string`; `app.Run()` pass `BuildCommit/BuildTime/GitHubRepo/GitHubPAT`
    - Detail: [3. Self-updater](#3-self-updater)
13. ✅ **Headless build tag** — `//go:build !headless` di semua 5 file `internal/systemtray/` + stub `systray_headless.go` (`//go:build headless`) print error + exit. `--headless` → `-tags headless` di builder (sudah ada di `cmd/cli/build.go`). Detail: [Build tag headless (optional)](#build-tag-headless-optional)
14. ✅ **CI/CD template** — 2 workflow di `template/.github/workflows/`:
    - `auto-tag.yml` — on push main, baca `version:` dari `wick.yml`, push git tag kalau belum ada
    - `release.yml` — on push tag `v*.*.*`, matrix build 6 OS×arch, `wick build` + sha256, `gh release create` ke `<app>-releases`
    - Support same-repo atau separate releases repo via `vars.RELEASES_REPO`
    - Setup PAT (PAT_DOWNLOAD baked, PAT_BUILD CI-only) di-dokumen lengkap di header release.yml
    - Detail: [CI/CD (GitHub Actions)](#cicd-github-actions)
15. ✅ **SQLite WAL + busy_timeout** — `internal/pkg/postgres/gorm.go` set `PRAGMA journal_mode=WAL` + `PRAGMA busy_timeout=5000` per SQLite open. Cross-process concurrency aman buat tray + MCP stdio. `SetMaxOpenConns(1)` tetap (intra-process serialise writers). Detail: [SQLite concurrency](#sqlite-concurrency)
16. ✅ **DB path auto-detect** — `userconfig.ResolveDBPath(appName, customPath)` set `DATABASE_URL` env sebelum `config.Load()`. Resolution order: env > `database_path` config > `<binary_dir>/wick.db` (kalau ada `wick.yml`) > `<UserConfigDir>/<appName>/wick.db`. Dipanggil di `systemtray.Run` (tray) + `serverCmd.RunE` + `workerCmd.RunE` (headless). Detail: [Lokasi DB](#lokasi-db)
17. ✅ **About submenu** — `About ▶ App / Wick / Commit / Built` (info disabled rows) + `Open logs` + `Wick Repository` (github.com/yogasw/wick) + `Wick Documentation` (yogasw.github.io/wick/). Klik link buka di default browser via `openInEditor` (cmd/start, open, xdg-open semua handle URL). Disabled row `Updates: not configured` muncul di About kalau updater gak ada repo.

    Updater state machine (manual click + auto-update on launch reuse same `runCheck()`):
    - `Updater.CheckLatest(ctx)` → fetch + compare semver only
    - `Updater.Download(ctx, info)` → download + verify SHA256 + stage
    - Tray call dua-duanya berurutan → bisa update title di antara fase ("New version X — downloading…")
    - `Updater.CheckNow(ctx)` masih ada sebagai convenience caller non-tray

18. ✅ **Port resolution + custom default** — default port `9425` ("WICK" T9 keypad, jarang collide). `userconfig.ResolvePort(cfg.Port)` set `PORT` env sebelum `config.Load()`. Resolution: env `PORT` > `userCfg.Port` > default `9425`. Pola sama persis DB path.
19. ✅ **Log rotation per-day + retention** — file ganti dari `wick-YYYY-MM-DD.log` jadi `wick-YYYY-MM-DD.log`. On startup `pruneOldLogs` hapus file > `LogRetentionDays` (default 7) hari. `LogRetentionDays` field di config.json. Cuma server + worker (in-process goroutine di tray binary) yg di-tee ke file — MCP serve subprocess tetap stderr-only.
20. ✅ **Drop unused config fields** — `default_project` + `recent_projects` di-hapus. Multi-project switcher gak relevan dengan arsitektur final (1 binary = 1 app = 1 DB; multi-project = install binary terpisah, masing-masing dapat config/DB sendiri).
21. ✅ **OS-level autostart** — `internal/autostart/` cross-platform package (Windows registry HKCU Run, macOS LaunchAgent plist, Linux XDG autostart .desktop). Toggle di Preferences ▶ "Auto-start app at login" (default `false`). Pas Enable, write entry pointing ke `os.Executable()` current path. Pas tray launch dengan AutoStartApp=true, panggil Enable lagi → refresh path otomatis kalau binary pindah/di-rename.

### ⏳ Belum diimplement (ringan, defer sampai ada kebutuhan)

- **Port toggle dari menu tray** — saat ini cuma via env / config.json edit. UI toggle minor convenience, skip kalau gak ada keluhan nyata.
- **Status submenu (uptime, request count, last error)** — observability ringan. Belum ada kebutuhan debug live, defer.
- **`Updates: not configured` actionable hint** — tooltip sudah explain caranya enable, tapi belum link ke doc / build command runner.

### State terakhir

Tray fungsional production-ready buat day-to-day. Semua plan utama (#1–20) selesai. Sisa hanya polish ringan yg di-defer sampai ada kebutuhan nyata (port toggle dari menu, status submenu observability, links di About).

End-to-end flow yg jalan:
- `wick init <app>` → scaffold + workflow CI ke-copy
- `wick build` → binary dengan ldflags inject (name/version/PAT/repo) + cross-compile + headless tag opsional
- Push ke main + bump `version:` → auto-tag.yml → release.yml → binary di `<app>-releases`
- User download / auto-update → tray launch → DB auto-detect ke `%APPDATA%`/binary dir, port resolve ke 9425, log per-day di UserCacheDir, MCP install ke detected client
- Auto-update background goroutine cek release tiap launch (kalau enabled), download → "Restart to apply vX" muncul

Verified manual: `wick init test4` → `wick build` → copy binary ke folder lain → run → DB otomatis ke `%APPDATA%\test4\` (standalone mode), config terpisah per binary, WAL aktif (file `.shm` + `.wal` muncul), port `:9425` listen. Path stabil pas binary dipindah-pindah.

## Stack

- **Go** (latest stable)
- **`fyne.io/systray`** — API tray cross-platform minimal; no cgo di Windows/Linux, cgo cuma di macOS (cocoa)
- **`github.com/sergeymakinen/go-ico`** — encode ICO buat tray icon Windows (PNG buat macOS/Linux)
- **`github.com/rs/zerolog`** — udah dipakai wick; di-redirect ke log file per-OS pas tray jalan
- **Tray icon**: 32×32 di-generate runtime (kotak ijo + huruf "W" putih)
- **Internal packages**: `internal/systemtray` (UI tray), `internal/mcpconfig` (install/uninstall MCP, shared sama wick CLI), `internal/updater` (self-update, lihat di bawah)
- **Self-update**: built-in via `wick build` (PAT + repo di-pass saat build)
- **DB**: pakai yang udah dipakai wick app (Postgres/SQLite) — tray gak butuh DB tambahan

Tray **bukan** binary terpisah. `wick build` produce satu executable: `./bin/app` (no args) buka tray, `./bin/app server` headless, `./bin/app mcp serve` MCP, dst.

## Reuse dari wick (jangan reimplement)

- **`internal/login`** — admin auth ada di balik HTTP admin panel; tray sendiri gak punya login (spawn pakai user OS yg sama). Kalau ada fitur tray yg perlu auth ke server yg jalan, reuse ini.
- **`internal/pkg/postgres`** — DB connection / migrations (udah dipakai `api.NewServer()` & `worker.NewServer()`)
- **`internal/configs`** — config table key-value; preferensi tray-specific simpan di sini
- **`internal/mcp`** + **`internal/mcpconfig`** — runtime MCP + logic install ke config file. Tray panggil `mcpconfig.Install` / `Uninstall` langsung (gak shell-out).
- **`internal/pkg/api.NewServer()`** — HTTP server. Tray jalanin in-process via `Run(ctx, port)`.
- **`internal/pkg/worker.NewServer()`** — background job worker. Tray jalanin in-process via `Run(ctx)`.

Tray itu **library wrapper** yg drive wick services di goroutine — bukan reimplementasi.

## Project & lokasi DB

Wick pakai konsep **project** — directory yg isinya `wick.db` (atau state wick app lainnya), jadi context buat CLI command + MCP server. Tray ngikut:

**Kenapa project-based:**
- CLI command yg context-aware perlu tau project mana yg lagi dioperate (mis. user `cd` ke folder project lalu jalanin command, wick perlu tau project context-nya)
- MCP server di-spawn per-project sama client (Claude/Cursor) dgn project path tertentu
- User bisa punya multi project di mesin sama (dev, staging, client A, client B, dst)

### Resolution order saat startup (PROJECT)

Status: **belum implement** (TODO #18). Saat ini `systemtray.Run` langsung pake CWD.

Plan:
```
1. Flag --project <path>?        ──Yes──> pakai itu
   ↓ No
2. CWD ada wick.db?              ──Yes──> pakai CWD
   ↓ No
3. DefaultProject di pointer config valid? ──Yes──> pakai itu
   ↓ No / invalid
4. Fallback CWD (server boleh fail keras — itu udah cukup feedback)
```

Gak ada first-run picker UI — tray gak bisa prompt. Kalau project salah, user jalan `./bin/app tray` dari CWD yg bener atau set `default_project` di pointer config.

### Lokasi DB (sudah implement)

`userconfig.ResolveDBPath(appName, customPath)` dipanggil sebelum `config.Load()` baik di `systemtray.Run` (tray mode) maupun `serverCmd.RunE` / `workerCmd.RunE` (headless mode). Fungsi set `DATABASE_URL` env supaya `config.Load()` pickup.

Resolution order — first non-empty wins, never overwrite:

```
1. DATABASE_URL env sudah set explicit       ──Yes──> pakai itu, jangan sentuh
   ↓ kosong
2. userCfg.DatabasePath di config.json       ──Yes──> pakai itu (user override manual)
   ↓ kosong
3. <binary_dir>/wick.yml exist?              ──Yes──> <binary_dir>/wick.db (project mode)
   ↓ tidak
4. Fallback: <UserConfigDir>/<appName>/wick.db (standalone / downloaded mode)
```

**Use cases:**

| Skenario | DB path |
|---|---|
| Dev `wick build` di `test4/` → run `./bin/test4.exe` (binary di `test4/bin/`, wick.yml di `test4/`) | `%APPDATA%\test4\wick.db` (standalone — wick.yml gak di sebelah binary) |
| Build `go build .` di project root → run `./test4.exe` (wick.yml ada di sebelahnya) | `<projectroot>/wick.db` (project mode) |
| User download `test4.exe` ke folder mana aja, double-click | `%APPDATA%\test4\wick.db` (standalone, path stabil) |
| User edit `database_path: "D:\\custom\\my.db"` di config.json | `D:\custom\my.db` (override manual) |
| CI / docker set `DATABASE_URL=postgres://...` | pakai env, tray gak override |

Path stabil sekali resolved — pindah binary gak ngubah DB location selama mode-nya sama (standalone tetep `%APPDATA%`, project mode follow binary dir).

### User config (machine-wide, 1 project = 1 file)

File JSON kecil di OS user-config dir, dinamain sesuai `app.BuildAppName` — di-bake saat `wick build` dari field `name:` di `wick.yml` (sekaligus dgn `version:` → `BuildAppVersion`).

| OS | Path |
|---|---|
| Windows | `%APPDATA%\<name>\config.json` |
| macOS | `~/Library/Application Support/<name>/config.json` |
| Linux | `~/.config/<name>/config.json` |

**Build-time injection flow:**

```
wick.yml: name: my-app, version: 0.1.0
    ↓ wick run/build (cmd/cli/run.go inject jadi {{.NAME}} & {{.VERSION}} var)
go build -ldflags "
  -X github.com/yogasw/wick/app.BuildAppName={{.NAME}}
  -X github.com/yogasw/wick/app.BuildAppVersion={{.VERSION}}
" -o bin/app .
    ↓ binary jalan
app.BuildAppName    == "my-app"
app.BuildAppVersion == "0.1.0"
app.BuildWickVersion == "v0.6.4"  // auto-fill dari debug.ReadBuildInfo()
    ↓
systemtray.Run(cwd, BuildAppName, BuildAppVersion, BuildWickVersion)
    ↓
%APPDATA%\my-app\config.json
tray menu top: "my-app v0.1.0  (wick v0.6.4)"
MCP advertise: server version = BuildAppVersion
```

Default kalau `wick.yml` gak punya `name:` / `version:` atau user `go run .` langsung → fallback `"app"` / `"dev"`. `BuildWickVersion` selalu auto-fill kalau binary di-build via go modules (release tag) atau via wick CLI `mcp serve` build (dari VERSION file).

Schema (lihat `internal/userconfig.Config`):

```json
{
  "auto_start_app": false,
  "auto_start_server": true,
  "auto_start_worker": false,
  "auto_update": true,
  "port": 0,
  "log_retention_days": 0,
  "database_path": "",
  "staged_update_path": "",
  "staged_update_version": ""
}
```

**Field:**
- `auto_start_app` (default `false`) — register binary ke OS supaya auto-launch pas user login. Lihat [OS-level autostart](#os-level-autostart)
- `auto_start_server` (default `true`) — saat tray launch, langsung start HTTP server
- `auto_start_worker` (default `false`) — saat tray launch, langsung start background worker
- `auto_update` (default `true`) — self-updater check + download di background
- `port` (default `0` = pakai env `PORT` atau built-in `9425`) — override HTTP listen port. Lihat [Lokasi port](#lokasi-port)
- `log_retention_days` (default `0` = 7 hari) — berapa hari per-day log file disimpan. File lama otomatis dihapus pas tray launch
- `database_path` — override SQLite DB location. Kosong = auto-detect (lihat [Lokasi DB](#lokasi-db-sudah-implement))
- `staged_update_path` / `staged_update_version` — managed self-updater, gak user-facing

Default jalan kalau file belum ada. Toggle dari tray menu nge-overwrite file (atomic write via `<path>.tmp` → rename).

Preferensi per-project (kalau ada — mis. config khusus app yg user setup di admin panel) tetep di wick.db project aktif lewat configs repo wick. Tray sendiri gak nyimpen apa-apa di DB.

## Lokasi port

`userconfig.ResolvePort(cfg.Port)` set `PORT` env sebelum `config.Load()`. Resolution:

```
1. PORT env sudah set explicit       ──Yes──> pakai itu, jangan sentuh
   ↓ kosong
2. userCfg.Port > 0 (config.json)    ──Yes──> pakai itu
   ↓ 0 / kosong
3. Fallback: 9425 (default di env.go)
```

Default `9425` = "WICK" di T9 keypad — dipilih supaya gak collide sama tools dev populer (3000 React, 5173 Vite, 5432 Postgres). Kalau user mau pin port custom, edit `port: 9876` di config.json — gak perlu ubah `.env`.

## OS-level autostart

Toggle dari Preferences ▶ "Auto-start app at login". Default `false` — opt-in supaya user gak surprise pas install.

`internal/autostart` provide 3 fungsi behind per-OS files (`autostart_{windows,darwin,linux}.go`):

- `autostart.Enable(appName)` — write entry yg point ke `os.Executable()` current path
- `autostart.Disable(appName)` — hapus entry
- `autostart.IsEnabled(appName)` — check entry ada/gak
- `autostart.Path(appName)` — string lokasi entry (buat display)

**Mekanisme per-OS:**

| OS | Lokasi entry |
|---|---|
| Windows | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\<appName>` (registry) |
| macOS | `~/Library/LaunchAgents/<appName>.plist` (launchd, RunAtLoad=true) |
| Linux | `~/.config/autostart/<appName>.desktop` (XDG autostart spec) |

Semua user-scoped — gak butuh admin/root.

**Self-healing:** pas tray launch dengan `AutoStartApp=true`, panggil `Enable()` lagi → write entry dengan current `os.Executable()` path. Kalau user pindah binary atau rename, entry stale → next launch refresh otomatis.

**Failure handling:** kalau Enable gagal (mis. registry locked, filesystem RO), title checkbox tetap unchecked + reset `userCfg.AutoStartApp = false` + log error. User aware via `Open logs` di About.

## SQLite concurrency

Tray + MCP stdio = 2 process bisa nulis ke `wick.db` yang sama. `internal/pkg/postgres/gorm.go` set 3 hal di SQLite open:

```go
db.Exec("PRAGMA journal_mode=WAL")    // cross-process reader/writer concurrency
db.Exec("PRAGMA busy_timeout=5000")   // writer wait 5s instead of SQLITE_BUSY
sqlDB.SetMaxOpenConns(1)              // serialise writers within single process
```

**Kenapa ketiganya:**
- **WAL** — solve cross-process. Tray bisa baca pas MCP nulis (vice versa).
- **busy_timeout** — solve write contention. Kalau tray + MCP nulis bareng, yg telat tunggu 5 detik bukan langsung error.
- **MaxOpenConns(1)** — solve intra-process. Go `database/sql` pool bisa buka multiple conn dari satu process; SQLite cuma allow 1 writer per file. Tanpa ini, 2 goroutine nulis lewat 2 conn berbeda → `SQLITE_BUSY` walaupun WAL aktif.

Pattern desktop = serial write (user click → response), bukan high-concurrency loop. Setup ini cukup buat shared `wick.db`. Postgres branch tidak tersentuh — tetep pakai `MaxOpenConns(100)` + `MaxIdleConns(10)`.

## Lokasi log

zerolog di-redirect ke log file pas tray start (selain ke stderr). File pakai per-day naming + auto-retention:

- Filename: `wick-YYYY-MM-DD.log` (ganti otomatis tiap kali tray launch di hari baru)
- Pas startup, file > `LogRetentionDays` hari (default 7) di-hapus

Path-nya ngikut `os.UserCacheDir()`:

| OS | Path |
|---|---|
| Windows | `%LOCALAPPDATA%\<appName>\wick-YYYY-MM-DD.log` |
| macOS | `~/Library/Caches/<appName>/wick-YYYY-MM-DD.log` |
| Linux | `~/.cache/<appName>/wick-YYYY-MM-DD.log` |

Menu tray ada **Open logs** — buka file di editor default OS (`cmd /c start`, `open`, atau `xdg-open`). File-nya append antar run — rotation di luar scope v1.

Mode headless (`./bin/app server`, `worker`, `mcp serve`) gak di-redirect — tetep tulis ke stderr kayak biasa.

## Build & distribution

**Local dev** — zero flag, baca `wick.yml`:

```bash
wick build
# → bin/<wick.yml name>[.exe], no PAT, full tray build
```

**CI / explicit** — flag override yg perlu, sisanya dari env:

```bash
WICK_APP_NAME=myapp \
WICK_APP_VERSION=1.0.0 \
GITHUB_PAT=$PAT \
GITHUB_REPOSITORY=org/myapp-releases \
wick build -o myapp-linux-amd64
```

**Resolution per value:**

| Value | Order |
|---|---|
| App name | `--app-name` → `$WICK_APP_NAME` → `wick.yml name:` → `"app"` |
| App version | `--app-version` → `$WICK_APP_VERSION` → `wick.yml version:` → `"dev"` |
| GitHub PAT | `--github-pat` → `$GITHUB_PAT` |
| GitHub repo | `--github-repo` → `$GITHUB_REPOSITORY` (auto-set GitHub Actions) |

**Tanggung jawab `wick build`:**
- Default: include tray. Opt-out via `--headless` (`-tags headless`) buat container Linux / headless server.
- Inject ldflags ke `github.com/yogasw/wick/app.{BuildAppName,BuildAppVersion,GitHubPAT,GitHubRepo}` (PAT/Repo skip kalau kosong).
- Cross-compile per `GOOS`/`GOARCH` env (inherit ke `go build`).
- macOS native build doang (cgo).

**Strategi repo:**
- `<appName>` — source code (private)
- `<appName>-releases` — binary release doang (private), 2 PAT scoped ke sini doang
- PAT bocor → attacker dapat binary aja, bukan source code

**Kenapa tray gak butuh frontend bake-dist:** udah gak ada frontend. Skip seluruh section yarn / dist / bake-dist dari plan original.

## CI/CD (GitHub Actions)

2 workflow di `template/.github/workflows/` (di-copy ke downstream lewat `wick init`):

1. **`auto-tag.yml`** — on push to main/master:
   - baca `version:` dari `wick.yml`
   - cek `git ls-remote --tags origin v<X>` — kalau sudah ada → skip
   - kalau belum → `git tag` + `git push origin <tag>` → trigger `release.yml`
2. **`release.yml`** — on push tag `v*.*.*`:
   - matrix build 6 OS×arch (windows/darwin/linux × amd64/arm64)
   - install wick CLI: `go install github.com/yogasw/wick@latest`
   - build: `wick build -o <app>-<os>-<arch>(.exe)` (`wick.yml` baca version langsung)
   - sha256 sibling
   - `gh release create` ke `<app>-releases`

### Build matrix

| OS | Arch | Output |
|---|---|---|
| windows | amd64 | `<app>-windows-amd64.exe` |
| windows | arm64 | `<app>-windows-arm64.exe` |
| darwin | amd64 | `<app>-darwin-amd64` |
| darwin | arm64 | `<app>-darwin-arm64` |
| linux | amd64 | `<app>-linux-amd64` |
| linux | arm64 | `<app>-linux-arm64` |

### Setup repo + PAT (2 skenario)

**Skenario A — separate releases repo (recommended buat private app):**

| Setting | Value |
|---|---|
| `vars.RELEASES_REPO` (Actions variable) | `org/<app>-releases` |
| `secrets.PAT_DOWNLOAD` | fine-grained PAT, scope `<app>-releases`, Contents read-only — **baked ke binary** |
| `secrets.PAT_BUILD` | fine-grained PAT, scope `<app>-releases`, Contents read+write — CI only, NOT embedded |

**Skenario B — same repo (source = releases):**

| Setting | Value |
|---|---|
| `vars.RELEASES_REPO` | (kosong → fallback `github.repository`) |
| `secrets.PAT_DOWNLOAD` | fine-grained PAT, scope this repo, Contents read-only — baked ke binary |
| `secrets.PAT_BUILD` | (kosong → fallback `github.token` yg auto-write same repo) |

Setup lengkap step-by-step ada di header komentar `template/.github/workflows/release.yml` — termasuk URL bikin PAT + path GitHub Settings.

### Trigger flow

```
bump version: di wick.yml → push main
    ↓
auto-tag.yml: tag exist? skip : git tag + push
    ↓
release.yml: build matrix 6 → gh release create
    ↓
binary baru di <app>-releases
    ↓
user yg pake versi lama → auto-updater download → install pas restart
```

Bisa juga manual: `git tag v1.2.3 && git push origin v1.2.3` langsung trigger `release.yml` tanpa lewat `auto-tag.yml`.

### Rotasi PAT

Manual, ngikut expiry GitHub fine-grained PAT (default 90 hari):

1. Generate PAT baru di GitHub (scope sama)
2. Update `secrets.PAT_DOWNLOAD` di source repo
3. Bump `version:` di `wick.yml` → push → release baru di-build dengan PAT baru di-embed
4. User auto-update → dapat binary baru → bisa cek update lagi

Auto-rotation gak feasible — GitHub fine-grained PAT gak bisa di-create via API. Cuma kalau gagal tahu lewat error log + menu tray ("Update check failed — PAT expired (see logs)"). Self-healing selama generate baru sebelum lama expire.

### Verify release

Setelah workflow selesai, cek di repo `<app>-releases` → Releases:
- 6 binary dgn naming consistent (`<app>-<os>-<arch>(.exe)`)
- 6 file `.sha256` companion
- Release notes auto-generate dari commit history sejak tag sebelumnya

User yg pake versi lama bakal kena notif via self-updater (kalau `auto_update=true`) atau bisa klik manual `Check for updates`.

### Catatan cross-compilation

`fyne.io/systray` jauh lebih friendly dibanding Wails:
- **Windows**: pure syscall, no cgo
- **Linux**: pure dbus, no cgo, no webkit deps
- **macOS**: cgo (cocoa) — wajib build di runner macOS

Cross-compile Windows arm64 / Linux arm64 dari host amd64 jalan; macOS arm64 → amd64 jalan di runner `macos-latest` yg sama.

## Project structure

```
cmd/
├── cli/                         # wick CLI (scaffolding doang — init, build, dst.)
└── lab/                         # binary smoke-test internal (gak di-ship)

app/
└── app.go                       # entry buat downstream apps. Run() register
                                  # subcommands: tray (default), server, worker,
                                  # mcp serve, mcp install, mcp uninstall, upgrade.

internal/
├── systemtray/
│   ├── systray.go               # menu tray + glue goroutine (//go:build !headless)
│   ├── systray_headless.go      # stub Run() yg print error + exit (//go:build headless)
│   ├── icon.go                  # generator icon 64×64 PNG/ICO (!headless)
│   ├── logs.go                  # redirect zerolog ke <UserCacheDir>/<name>/wick-YYYY-MM-DD.log (!headless)
│   ├── lock.go                  # single-instance lock via 127.0.0.1:47829 (!headless)
│   └── helpers.go               # openInEditor, jsonIndent (!headless)
├── autostart/
│   ├── autostart.go             # shared currentExe()
│   ├── autostart_windows.go     # registry HKCU\...\Run\<appName>
│   ├── autostart_darwin.go      # ~/Library/LaunchAgents/<appName>.plist
│   └── autostart_linux.go       # ~/.config/autostart/<appName>.desktop
├── userconfig/
│   └── config.go                # Load/Save Config + ResolveDBPath + ResolvePort
│                                  # config: <UserConfigDir>/<name>/config.json
│                                  # name = app.BuildAppName (baked saat wick build)
├── mcpconfig/
│   └── install.go               # AllClients/Detected/Find/Install/Uninstall/
│                                  # InstallMany/UninstallMany/SelfEntry/WickEntry/
│                                  # IsInstalled/Locations
├── updater/
│   └── updater.go               # GitHub release check + download + apply
│                                  # New, CheckNow, ApplyStagedAndRestart, CleanupOldBinary
│                                  # pakai PAT + repo embedded via ldflags
└── pkg/
    ├── api/server.go            # Run(ctx, port) error — context-aware
    ├── worker/server.go         # Run(ctx) error — context-aware
    └── postgres/gorm.go         # SQLite WAL + busy_timeout + MaxOpenConns(1)

template/
└── .github/workflows/
    ├── auto-tag.yml             # push main → tag dari wick.yml version
    └── release.yml              # push tag → matrix build + gh release create
                                  # (header komentar = setup PAT step-by-step)
```

Gak ada `cmd/gui/`. Gak ada `frontend/`. Tray cuma Go package yg di-wire jadi subcommand.

## Features

### 1. System tray (satu-satunya UI)

Right-click menu, di-generate saat startup dari state sekarang:

```
<name> v<appVersion>  (wick v<wickVersion>)        (disabled, info)
─────────────────────────────────────
Start server  /  Stop server  (running on :9425)   ← satu toggle
Start worker  /  Stop worker  (running)            ← satu toggle
Check for updates                                  ← stateful (lihat di bawah). Hidden kalau updater not configured.
Restart to apply v1.2.4                            ← hidden sampai download ready
─────────────────────────────────────
MCP ▶
  Install all detected
  Uninstall all
  Show example config
  ─────────────
  Claude Desktop  ✓ installed     ▶
    Install / update
    Uninstall
    Open config
  Cursor — not installed          ▶
    ...
  Gemini CLI — not configured yet ▶
    ...
─────────────────────────────────────
Preferences ▶
  ☐ Auto-start app at login                        ← OS-level (registry / LaunchAgent / .desktop)
  ☑ Auto-start server on launch                    ← in-app (saat tray buka)
  ☐ Auto-start worker on launch
  ☑ Auto-update
  ─────────────
  Open config file
About ▶
  App:    <name> v<appVersion>                     (disabled)
  Wick:   v<wickVersion>                           (disabled)
  Commit: <git short hash>                         (disabled)
  Built:  <RFC3339 build time>                     (disabled)
  Updates: not configured                          (only when not configured, tooltip = setup hint)
  ─────────────
  Open logs                                        ← buka wick-YYYY-MM-DD.log di editor default
  Wick Repository                                  ← github.com/yogasw/wick (open browser)
  Wick Documentation                               ← yogasw.github.io/wick/ (open browser)
─────────────────────────────────────
Quit
```

**`Check for updates` states (stateful title, both manual click & auto-update on launch use same flow):**

```
Default                      : Check for updates
Click / auto-trigger         → Checking for updates…              (disabled)
                                ├─ error                           → Update check failed (see logs)
                                ├─ error 401/403                   → Update check failed — PAT expired (see logs)
                                ├─ already latest                  → Up to date (vX.Y.Z)
                                ├─ already staged (prior session)  → Check for updates  +  Restart to apply vX muncul
                                └─ new version                     → New version vX.Y.Z — downloading…
                                                                      ├─ download fail → Download failed (see logs)
                                                                      └─ ok            → Check for updates  +  Restart to apply vX muncul
```

Implementation: `Updater.CheckLatest(ctx)` (fetch + compare semver only) lalu `Updater.Download(ctx, info)` (download + verify SHA256 + stage). Tray panggil dua-duanya berurutan supaya bisa update title di tengah ("New version X — downloading…"). Background auto-update di-trigger sekali pas tray launch via `runCheck()` yg sama — UI konsisten.

`Updater.CheckNow(ctx)` masih ada sebagai convenience yg call CheckLatest + Download dalam satu shot — buat caller non-tray (mis. headless updater nanti).

**Kenapa "Open logs" di About:** menu utama tray fokus ke action hari-hari (server/worker/update). Open logs jarang dipakai — kebanyakan buat debug — jadi masuk About bareng version info dan link dokumentasi.

**Server toggle:** spawn goroutine yg jalanin `api.NewServer().Run(ctx, port)`. Cancel context buat stop. Pas crash, goroutine log error + reset ke Stopped (icon balik gray).

**Worker toggle:** pola sama persis pakai `worker.NewServer().Run(ctx)`.

**Auto-start saat launch:** dikontrol sama `auto_start_server` / `auto_start_worker` di user config. Toggle dari menu **Preferences ▶ Auto-start … on launch** — efek pas next launch (gak start/stop runtime langsung). Default: server `true`, worker `false`.

**Feedback:** zero toast notif — Windows toast cenderung intrusif + nyangkut di Action Center. Visual feedback dari:
1. Label menu yg auto-update (`Start server` ↔ `Stop server  (running on :9425)`)
2. Tray icon yg ganti per state — bg color + corner badge (lihat section "Tray icon" di bawah)
3. Log file di `<UserCacheDir>/<name>/wick-YYYY-MM-DD.log` buat detail/error

### 2. MCP install / uninstall

**Architecture diagram (informational):**

```
[Client: Claude/Cursor] ──spawns──> [Binary: ./bin/app] ──stdio──> [mcp serve subprocess]
```

MCP **gak** jalan di HTTP server — di-spawn sama client tiap conversation. Tray cuma nulis config sekali aja biar client tau cara launch binary.

**Format config yg ditulis:**

```json
{
  "mcpServers": {
    "<appName>": {
      "command": "<absolute path to ./bin/app>",
      "args": ["mcp", "serve"]
    }
  }
}
```

`command` = `os.Executable()` (resolved sama `EvalSymlinks`). Buat client TOML (Codex), block-nya `[[mcp_servers]]` dgn field `name` / `cmd` / `args`.

**Per-client config paths:**

| Client | Windows | macOS | Linux |
|---|---|---|---|
| Claude Desktop | `%APPDATA%\Claude\claude_desktop_config.json` (atau `%LOCALAPPDATA%\Packages\Claude_*\...\Claude\claude_desktop_config.json` buat Store install) | `~/Library/Application Support/Claude/claude_desktop_config.json` | `~/.config/Claude/claude_desktop_config.json` |
| Cursor | `%APPDATA%\Cursor\User\settings.json` | `~/Library/Application Support/Cursor/User/settings.json` | `~/.config/Cursor/User/settings.json` |
| Claude Code | `.mcp.json` (project root) | sama | sama |
| Gemini CLI | `%USERPROFILE%\.gemini\settings.json` | `~/.gemini/settings.json` | `~/.gemini/settings.json` |
| Codex CLI | `%USERPROFILE%\.codex\config.toml` (TOML!) | `~/.codex/config.toml` | `~/.codex/config.toml` |

Driven sama `internal/mcpconfig` — package yg sama yg dipakai wick CLI. Logic merge buat config JSON / Codex TOML shared + tested lewat dua jalur.

**Auto-detect:** `mcpconfig.Detected(cwd)` return cuma client yg parent config dir-nya ada (Claude Code project-local, selalu di-show). Tray bikin satu submenu per detected client.

**Status label per-client** update tiap habis install/uninstall:
- `<client>  ✓ installed` — entry udah ada
- `<client> — not installed` — config file ada, entry belum
- `<client> — not configured yet` — config file belum dibikin

**Bulk action:** `Install all detected` / `Uninstall all` — refresh status label per client habis aksi (✓ installed / not installed).

**Show example config:** tulis snippet hasil generate ke `%TEMP%\<appName>-mcp-config.json` + buka di editor default — buat manual paste atau referensi.

**Server name di config:** default basename project directory (`filepath.Base(cwd)`). Bisa di-override pas wire dari `app.go`.

**Detail format:**
- JSON client: merge sama `mcpServers` existing (jangan overwrite)
- Codex TOML: append blok `[[mcp_servers]]`; uninstall scan `name = "<app>"` lalu drop blok yg match

### 3. Self-updater

**Default behavior: auto-check + auto-download. User wajib restart buat aktivasi.** Single toggle `auto_update` (default `true`) di config table per-project.

**Flow:**

```
App launch
    ↓
[Ada staged update dari sesi sebelumnya?] ──Yes──> apply + restart (sebelum tray muncul)
    ↓ No
[auto_update = ON?] ──No──> skip (manual via menu tray "Check for updates" doang)
    ↓ Yes
Goroutine background: GET /releases/latest dari <app>-releases
    ↓
[Versi baru ketemu?] ──No──> selesai
    ↓ Yes
Download asset buat runtime.GOOS/runtime.GOARCH ke %TEMP%
    ↓
Verify SHA256 lawan asset .sha256 sibling
    ↓
Stage di <UserCacheDir>/<app>/updates/<app>-<version>(.exe)
    ↓
Save staged path + version ke configs table
    ↓
Toast (non-blocking): "Update v1.2.3 downloaded — Restart to activate"
    ↓ User klik "Restart now" di tray (atau quit — pending apply pas next launch)
[Server / worker jalan?] ──Yes──> stop graceful (cancel ctx) sebelum swap
    ↓
Apply binary swap → re-exec self
```

**Aturan UX:**
- **Background, gak pernah block** — UI tetep responsif
- **Quiet failure** — error check/download gak munculin dialog; silent retry next launch
- **Restart wajib** — download otomatis, aktivasi ngga
- **Auto-apply pas next launch** — kalau user quit aja, staged binary apply sebelum tray baru muncul
- **Idempotent** — re-download skip kalau binary versi sama udah staged
- **Manual trigger** — menu tray "Check for updates" selalu jalanin flow yg sama, bypass `auto_update` toggle

**Implementation outline** (`internal/updater/updater.go`):

```go
package updater

type Config struct {
    AutoUpdate          bool
    StagedUpdatePath    string // empty = no pending
    StagedUpdateVersion string
}

type Updater struct {
    cfg            *Config
    owner, repo    string
    pat            string
    currentVersion string
}

func New(cfg *Config, pat, repo, version string) *Updater { ... }

// CheckOnStartup apply staged update dulu, lalu (kalau enabled) check
// release baru di background. Aman dipanggil dari main / tray onReady —
// gak pernah block.
func (u *Updater) CheckOnStartup(ctx context.Context) {
    if u.cfg.StagedUpdatePath != "" {
        u.applyStaged()  // re-exec, gak pernah return
        return
    }
    if !u.cfg.AutoUpdate {
        return
    }
    go u.checkAndDownload(ctx)
}

// CheckNow jalanin check yg sama secara synchronous (return latest version
// + apakah download terjadi) — dipakai sama menu manual tray.
func (u *Updater) CheckNow(ctx context.Context) (Result, error) { ... }

// RestartIfStaged stop cancel func yg di-pass (server, worker), apply
// staged binary, lalu re-exec. Cuma return error.
func (u *Updater) RestartIfStaged(stops ...context.CancelFunc) error { ... }
```

**Komponen:**
- HTTP client ke GitHub API (`/releases/latest`), `Authorization: Bearer <PAT>`, `Accept: application/octet-stream` buat asset download
- Semver compare (`golang.org/x/mod/semver` aman)
- Asset name di-resolve dari `runtime.GOOS` + `runtime.GOARCH` (match build matrix CI)
- SHA256 di-check lawan `.sha256` sibling
- Binary swap:
  - **Linux/macOS**: `os.Rename(staged, current)` atomic; `syscall.Exec` buat re-exec
  - **Windows**: rename current → `<current>.old`, taro staged di `current`, restart via `os.StartProcess`, hapus `.old` next launch

**Variabel build-time (di-set sama `wick build` via ldflags):**

```go
// app/app.go
var (
    BuildAppName     = "app"      // dari wick.yml `name:`
    BuildAppVersion  = "dev"      // dari wick.yml `version:`
    BuildWickVersion = "dev"      // wick framework semver, auto-fill via debug.ReadBuildInfo()
    BuildCommit      = "unknown"
    BuildTime        = "unknown"
    GitHubPAT        = ""
    GitHubRepo       = ""
)
```

`wick.yml`'s `build` task ldflags inject `BuildAppName` + `BuildAppVersion` dari `{{.NAME}}` / `{{.VERSION}}`. wick CLI `runTask` populate kedua var itu dari top-level `name:` / `version:` field. `BuildWickVersion` auto-fill dari embedded module info (gak perlu ldflag manual). `wick build` juga inject `GitHubPAT` / `GitHubRepo` dari flag self-update — gak ada plaintext secret di source.

### 4. Preferences

Disimpen di `internal/userconfig.Config` (file JSON di OS user-config dir, lihat section "User config" di atas). Tray expose lewat submenu **Preferences** — toggle update field + atomic save ke disk.

```go
type Config struct {
    AutoStartServer     bool     `json:"auto_start_server"`      // default: true
    AutoStartWorker     bool     `json:"auto_start_worker"`      // default: false
    AutoUpdate          bool     `json:"auto_update"`            // default: true
    DefaultProject      string   `json:"default_project,omitempty"`
    RecentProjects      []string `json:"recent_projects,omitempty"`
    StagedUpdatePath    string   `json:"staged_update_path,omitempty"`
    StagedUpdateVersion string   `json:"staged_update_version,omitempty"`
}
```

Toggle effect-nya **next launch**, bukan langsung — auto-start gak ngubah server/worker yg lagi jalan, cuma decide behavior pas tray buka berikutnya. Bikin UX-nya predictable.

**Open config file** menu item buka file di editor default — buat user yg mau edit manual atau backup.

## Catatan implementasi penting

### Run(ctx) sebagai interface boundary

`api.Server.Run(ctx, port)` & `worker.Server.Run(ctx)` keduanya nerima context + return error. Subcommand CLI wrap pakai `signal.NotifyContext(...)`; tray wrap pakai `context.WithCancel` + simpan cancel func. **Ngga ada** `os.Signal` handling di dalam `Run`.

Ini kontrak yg bikin code path sama bisa jalan buat headless deploy (`./bin/app server`) + tray (goroutine in-process).

### Catatan cross-platform process

Tray udah gak spawn subprocess buat server/worker — udah in-process. Code kill process tree udah gone. Self-update tinggal satu-satunya concern process cross-platform (binary swap di Windows, `syscall.Exec` di Unix).

### Tray icon (stateful)

Di-generate runtime di `icon.go` — image RGBA 64×64. PNG buat macOS/Linux, ICO via `go-ico` buat Windows. Gak ada asset icon di repo.

Layout: brand "W" (8-px stroke Bresenham, edge-to-edge) di tengah, plus corner badge di bottom-right (white disk + state-specific glyph). Bg color + badge ngasih sinyal at-a-glance:

| Server | Worker | Bg | W color | Badge |
|---|---|---|---|---|
| stop | stop | gray `#888780` | dim `#c8c7c1` | (none) |
| running | stop | blue `#185fa5` | white | white disk + 3 blue bars (server rack) |
| stop | running | orange `#ef9f27` | white | white disk + orange ring (gear) |
| running | running | green `#1d7d4f` | white | white disk + green ✓ check |

Bg color jadi sinyal primer pas Windows scale ke 16-px tray slot (badge jadi kecil tapi warna tetep beda). Badge baru jelas di high-DPI / 24-px+ tray. Refresh icon dipanggil tiap habis start/stop server/worker dan habis goroutine server/worker exit.

### Build tag headless (optional)

Buat deploy yg gak mau libs tray (Docker container, headless server), tambah tag `//go:build !headless` di `internal/systemtray/systray.go` + stub `Run(...)` di bawah `//go:build headless` yg print "tray not available in headless build" lalu exit. `wick build --headless` pass `-tags headless`.

Gak wajib v1 — `./bin/app server` udah lets user skip tray.

## Open questions

1. **Nama org GitHub** — confirm path `<owner>/<app>-releases` yg di-bake ke binary
2. **Multiple instance** — kalau user double-launch `./bin/app`, dua tray muncul + dua-duanya try `:9425`. Single-instance lock (file lock di bawah `UserCacheDir`) worth ditambah.
3. **macOS code signing** — tray binary unsigned trigger Gatekeeper; defer dulu MVP
4. **DB choice** — wick framework support PostgreSQL (GORM) + SQLite (`glebarez/sqlite`). Buat single-user desktop scenario, SQLite default-nya lebih masuk akal — confirm wick load config respect ini.
5. **Recent projects switching** — pointer config nyimpen `recent_projects[]` tapi tray gak punya UI buat switch. Either drop field-nya dari MVP, atau expose lewat halaman Settings di admin panel.

## Out of scope

- UI berbasis Wails / webview
- UI login / auth di tray (admin panel handle itu di sisi HTTP)
- OAuth login (defer)
- Public release repo (pakai private)
- Code obfuscation (`garble`) — optional hardening, skip MVP
- Telemetry / crash reporting
- Auto-rotation PAT
- Code signing (Apple notarization, Authenticode)
- Log viewer di sisi tray (Open logs → editor eksternal udah cukup)

## Referensi

Plan original berbasis Wails di-preserve di `gui.md` — di-keep buat section project / DB / build-distribution yg masih relevan. Apapun UI-specific di sana (komponen Svelte, frontend dist, Wails event) udah disuperseded sama dokumen ini.
