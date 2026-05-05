# Wick Manager — Implementation Plan System Tray

App system tray cross-platform untuk manage wick service lokal. Tinggal **di dalam** binary user (subcommand `tray`, default kalau jalan tanpa argumen) — bukan binary terpisah. Gak ada UI browser — semua aksi via menu tray + toast notification.

## Stack

- **Go** (latest stable)
- **`fyne.io/systray`** — API tray cross-platform minimal; no cgo di Windows/Linux, cgo cuma di macOS (cocoa)
- **`github.com/gen2brain/beeep`** — desktop notification (Windows toast, macOS NSUserNotification, Linux libnotify/dbus)
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

### Resolution order saat startup

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

### User config (machine-wide, 1 project = 1 file)

File JSON kecil di OS user-config dir, dinamain sesuai `app.BuildAppName` — di-bake saat `wick build` dari field `name:` di `wick.yml`.

| OS | Path |
|---|---|
| Windows | `%APPDATA%\<name>\config.json` |
| macOS | `~/Library/Application Support/<name>/config.json` |
| Linux | `~/.config/<name>/config.json` |

**Build-time injection flow:**

```
wick.yml: name: my-app
    ↓ wick run/build (cmd/cli/run.go inject jadi {{.NAME}} var)
go build -ldflags "-X github.com/yogasw/wick/app.BuildAppName={{.NAME}}" -o bin/app .
    ↓ binary jalan
app.BuildAppName == "my-app"  →  systemtray.Run(cwd, "my-app")
    ↓
%APPDATA%\my-app\config.json
```

Default kalau `wick.yml` gak punya `name:` atau user `go run .` langsung tanpa wick build → fallback ke `"app"` (default `BuildAppName`) atau `filepath.Base(cwd)` (kalau caller pass empty). Project beda → folder beda → preferensi terpisah.

Schema (lihat `internal/userconfig.Config`):

```json
{
  "auto_start_server": true,
  "auto_start_worker": false,
  "auto_update": true,
  "default_project": "D:\\code\\work\\wick",
  "recent_projects": ["D:\\code\\work\\wick", "D:\\code\\work\\client-a"],
  "staged_update_path": "",
  "staged_update_version": ""
}
```

**Field:**
- `auto_start_server` (default `true`) — saat tray launch, langsung start HTTP server
- `auto_start_worker` (default `false`) — saat tray launch, langsung start background worker
- `auto_update` (default `true`) — self-updater check + download di background
- `default_project` / `recent_projects` — pointer project (defer aktivasi sampe ada switcher UI)
- `staged_update_path` / `staged_update_version` — managed self-updater, gak user-facing

Default jalan kalau file belum ada. Toggle dari tray menu nge-overwrite file (atomic write via `<path>.tmp` → rename).

Preferensi per-project (kalau ada — mis. config khusus app yg user setup di admin panel) tetep di wick.db project aktif lewat configs repo wick. Tray sendiri gak nyimpen apa-apa di DB.

## Lokasi log

zerolog di-redirect ke log file pas tray start (selain ke stderr). Path-nya ngikut `os.UserCacheDir()`:

| OS | Path |
|---|---|
| Windows | `%LOCALAPPDATA%\<appName>\wick.log` |
| macOS | `~/Library/Caches/<appName>/wick.log` |
| Linux | `~/.cache/<appName>/wick.log` |

Menu tray ada **Open logs** — buka file di editor default OS (`cmd /c start`, `open`, atau `xdg-open`). File-nya append antar run — rotation di luar scope v1.

Mode headless (`./bin/app server`, `worker`, `mcp serve`) gak di-redirect — tetep tulis ke stderr kayak biasa.

## Build & distribution

Flow sama kayak plan GUI awal:

```bash
wick build \
  --github-pat=$GITHUB_PAT \
  --github-repo=org/<appName>-releases \
  --output=<appName>
```

**Tanggung jawab `wick build` (gak berubah):**
- Default: include tray. Opt-out via `wick build --headless` buat build yg exclude `internal/systemtray`. (Berguna buat container Linux yg gak ada tray libs.)
- Inject PAT + repo via ldflags (dibaca `internal/updater`)
- Cross-compile per `GOOS`/`GOARCH`
- macOS native build doang (cgo)

**Strategi repo:**
- `<appName>` — source code (private)
- `<appName>-releases` — binary release doang (private), 2 PAT scoped ke sini doang
- PAT bocor → attacker dapat binary aja, bukan source code

**Kenapa tray gak butuh frontend bake-dist:** udah gak ada frontend. Skip seluruh section yarn / dist / bake-dist dari plan original.

## CI/CD (GitHub Actions)

Matrix sama kayak sebelumnya, tapi **tanpa** Node/yarn setup atau bake-dist step. Disediain sebagai template di `template/.github/workflows/release.yml`, di-copy lewat `wick init`.

### Build matrix

| OS | Arch | Output |
|---|---|---|
| windows | amd64 | `<app>-windows-amd64.exe` |
| windows | arm64 | `<app>-windows-arm64.exe` |
| darwin | amd64 | `<app>-darwin-amd64` |
| darwin | arm64 | `<app>-darwin-arm64` |
| linux | amd64 | `<app>-linux-amd64` |
| linux | arm64 | `<app>-linux-arm64` |

```yaml
name: Release
on:
  push:
    tags: ['v*.*.*']
permissions:
  contents: read
jobs:
  build:
    name: Build ${{ matrix.os }}-${{ matrix.arch }}
    runs-on: ${{ matrix.runner }}
    strategy:
      fail-fast: false
      matrix:
        include:
          - { os: windows, arch: amd64, runner: windows-latest, ext: '.exe' }
          - { os: windows, arch: arm64, runner: windows-latest, ext: '.exe' }
          - { os: darwin,  arch: amd64, runner: macos-latest,   ext: ''     }
          - { os: darwin,  arch: arm64, runner: macos-latest,   ext: ''     }
          - { os: linux,   arch: amd64, runner: ubuntu-latest,  ext: ''     }
          - { os: linux,   arch: arm64, runner: ubuntu-latest,  ext: ''     }
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: 'stable'
          cache: true

      # Tray Linux gak butuh deps tambahan (fyne.io/systray pakai dbus pure-Go).
      # macOS pakai cocoa via cgo — Xcode CLT udah preinstalled di macos-latest.
      # Windows pakai syscall — gak perlu setup.

      - name: Resolve app name dari go.mod
        id: meta
        run: |
          NAME=$(awk '/^module /{n=split($2,a,"/"); print a[n]}' go.mod)
          echo "app_name=$NAME" >> $GITHUB_OUTPUT

      - name: Build pakai wick
        env:
          GOOS: ${{ matrix.os }}
          GOARCH: ${{ matrix.arch }}
          OUTPUT: ${{ steps.meta.outputs.app_name }}-${{ matrix.os }}-${{ matrix.arch }}${{ matrix.ext }}
        run: |
          wick build \
            --github-pat=${{ secrets.PAT_DOWNLOAD }} \
            --github-repo=${{ github.repository_owner }}/${{ steps.meta.outputs.app_name }}-releases \
            --output=$OUTPUT

      - run: sha256sum ${{ steps.meta.outputs.app_name }}-${{ matrix.os }}-${{ matrix.arch }}${{ matrix.ext }} > $_.sha256

      - uses: actions/upload-artifact@v4
        with:
          name: ${{ steps.meta.outputs.app_name }}-${{ matrix.os }}-${{ matrix.arch }}
          path: |
            ${{ steps.meta.outputs.app_name }}-${{ matrix.os }}-${{ matrix.arch }}${{ matrix.ext }}
            ${{ steps.meta.outputs.app_name }}-${{ matrix.os }}-${{ matrix.arch }}${{ matrix.ext }}.sha256
          retention-days: 7

  release:
    needs: build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/download-artifact@v4
        with:
          path: artifacts
          merge-multiple: true
      - id: meta
        run: |
          NAME=$(awk '/^module /{n=split($2,a,"/"); print a[n]}' go.mod)
          echo "app_name=$NAME" >> $GITHUB_OUTPUT
      - env:
          GH_TOKEN: ${{ secrets.PAT_BUILD }}
        run: |
          gh release create ${{ github.ref_name }} \
            --repo ${{ github.repository_owner }}/${{ steps.meta.outputs.app_name }}-releases \
            --title "${{ steps.meta.outputs.app_name }} ${{ github.ref_name }}" \
            --generate-notes \
            artifacts/*
```

### GitHub Secrets

| Secret | Scope | Permissions |
|---|---|---|
| `PAT_BUILD` | `<app>-releases` only | `Contents: Read & Write` |
| `PAT_DOWNLOAD` | `<app>-releases` only | `Contents: Read-only` (di-embed ke binary buat self-update) |

**Cara generate fine-grained PAT:**
1. GitHub → Settings → Developer settings → Personal access tokens → Fine-grained tokens
2. Repository access: "Only select repositories" → `<app>-releases`
3. Permissions: cuma `Contents` sesuai tabel di atas
4. Expiration: 90 hari (rotate berkala via release baru)

### Trigger workflow

```bash
git tag v1.2.3
git push origin v1.2.3
# Workflow auto-trigger → build 6 binary → publish ke <app>-releases
```

### Verify release

Setelah workflow selesai, cek di repo `<app>-releases` → Releases:
- 6 binary dgn naming consistent (`<app>-<os>-<arch>(.exe)`)
- 6 file `.sha256` companion
- Release notes auto-generate dari commit history sejak tag sebelumnya

User yg pake versi lama bakal otomatis kena notif via self-updater.

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
│   ├── systray.go               # menu tray + glue goroutine
│   ├── icon.go                  # generator icon 32×32 (PNG/ICO)
│   ├── logs.go                  # redirect zerolog ke <UserCacheDir>/<name>/wick.log
│   ├── lock.go                  # single-instance lock via 127.0.0.1:47829
│   └── helpers.go               # openInEditor, jsonIndent
├── userconfig/
│   └── config.go                # Load/Save Config ke <UserConfigDir>/<name>/config.json
                                  # name = app.BuildAppName (baked saat wick build)
├── mcpconfig/
│   └── install.go               # AllClients/Detected/Find/Install/Uninstall/
                                  # InstallMany/UninstallMany/SelfEntry/WickEntry/
                                  # IsInstalled/Locations
├── updater/
│   └── updater.go               # GitHub release check + download + apply
│                                  # pakai PAT + repo embedded
└── pkg/
    ├── api/server.go            # Run(ctx, port) error — context-aware
    └── worker/server.go         # Run(ctx) error — context-aware
```

Gak ada `cmd/gui/`. Gak ada `frontend/`. Tray cuma Go package yg di-wire jadi subcommand.

## Features

### 1. System tray (satu-satunya UI)

Right-click menu, di-generate saat startup dari state sekarang:

```
Project: <name>                            (disabled, info)
─────────────────────────────────────
Start server  /  Stop server  (running on :8080)   ← satu toggle
Start worker  /  Stop worker  (running)            ← satu toggle
Open logs                                          ← buka wick.log di editor
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
  ☑ Auto-start server on launch
  ☐ Auto-start worker on launch
  ☑ Auto-update
  ─────────────
  Open config file
─────────────────────────────────────
Quit
```

**Server toggle:** spawn goroutine yg jalanin `api.NewServer().Run(ctx, port)`. Cancel context buat stop. Pas crash, goroutine notify "Server crashed: …" + reset ke Stopped.

**Worker toggle:** pola sama persis pakai `worker.NewServer().Run(ctx)`.

**Auto-start saat launch:** dikontrol sama `auto_start_server` / `auto_start_worker` di user config. Toggle dari menu **Preferences ▶ Auto-start … on launch** — efek pas next launch (gak start/stop runtime langsung). Default: server `true`, worker `false`.

**Notifications:** tiap aksi raise toast lewat `beeep` ("MCP installed: <appName> → Claude Desktop", "Server stopped", "Update v1.2.3 ready — restart to activate"). Error jadi "<x> failed: <message>".

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

**Bulk action:** `Install all detected` / `Uninstall all` — toast tampil `N installed, M failed`.

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
    BuildVersion = "dev"
    BuildCommit  = "unknown"
    BuildTime    = "unknown"
    BuildAppName = "app"  // dari wick.yml `name:` field
    GitHubPAT    = ""
    GitHubRepo   = ""
)
```

`wick.yml`'s `build` task pakai `-ldflags "-X github.com/yogasw/wick/app.BuildAppName={{.NAME}}"` — wick CLI inject `NAME` var dari top-level `name:` field di wick.yml. `wick build` populate `GitHubPAT` / `GitHubRepo` dari flag-nya. Code app baca aja — gak ada plaintext secret di source.

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

### Tray icon

Di-generate runtime di `icon.go`: image RGBA 32×32, background ijo `#1d7d4f`, "W" putih digambar pakai stroke Bresenham tebel. PNG buat macOS/Linux, ICO via `go-ico` buat Windows. Gak ada asset icon di repo.

### Build tag headless (optional)

Buat deploy yg gak mau libs tray (Docker container, headless server), tambah tag `//go:build !headless` di `internal/systemtray/systray.go` + stub `Run(...)` di bawah `//go:build headless` yg print "tray not available in headless build" lalu exit. `wick build --headless` pass `-tags headless`.

Gak wajib v1 — `./bin/app server` udah lets user skip tray.

## Urutan implementasi

1. ✅ **Bootstrap** — package `internal/systemtray/`, subcommand `tray` di-wire di `app.Run()`
2. ✅ **MCP install** — `internal/mcpconfig` shared sama wick CLI
3. ✅ **Server / worker toggle** — refactor `Run` nerima context, goroutine in-process di tray
4. ✅ **Logs ke UserCacheDir** — redirect zerolog pas tray startup
5. ✅ **Notifications** — `beeep` buat tiap aksi
6. ✅ **Tray icon** — generate runtime PNG/ICO
7. **Self-updater** — `internal/updater/`, GitHub API + SHA verify + binary swap. Wire ke startup `app.Run`. Tambah menu `Check for updates` + `Restart now`.
8. **Polish** — handle "another instance already running" (port-bind clash dst), crash report di toast, port configurable dari menu tray, retention/rotation `wick.log`.

## Open questions

1. **Nama org GitHub** — confirm path `<owner>/<app>-releases` yg di-bake ke binary
2. **Multiple instance** — kalau user double-launch `./bin/app`, dua tray muncul + dua-duanya try `:8080`. Single-instance lock (file lock di bawah `UserCacheDir`) worth ditambah.
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
