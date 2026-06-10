# Wick Manager — Implementation Plan

Cross-platform desktop GUI app untuk manage wick framework instance lokal. Built di atas wick (pakai `wick build` untuk produce final binary).

## Stack

- **Go** (latest stable)
- **Wails v2** (`github.com/wailsapp/wails/v2`) — native webview shell, embed frontend via `//go:embed`
- **Svelte + TypeScript + Vite** — UI framework, source di `cmd/gui/frontend/src/`
- **Tailwind CSS** — styling, integrated via Vite plugin
- **yarn** — frontend package manager (no npm/pnpm)
- **System tray**: built-in Wails tray API
- **Internal packages**: `autostart` (OS auto-start) & `updater` (self-update) — implement sendiri, referensi dari `emersion/go-autostart` & `creativeprojects/go-selfupdate`
- **Self-update**: built into the binary via `wick build` (PAT + repo passed at build time)
- **SQLite & bcrypt**: gunakan yang sudah dipakai wick

## Reuse from wick (don't reimplement)

Wick framework sudah punya banyak komponen yang dibutuhkan desktop app ini. **Wajib reuse, jangan duplicate**:

- **`internal/login`** — auth service, password hash, force-change flow, session
- **`internal/pkg/postgres`** — DB connection, GORM, migrations
- **`internal/configs`** atau equivalent — untuk simpan key-value config (preferences desktop app simpan di sini juga)
- **`internal/mcp`** — MCP handler (stdio, sse) sudah ada — desktop app cuma generate config untuk client, gak handle MCP runtime
- **`cmd/cli/mcp`** — **Sudah ada semua logic install/uninstall MCP, detect config Claude/Cursor/dll, generate config JSON.** Desktop app GUI **wajib panggil command-command ini**, jangan reimplement. Tab MCP Config di GUI = wrapper visual untuk command-command di `cmd/cli/mcp`.
- **`cmd/cli`** lainnya — `init`, `upgrade`, dll — shell-out ke command ini daripada reimplement

Desktop app berperan sebagai **GUI wrapper** di atas wick services — bukan reimplement logic-nya.

## Project & DB location

Wick menggunakan konsep **project** — direktori yang berisi `wick.db` dan menjadi context untuk CLI commands & MCP server. Ini sesuai pattern wick yang sudah ada (`--project` flag).

**Mengapa project-based:**
- CLI commands yang context-aware perlu tau project mana yang sedang di-operate (mis. saat user `cd` ke folder project lalu jalanin wick command, wick perlu tau project context-nya)
- MCP server di-spawn per-project oleh client (Claude/Cursor) dengan project path tertentu
- User bisa punya multiple wick projects di mesin yang sama (dev, staging, client A, client B, dll)

### Resolution order saat startup

```
1. CLI flag --project <path>? ──Yes──> pakai itu
   ↓ No
2. CWD ada wick.db? ──Yes──> pakai CWD sebagai project
   ↓ No
3. DefaultProject di config valid? ──Yes──> pakai itu
   ↓ No / invalid
4. First-run flow: tampilkan picker "Pilih atau create wick project"
```

### Recent projects

Wick-manager track recent projects untuk UX yang lebih baik:
- Header app menampilkan active project name + dropdown switcher
- Settings → "Default project" dengan list recent projects (klik untuk swap default)
- File menu → "Open recent" (kalau pakai menu bar)

### Pointer config (di OS config dir)

File pointer kecil yang track default project + recent list:

| OS | Path |
|---|---|
| Windows | `%APPDATA%\Wick\config.json` |
| macOS | `~/Library/Application Support/Wick/config.json` |
| Linux | `~/.config/wick/config.json` |

Isinya minimal:

```json
{
  "default_project": "D:\\code\\work\\wick",
  "recent_projects": [
    "D:\\code\\work\\wick",
    "D:\\code\\work\\client-a"
  ]
}
```

User preferences lainnya (toggles, dll) disimpan di **wick.db dari project aktif** lewat existing config repo wick — bukan di file ini. Ini bikin preferences scope-nya per-project (e.g., auto-start admin panel mungkin beda settings di project dev vs prod).

## Build & distribution

Final binary di-produce dengan:

```bash
wick build \
  --release-github-pat=$RELEASE_GITHUB_PAT \
  --release-github-repo=org/wick-manager-releases \
  --output=wick-manager
```

**Asumsi `wick build`:**
- Default: include GUI (frontend embedded). Opt-out via `wick build --cli-only` untuk binary CLI saja.
- Inject PAT + repo ke binary via ldflags (developer tinggal akses sebagai variable di Go code)
- Bundle GUI + CLI dalam satu executable
- Handle cross-compilation (Win/Mac/Linux)
- **Frontend dist**: kalau `cmd/gui/frontend/dist/` belum ada, jalankan `yarn install --frozen-lockfile && yarn build` dulu. Kalau sudah ada (case: tag fetch dari Go modules), skip yarn step.

**Kenapa wick framework user (downstream) tidak butuh yarn**: dist gitignored di branch, tapi CI bake dist ke tag commit sebelum push. `go get wick@vX.Y.Z` ambil tree tag → dist sudah ada → `wick build` skip yarn step. Lihat "CI bake-dist" di section CI/CD.

**Repo strategy (aktor 2)**:
- `<aktor2-app>` — source code aktor 2 (private)
- `<aktor2-app>-releases` — binary releases only (private), 2 PAT scoped ke sini saja
- PAT bocor → attacker hanya dapat binary, bukan source code

## CI/CD (GitHub Actions)

Wick framework menyiapkan workflow ini sebagai **template**, bukan untuk wick repo sendiri. Workflow di-scaffold ke project aktor 2 lewat `wick init` di path `template/.github/workflows/release.yml` — aktor 2 yang ngebuild app mereka multi-OS pakai workflow ini, lalu publish ke release repo mereka. Aktor 3 download dari sana, auto-update via embedded updater.

Trigger saat push tag `v*.*.*`. Build cross-platform pakai matrix strategy, push semua artifact ke release repo aktor 2 (`<aktor2-org>/<aktor2-app>-releases`).

### Build matrix

6 target binary:

| OS | Arch | Output filename |
|---|---|---|
| windows | amd64 | `<app>-windows-amd64.exe` |
| windows | arm64 | `<app>-windows-arm64.exe` |
| darwin | amd64 | `<app>-darwin-amd64` |
| darwin | arm64 | `<app>-darwin-arm64` |
| linux | amd64 | `<app>-linux-amd64` |
| linux | arm64 | `<app>-linux-arm64` |

`<app>` = app name aktor 2 (di-resolve dari `go.mod` module name atau env var di workflow).

### Workflow file (template)

Disimpan di `template/.github/workflows/release.yml`. Saat aktor 2 jalanin `wick init`, file ini di-copy ke `.github/workflows/release.yml` di project mereka.

```yaml
name: Release

on:
  push:
    tags:
      - 'v*.*.*'

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
      - name: Checkout
        uses: actions/checkout@v4

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: 'stable'
          cache: true

      - name: Setup Node + yarn cache
        uses: actions/setup-node@v4
        with:
          node-version: '20'
          cache: 'yarn'
          cache-dependency-path: cmd/gui/frontend/yarn.lock

      # Wails butuh native webview deps di Linux
      - name: Install Linux deps
        if: matrix.os == 'linux'
        run: |
          sudo apt-get update
          sudo apt-get install -y libwebkit2gtk-4.1-dev pkg-config

      - name: Resolve app name from go.mod
        id: meta
        run: |
          NAME=$(awk '/^module /{n=split($2,a,"/"); print a[n]}' go.mod)
          echo "app_name=$NAME" >> $GITHUB_OUTPUT

      - name: Build with wick
        env:
          GOOS: ${{ matrix.os }}
          GOARCH: ${{ matrix.arch }}
          APP_NAME: ${{ steps.meta.outputs.app_name }}
          RELEASE_GITHUB_PAT: ${{ secrets.RELEASE_GITHUB_DOWNLOAD_PAT }}
          OUTPUT: ${{ steps.meta.outputs.app_name }}-${{ matrix.os }}-${{ matrix.arch }}${{ matrix.ext }}
        run: |
          wick build \
            --release-github-pat=$RELEASE_GITHUB_PAT \
            --release-github-repo=${{ github.repository_owner }}/${{ steps.meta.outputs.app_name }}-releases \
            --output=$OUTPUT

      - name: Generate SHA256
        run: |
          sha256sum ${{ steps.meta.outputs.app_name }}-${{ matrix.os }}-${{ matrix.arch }}${{ matrix.ext }} > ${{ steps.meta.outputs.app_name }}-${{ matrix.os }}-${{ matrix.arch }}${{ matrix.ext }}.sha256

      - name: Upload artifact
        uses: actions/upload-artifact@v4
        with:
          name: ${{ steps.meta.outputs.app_name }}-${{ matrix.os }}-${{ matrix.arch }}
          path: |
            ${{ steps.meta.outputs.app_name }}-${{ matrix.os }}-${{ matrix.arch }}${{ matrix.ext }}
            ${{ steps.meta.outputs.app_name }}-${{ matrix.os }}-${{ matrix.arch }}${{ matrix.ext }}.sha256
          retention-days: 7

  release:
    name: Publish release
    needs: build
    runs-on: ubuntu-latest

    steps:
      - name: Download all artifacts
        uses: actions/download-artifact@v4
        with:
          path: artifacts
          merge-multiple: true

      - name: Resolve app name from go.mod
        id: meta
        run: |
          NAME=$(awk '/^module /{n=split($2,a,"/"); print a[n]}' go.mod)
          echo "app_name=$NAME" >> $GITHUB_OUTPUT

      - name: Create release in releases repo
        env:
          GH_TOKEN: ${{ secrets.RELEASE_GITHUB_PUBLISH_PAT }}
          APP_NAME: ${{ steps.meta.outputs.app_name }}
        run: |
          gh release create ${{ github.ref_name }} \
            --repo ${{ github.repository_owner }}/${APP_NAME}-releases \
            --title "${APP_NAME} ${{ github.ref_name }}" \
            --generate-notes \
            artifacts/*
```

### GitHub Secrets needed (di repo aktor 2)

Di repo source aktor 2 → Settings → Secrets:

| Secret | Scope | Permissions |
|---|---|---|
| `RELEASE_GITHUB_PUBLISH_PAT` | `<aktor2-app>-releases` only | `Contents: Read & Write` (untuk create release + upload assets) |
| `RELEASE_GITHUB_DOWNLOAD_PAT` | `<aktor2-app>-releases` only | `Contents: Read-only` (di-embed ke binary untuk self-update) |

**Cara generate fine-grained PAT:**
1. GitHub → Settings → Developer settings → Personal access tokens → Fine-grained tokens
2. Repository access: "Only select repositories" → `<aktor2-app>-releases`
3. Permissions: hanya `Contents` sesuai tabel di atas
4. Expiration: 90 hari (rotate berkala via release baru)

### Cross-compilation notes

Wails butuh native webview SDK per target (WebView2 di Windows, WebKit di macOS, WebKitGTK di Linux). Implikasi:

- **Native build per OS** — pakai matrix dengan `runner` per-OS (Windows host build Windows binary, dst). Yang dilakukan workflow di atas. ✅ Paling reliable.
- **Cross-compile dari Linux** — secara teori bisa, tapi setup-nya kompleks dan rentan break. Tidak direkomendasikan.

**Alternatif: nightly-style cross-compile dari satu host** — kalau wick build sudah handle ini internally, tinggal sesuaikan workflow (ganti matrix runner jadi cuma `ubuntu-latest`, tetap GOOS/GOARCH matrix).

### CI bake-dist (sebelum tag push)

Tujuan: tag commit di-publish dengan `cmd/gui/frontend/dist/` ada di tree, supaya wick framework user (`go get wick@vX.Y.Z`) langsung dapat dist tanpa butuh yarn lokal. `dist/` tetap gitignored di branch HEAD.

Tambahkan job sebelum build matrix di workflow yang sama, atau jadi langkah awal di tiap matrix runner (lebih simple — bake dist sekali, semua matrix consume hasilnya):

```yaml
- name: Build frontend dist
  working-directory: cmd/gui/frontend
  run: |
    yarn install --frozen-lockfile
    yarn build

- name: Bake dist into tag (one runner only, e.g. ubuntu-latest amd64)
  if: matrix.os == 'linux' && matrix.arch == 'amd64'
  env:
    GH_TOKEN: ${{ secrets.ADMIN_TOKEN }}
  run: |
    sed -i '/cmd\/gui\/frontend\/dist/d' .gitignore
    git config user.name "github-actions[bot]"
    git config user.email "github-actions[bot]@users.noreply.github.com"
    git add .gitignore cmd/gui/frontend/dist
    git commit -m "release: ${{ github.ref_name }} (dist baked)"
    git tag -f ${{ github.ref_name }}
    git push origin -f ${{ github.ref_name }}
```

**End state**:
- Branch HEAD: dist gitignored, no dist files
- Tag tree: dist ada (force-pushed at release time)
- `go get wick@vX.Y.Z` → ambil tag tree → dist available untuk go:embed
- Aktor downstream `wick build` skip yarn step otomatis

### Trigger workflow

```bash
# Bump version & tag
git tag v1.2.3
git push origin v1.2.3

# Workflow auto-trigger → build 6 binary → publish ke wick-manager-releases
```

### Verify release

Setelah workflow selesai, cek di `wick-manager-releases` repo → Releases:
- 6 binary dengan naming consistent
- 6 file `.sha256` companion
- Release notes auto-generated dari commit history sejak tag sebelumnya

User yang punya wick-manager versi lama bakal otomatis dapet notif via self-updater.

## Project structure

```
cmd/
└── gui/
    ├── main.go                  # Wails app entry, register Go services as bindings
    ├── embed.go                 # //go:embed all:frontend/dist
    ├── wails.json               # Wails config: frontend:install=yarn, frontend:build=yarn build
    └── frontend/
        ├── package.json
        ├── yarn.lock
        ├── tsconfig.json
        ├── svelte.config.js
        ├── vite.config.ts
        ├── tailwind.config.js
        ├── index.html
        ├── src/
        │   ├── App.svelte             # root, routing antar screen/tab
        │   ├── app.ts                 # mount entry
        │   ├── lib/
        │   │   ├── bridge.ts          # wrap window.go.<service>.<method>
        │   │   └── stores.ts          # Svelte stores (project, server status, config)
        │   ├── screens/
        │   │   ├── Login.svelte
        │   │   └── ProjectPicker.svelte
        │   ├── tabs/
        │   │   ├── Server.svelte
        │   │   ├── MCP.svelte
        │   │   └── Settings.svelte
        │   ├── components/
        │   │   ├── PillToggle.svelte
        │   │   ├── ClientCard.svelte
        │   │   └── HeroCard.svelte
        │   └── styles/
        │       └── app.css            # @tailwind directives
        └── dist/                      # generated by yarn build, gitignored di branch

internal/
├── server/
│   ├── manager.go               # Spawn/kill wick admin panel via os/exec
│   ├── logs.go                  # Stream stdout/stderr to UI buffer
│   └── status.go                # PID, uptime, memory tracking
├── mcp/
│   ├── config.go                # Generate MCP config JSON
│   ├── installer.go             # Read/merge/write client config files
│   └── clients.go               # Per-client paths (Claude/Cursor/etc per OS)
├── updater/
│   └── updater.go               # GitHub release check + apply (uses embedded PAT)
│                                # Implement sendiri, ref: creativeprojects/go-selfupdate
├── autostart/
│   ├── autostart.go             # Common interface (Enable/Disable/IsEnabled)
│   ├── autostart_windows.go     # Registry HKCU\...\Run
│   ├── autostart_darwin.go      # ~/Library/LaunchAgents/*.plist
│   └── autostart_linux.go       # ~/.config/autostart/*.desktop
│                                # Implement sendiri, ref: emersion/go-autostart
└── config/
    └── config.go                # User prefs (tray, auto-update settings, etc.)
                                 # Stored in wick DB — reuse existing wick repo/persistence layer
                                 # NOTE: PAT & GitHub repo injected at build time via ldflags — NOT stored anywhere
```

UI dipisah ke `cmd/gui/frontend/` (Svelte+TS+Tailwind) — Go side cuma binding services.

## Features

### 1. Login screen (modal sebelum main window)

**Reuse existing wick services**: wick sudah punya `internal/login` (handler, service, middleware, repo) lengkap. Desktop app **tidak perlu reimplement** auth logic — tinggal panggil service-nya.

- Validate via `internal/login` service yang ada di wick
- Default credentials: `admin@admin.com` / `admin` (sesuai default seeding wick)
- Login pakai default password → **force change password** screen:
  - Min 8 karakter
  - Tidak boleh `admin`
  - Confirm password match
  - Real-time strength meter (weak/medium/strong)
- Setelah change → reuse password change flow dari `internal/login/service`
- Login state di-cache di config table (DB), tapi tetap divalidasi tiap launch

### 2. Server tab

**Hero card:**
- Pulse indicator (green = running, gray = stopped)
- Label "Admin panel"
- Address `http://localhost:<port>` — clickable, buka browser
- Metadata inline: PID, memory, SSO status, uptime

**Controls:**
- Start / Stop / Restart buttons
- Port input (default 9425)

**Logs:**
- Live tail of wick stdout/stderr di monospaced text area
- Auto-scroll, capped buffer (e.g., last 500 lines)

**Implementation:**
- Spawn pakai `exec.Command("wick", "<start-cmd>", "--port", port)` — confirm exact subcommand
- Capture stdout/stderr via pipes → push lines ke UI via channel
- Stop: SIGTERM (Unix) atau `taskkill /F /T /PID` (Windows)
- Update PID/uptime tiap 1s

### 3. MCP Config tab

**Architecture diagram di atas (informational):**
```
[Client: Claude/Cursor] ──spawns──> [Binary: wick.exe] ──stdio──> [mcp serve subprocess]
```
Penjelasan: MCP **tidak** jalan di admin panel server — di-spawn oleh client tiap conversation.

**Inputs:**
- **Wick project** (directory containing `wick.db`) — pre-filled dari active project di header. Bisa di-override per-install kalau user mau install MCP untuk project lain. Browse button untuk pilih directory.
- **Target client picker** (grid 3 kolom):
  - Claude Desktop
  - Cursor
  - Claude Code (project-local)
  - Gemini CLI
  - Codex CLI
  - Manual
- Tiap client: deteksi instalasi → "Detected" / "Not found" / "Project-local"

**Output:**
- Live JSON preview dengan syntax highlighting
- Path file yang akan di-modify (auto-update per client)
- Buttons: **Install to <Client>** / **Open file** / **Uninstall** / **Copy**
- Mode Manual: hide Install/Open/Uninstall, hanya Copy

**Config format:**
```json
{
  "mcpServers": {
    "wick": {
      "command": "<absolute path to wick binary>",
      "args": ["mcp", "serve", "--mode", "auto", "--project", "<project path>"]
    }
  }
}
```

**Per-client config paths:**

| Client | Windows | macOS | Linux |
|---|---|---|---|
| Claude Desktop | `%APPDATA%\Claude\claude_desktop_config.json` | `~/Library/Application Support/Claude/claude_desktop_config.json` | `~/.config/Claude/claude_desktop_config.json` |
| Cursor | `%APPDATA%\Cursor\User\settings.json` | `~/Library/Application Support/Cursor/User/settings.json` | `~/.config/Cursor/User/settings.json` |
| Claude Code | `.mcp.json` (project root) | same | same |
| Gemini CLI | `%USERPROFILE%\.gemini\settings.json` | `~/.gemini/settings.json` | `~/.gemini/settings.json` |
| Codex CLI | `%USERPROFILE%\.codex\config.toml` (TOML!) | `~/.codex/config.toml` | `~/.codex/config.toml` |

**Penting:** Saat write ke client config, **MERGE** dengan existing `mcpServers` object — jangan overwrite seluruh file. Untuk Codex, pakai TOML library.

**Tip:** Wick sudah punya `wick mcp install --client <id>`. Pertimbangkan shell-out ke command itu daripada reimplement file logic — lebih simpel & sync dengan format wick.

### 4. Settings tab

Semua boolean toggle default `true`. Lihat Config struct di section Self-updater untuk full schema.

**Server section:**
- Toggle: Auto-start admin panel on launch *(default: on)* — pakai internal `autostart` package (referensi dari `emersion/go-autostart`) untuk register/unregister OS auto-start. Saat ON: app jalan otomatis pas OS boot + auto-start admin panel server saat app run. Saat OFF: kebalikannya, full manual.
- Wick binary path (file picker) — auto-detect dari `$PATH` saat first run kalau ada
- **Default project** (directory picker) — project yang dibuka otomatis saat app launch. Auto-detect dari CWD kalau ada `wick.db`, atau user pilih saat first run.

**App section:**
- Toggle: Minimize to tray on close *(default: on)*
- Toggle: Auto-update *(default: on)* — cek + auto-download versi baru dari GitHub. Restart required untuk aktivasi.

**Persistence:**
- **Per-project preferences** (toggle tray, auto-start, auto-update prefs) → simpan di **wick.db dari project aktif** lewat existing config repo wick. Scope per-project (settings bisa beda antara dev project & prod project).
- **Cross-project pointer** (default project, recent projects list) → simpan di file `config.json` di OS config dir (lihat "Project & DB location" section)

### 5. System tray

Right-click menu:
- Status line: "Admin panel on :9425" / "Stopped"
- Show window
- Open admin panel (browser)
- --- divider ---
- Admin panel header (disabled item)
- Start / Stop / Restart
- --- divider ---
- Check for updates
- --- divider ---
- Quit

Closing window → hide to tray (kalau toggle aktif), tidak quit.
Double-click tray icon → show window.

### 6. Self-updater

**Default behavior: auto-check + auto-download. User must restart to activate new version.**
Single toggle `auto_update` di Settings — kalau `false`, semua otomatis tidak jalan, user trigger manual via "Check for updates" link.

**Flow:**

```
App launch
    ↓
[Staged update from previous session?] ──Yes──> apply + restart (before UI shows)
    ↓ No
[auto_update = ON?] ──No──> skip (manual only via UI/tray menu)
    ↓ Yes
Background goroutine: query GitHub API for latest release
    ↓
[Newer version found?] ──No──> done
    ↓ Yes
Download binary to temp folder + verify SHA256
    ↓
Stage binary for next launch
    ↓
Show notif (non-blocking): "Update v1.2.3 downloaded — Restart to activate"
    ↓ User clicks "Restart now"
[Server running?] ──Yes──> Confirm: "This will stop wick admin panel. Continue?"
    ↓ No / confirmed
Apply binary replacement → restart app
```

**Critical UX rules:**
- **Background, never blocking**: check + download di goroutine — UI tetap responsif
- **Quiet failure**: kalau check/download gagal → silent retry next launch, jangan kasih error popup
- **Restart required**: download otomatis, tapi binary baru baru aktif setelah restart. Notif jelas: "downloaded — restart to activate"
- **Interrupt awareness**: kalau wick server lagi running, tampilkan extra warning sebelum restart
- **Auto-apply on next launch**: kalau user pilih "Later" atau tutup app tanpa restart, binary yang udah di-stage akan otomatis di-apply pas launch berikutnya
- **Idempotent**: re-download skip kalau binary udah di-stage untuk versi yang sama
- **Manual trigger**: footer link "Check for updates" + tray menu item — selalu trigger flow yang sama, bypass auto_update toggle

**Implementation:**

Implement sendiri di `internal/updater/`. Referensi struktur dari `creativeprojects/go-selfupdate` source code, tapi sederhanakan sesuai kebutuhan (tidak semua feature library itu kepake).

**Komponen utama:**
- HTTP client untuk GitHub API (`/releases/latest`) dengan auth `Bearer <PAT>` + `Accept: application/octet-stream`
- Semver compare (pakai `golang.org/x/mod/semver` atau implement minimal)
- Download asset berdasarkan `runtime.GOOS` + `runtime.GOARCH`
- SHA256 verification (download `.sha256` checksum file dari release assets)
- Binary replacement:
  - **Linux/macOS**: `os.Rename(temp, current)` — atomic
  - **Windows**: rename current → `.old`, place new file di posisi current, restart, hapus `.old` saat next launch

```go
// internal/updater/updater.go
package updater

type Updater struct {
    cfg     *config.Config
    owner   string
    repo    string
    pat     string
}

func New(cfg *config.Config, pat, repo string) *Updater {
    parts := strings.SplitN(repo, "/", 2)
    return &Updater{
        cfg:   cfg,
        owner: parts[0],
        repo:  parts[1],
        pat:   pat,
    }
}

// Run on app launch
func (u *Updater) CheckOnStartup(ctx context.Context, currentVersion string) {
    // 1. Apply any staged update from previous session first (regardless of toggle)
    if u.cfg.StagedUpdatePath != "" {
        u.applyStaged()
        return // app will restart
    }

    // 2. Skip if auto-update disabled
    if !u.cfg.AutoUpdate { return }

    // 3. Background check + download
    go u.checkAndDownload(ctx, currentVersion)
}

func (u *Updater) checkAndDownload(ctx context.Context, current string) {
    latest, err := u.fetchLatestRelease(ctx)
    if err != nil { return } // silent fail

    if !isNewer(latest.TagName, current) { return }

    // Auto-download to temp folder
    stagedPath, err := u.downloadAsset(ctx, latest)
    if err != nil { return }

    // Verify SHA256
    if err := u.verifyChecksum(ctx, stagedPath, latest); err != nil { return }

    // Stage for next launch
    u.cfg.StagedUpdatePath = stagedPath
    u.cfg.StagedUpdateVersion = latest.TagName
    u.cfg.Save()

    // Notify user — they can restart now or later
    u.notifyReadyToRestart(latest, stagedPath)
}
```

**Config fields needed:**

```go
// Per-project config — disimpan di wick.db project aktif via wick's config repo
type Config struct {
    // Server
    AutoStartAdminPanel bool   `json:"auto_start_admin_panel"` // default: true
    WickBinaryPath      string `json:"wick_binary_path"`       // detected/set on first run

    // App
    MinimizeToTray bool `json:"minimize_to_tray"` // default: true
    AutoUpdate     bool `json:"auto_update"`      // default: true

    // Update state (managed by updater, not user-facing)
    StagedUpdatePath    string `json:"staged_update_path"`    // empty = no pending
    StagedUpdateVersion string `json:"staged_update_version"`
}

// Cross-project pointer config — disimpan di OS config dir sebagai config.json
type PointerConfig struct {
    DefaultProject  string   `json:"default_project"`
    RecentProjects  []string `json:"recent_projects"` // capped at e.g. 10 entries
}
```

**Default values:** semua boolean toggle default `true`. `WickBinaryPath` di-detect dari `$PATH` saat first run.

## Key implementation notes

### Build-time variable injection (set by `wick build`)

```go
// main.go
package main

var (
    Version       = "dev"   // semver from git tag
    GitHubPAT     = ""      // for self-update
    GitHubRepo    = ""      // org/repo format
)
```

`wick build` is responsible for setting these via ldflags. App code just reads the variables — no plaintext secrets in source.

### Cross-platform process management

- **Windows**: `taskkill /F /T /PID <pid>` untuk kill process tree
- **Unix**: `syscall.Kill(-pid, syscall.SIGTERM)` (negative PID = kill process group; perlu `Setpgid: true` di `SysProcAttr`)
- Track state via channel dari goroutine yang watch `cmd.Wait()`

### Self-update binary replacement

Library handles ini, tapi:
- **Linux/macOS**: `os.Rename` atomic, tidak ada masalah
- **Windows**: file lock saat .exe running → library rename current ke `.old`, place new file, restart; on next launch, hapus `.old`

### Wails + Svelte tips

- 3 tab: Svelte component routing di `App.svelte` dengan store `activeTab`
- Hero card + status pill: pakai `<canvas>` atau pure CSS animation di `HeroCard.svelte`
- Live logs: subscribe ke Wails event `server:log` via `EventsOn` di `lib/bridge.ts`, append ke `<pre>` element + scroll to end
- Tray: Wails native `systemtray` API (di Go side, register saat app startup), fire menu actions ke window via events
- HMR dev: `wails dev` jalanin vite hot reload + Go reload bersamaan

## Implementation order

1. **Bootstrap** — `go mod init`, basic Fyne window dengan 3 empty tabs
2. **Config persistence** — implement `config` package yang baca/tulis ke wick DB (foundation untuk fitur lain)
3. **Server tab** — start/stop wick, capture logs, status updates
4. **MCP config tab** — generate JSON, write ke client files (mulai dari Claude Desktop, expand later)
5. **Login screen** — reuse `internal/login` service dari wick, GUI wrapper untuk panggil flow login + force-change
6. **System tray** — minimize-to-tray, menu actions
7. **Self-updater** — implement sendiri di `internal/updater/`, ref dari go-selfupdate. Test dengan manual release.
8. **Polish** — theme, icons, error handling, toast notifications

## Open questions (confirm before coding)

1. **Wick start command** — exact subcommand untuk start admin panel? (lihat `cmd/cli` di wick repo)
2. **GitHub org name** — untuk repo path
3. **DB choice** — wick pakai PostgreSQL via GORM, tapi konteks pakai `wick.db` (SQLite-style filename). Apakah wick support keduanya tergantung config? Desktop app default-nya pakai SQLite (lebih cocok untuk single-user desktop scenario).

## Out of scope (don't build)

- OAuth login (deferred)
- Public release repo (using private)
- Code obfuscation (`garble`) — optional hardening, skip MVP
- Telemetry / crash reporting
- PAT auto-rotation
- Code signing

## Design reference

Lihat `wick-manager-mockup-v4-en.html` untuk visual reference layout & flow. Match behavior, tapi pakai native Fyne widgets — Fyne style acceptable, no need pixel-match HTML.
