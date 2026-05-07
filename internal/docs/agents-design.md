# Agents — Desain

Status: draft.
Update terakhir: 2026-05-07.

---

## 1. Latar Belakang

Wick sudah menjadi MCP server. Claude CLI dan Codex CLI mendukung MCP server eksternal via config. Dari sini muncul peluang: spawn claude/codex CLI sebagai subprocess, inject MCP config ke wick endpoint, dan agent langsung mendapat akses semua tools/connectors wick secara otomatis.

**Agents** adalah modul orchestration yang mengatur siklus hidup AI CLI agent (claude atau codex), routing session via Slack thread, kontrol akses command, dan dashboard real-time via HTTP stream.

Analoginya mirip Open Claw tapi native Go, didesain sebagai bagian dari wick bukan standalone tool.

---

## 2. Konsep Inti

Agents adalah modul **first-class** di wick — sejajar dengan Tools, Jobs, dan Connectors. Punya menu sendiri di UI, config terpisah per concern, dan manager yang di-expose sebagai Tool.

```
Slack thread message
  → Transport (Slack)
  → Access Control (everyone | users | groups)
  → Session Manager  (lookup/create session by thread ID)
  → Meta-command check (ganti agent X, reset, dll → wick handle)
  → Agent Pool       (slot tersedia? → run, else queue)
  → Subprocess       (claude/codex CLI + workspace)
  → Command Gate     (setiap perintah di-check whitelist)
  → Response stream  → Slack thread
  → Dashboard        ← HTTP stream (SSE) real-time state
```

**Prinsip:**
- 1 Slack thread ID = 1 session (thread ID adalah session key)
- 1 session bisa punya banyak named agents, hanya 1 aktif di satu waktu
- Switch agent via meta-command yang di-intercept wick sebelum masuk subprocess
- Setiap agent punya identity via file `.agents/<name>.md` di workspace
- Agent Pool menghitung slot dari total subprocess aktif lintas semua session
- Command gate: tidak terdaftar → auto-block + log, tidak ada arbitrary shell

---

## 3. Komponen

### 3.1 Agent Pool

Pool mengatur jumlah agent yang berjalan bersamaan.

```
┌──────────────────────────────────────────┐
│              Agent Pool                  │
│                                          │
│  [slot 1: session-A / agent "backend"]   │
│  [slot 2: session-B / agent "default"]   │
│  [queue: session-C, session-A/reviewer]  │
└──────────────────────────────────────────┘
```

- **`max_concurrent`** — batas jumlah subprocess aktif (default: 2, configurable)
- **Queue** — FIFO, session menunggu slot kosong
- **TTL idle** — subprocess di-down setelah **benar-benar idle** melewati threshold (default: 60s). Timer hanya berjalan saat tidak ada I/O activity. Kalau agent sedang proses, timer tidak jalan.
- Session state tetap persist meski subprocess sudah down
- Ketika pesan baru masuk ke session yang idle → revive (masuk pool/queue)

### 3.2 Session Manager

Mapping: `Slack thread ID → Session`

```go
type Session struct {
    ID           string
    ThreadID     string         // Slack thread ts (session key)
    ChannelID    string         // Slack channel
    ProjectID    string         // project yang dipakai session ini
    Agents       []Agent        // semua agent dalam session ini
    ActiveAgent  string         // nama agent yang sedang aktif
    CreatedAt    time.Time
    LastActiveAt time.Time
}

type Agent struct {
    Name      string        // e.g. "backend", "reviewer"
    Backend   Backend       // claude | codex | gemini
    IdentityFile string     // path ke .agents/<name>.md (optional)
    CLISessionID string     // session ID dari CLI, untuk --resume
    Status    AgentStatus   // idle | queued | running
    Process   *os.Process   // nil kalau idle
}
```

**Persona via `personas/`:**

Setiap agent punya persona yang didefinisikan di `personas/<nama>/agent.md`. Persona bisa diedit dari UI atau oleh agent itu sendiri. Session mendapat snapshot persona saat dibuat/switch.

Lihat section 3.4 untuk struktur folder lengkap.

**Switch agent:**

User ketik di Slack thread: `ganti agent reviewer` atau `switch to backend` → wick:
1. Set `Session.ActiveAgent = "reviewer"`
2. Input berikutnya diteruskan ke subprocess agent "reviewer"
3. Agent sebelumnya tetap hidup (tidak di-kill) sampai TTL idle-nya habis
4. Kalau agent tujuan belum ada subprocess-nya → spawn (masuk pool/queue)

- Session persist ke DB wick, survive daemon restart
- Workspace bisa di-share antar session (kalau `workspace` sama → reuse folder)

### 3.3 Command Gate

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
- Semua eksekusi (allowed dan blocked) → log ke DB

```
[2026-05-07T10:23:11Z] session=T123 agent=backend cmd="git clone ..." status=allowed
[2026-05-07T10:23:15Z] session=T123 agent=backend cmd="rm -rf ."   status=blocked
```

### 3.4 Folder Structure

Semua data agents tinggal di `~/.wick/agents/` dengan tiga subdirektori utama:

```
~/.wick/agents/
  personas/                        ← definisi persona, reusable, editable
    default/
      agent.md                     ← persona default (kosong / generic)
    backend/
      agent.md                     ← instruksi spesifik persona "backend"
    reviewer/
      agent.md

  workspaces/                      ← project folders (git repos, dll)
    my-project/                    ← git repo utama (clone sekali)
      CLAUDE.md                    ← project context, dibaca semua agent
      AGENTS.md                    ← setara CLAUDE.md untuk codex
      ...

  sessions/                        ← per Slack thread
    <thread-id>/
      agent.md                     ← snapshot persona aktif saat ini
      workspace/                   ← git worktree dari workspaces/my-project/
        CLAUDE.md                  ← merged: project CLAUDE.md + agent.md
        src/                       ← file project (independent dari session lain)
        ...
```

**Kenapa git worktree:**

Satu project bisa dipakai banyak session tanpa clone ulang. Setiap session punya working tree + branch sendiri, tidak konflik.

```bash
# Clone sekali
git clone <repo> ~/.wick/agents/workspaces/my-project

# Setiap session dapat worktree sendiri
git -C ~/.wick/agents/workspaces/my-project \
    worktree add ~/.wick/agents/sessions/<thread-id>/workspace \
    -b session/<thread-id>
```

| | Clone per session | Symlink shared | Git worktree |
|---|---|---|---|
| Disk usage | ❌ boros | ✅ ringan | ✅ ringan (objects shared) |
| Konflik antar session | ✅ tidak ada | ❌ bisa konflik | ✅ tidak ada (branch terpisah) |
| Independensi edit | ✅ | ❌ | ✅ |

**`agent.md` di session:**

Snapshot persona saat session dibuat atau saat switch agent. Wick merge `workspaces/<project>/CLAUDE.md` + `sessions/<id>/agent.md` → tulis ke `sessions/<id>/workspace/CLAUDE.md` sebelum spawn subprocess.

Kenapa snapshot dan bukan reference langsung:
- Edit persona dari UI tidak break session yang sedang jalan
- Reset session → update snapshot ke persona terbaru

### 3.5 Project Manager

Project adalah entitas yang menghubungkan workspace + persona default + sessions.

```go
type Project struct {
    ID             string
    Name           string    // e.g. "frontend", "api-service"
    RepoURL        string    // git clone URL (kosong kalau bukan git repo)
    WorkspaceDir   string    // ~/.wick/agents/workspaces/<name>/
    DefaultPersona string    // nama persona default untuk session baru
    CreatedAt      time.Time
}
```

**Lifecycle project:**

```
Buat project "frontend" (dari UI atau Slack)
  → kalau ada repo_url: git clone ke workspaces/frontend/
  → kalau tidak: mkdir workspaces/frontend/
  → buat personas/default/agent.md kalau belum ada

Session T123 pakai project "frontend"
  → git worktree add sessions/T123/workspace -b session/T123
  → copy snapshot: personas/<default>/agent.md → sessions/T123/agent.md
  → merge CLAUDE.md → sessions/T123/workspace/CLAUDE.md
  → spawn subprocess, cwd = sessions/T123/workspace/

Session T456 juga pakai "frontend"
  → git worktree add sessions/T456/workspace -b session/T456
  → independent dari T123, tidak konflik
```

**Manage dari Web UI** (halaman Projects):

| Aksi | Detail |
|---|---|
| Create project | Nama + repo URL (optional) + default persona |
| Edit project | Ganti default persona, rename |
| Delete project | Hapus workspace + semua session worktree |
| Lihat sessions | List session yang attach ke project ini |
| Git pull | Update workspaces/<name>/ dari remote |
| List branches/worktrees | Lihat worktree per session |

**Manage dari Slack (meta-command):**

| Command | Aksi |
|---|---|
| `buat project frontend` | Create project tanpa repo |
| `buat project frontend https://github.com/...` | Create + auto-clone |
| `pakai project frontend` | Attach session ke project ini |
| `ganti project api` | Switch session ke project lain (buat worktree baru) |
| `list project` | Reply list semua project |

### 3.6 Streaming States & Raw Output

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

Ini murni kode di `events.go` dan `slack.go`. `agent.md` tidak perlu diubah — streaming adalah runtime behavior, bukan konfigurasi persona.

**Step 1 — Internal event type (abstraksi lintas CLI):**

Setiap CLI punya format berbeda. Wick normalkan ke satu struct internal:

```go
type AgentEvent struct {
    Type      EventType // Thinking, TextDelta, ToolUse, ToolResult, Done, Error, SessionID
    Text      string    // isi text (untuk TextDelta)
    ToolName  string    // nama tool (untuk ToolUse, e.g. "Bash")
    ToolInput string    // input tool sebelum dieksekusi (untuk gate check)
    SessionID string    // di-extract dari event, disimpan ke DB
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

**Step 4 — Slack handler (`slack.go`) — apa yang dikirim per state:**

| State | Aksi ke Slack |
|---|---|
| `thinking` | Post "⏳ thinking..." (atau Slack typing indicator kalau tersedia) |
| `running_tool: Bash "git clone..."` | Update message → "⚙️ running: `git clone ...`" |
| `running_tool: BLOCKED` | Update message → "🚫 blocked: `rm -rf .`" |
| `responding` | Buffer semua `text_delta`, kirim setelah `Done` (Slack tidak support true streaming) |
| `Done` | Edit/replace "thinking..." message dengan final response |
| `error` | Post "❌ Agent error: ..." |

Kenapa buffer dulu baru kirim: Slack rate limit ketat untuk edit message. Lebih aman buffer seluruh response lalu kirim sekali. Kalau response > 4000 char → split jadi beberapa reply dalam thread.

**Step 5 — SSE dashboard (`stream.go`):**

Dashboard mendapat semua events real-time karena tidak ada rate limit:
- Setiap event (termasuk `text_delta` per karakter) langsung di-broadcast via SSE
- Dashboard tampilkan dua mode:

| Mode | Tampilan |
|---|---|
| **Formatted** | Conversation biasa: user turn → assistant response |
| **Raw stream** | Semua events JSON mentah: thinking, tool calls, deltas — seperti panel Output di VSCode |

**Step 6 — Simpan ke DB (`store.go`):**

| Event | Yang disimpan |
|---|---|
| `TextDelta` (accumulated) | `agent_conversations`: role=assistant, content=full text |
| `ToolUse` | `agent_command_logs`: cmd, status (allowed/blocked) |
| `SessionID` | `session_agents.cli_session_id` untuk resume |
| `Raw` (semua events) | `agent_raw_events` (optional, untuk raw view replay) |

**Ringkasan: apa yang perlu di-coding:**

| File | Yang dibuat |
|---|---|
| `events.go` | Interface `EventParser` + implementasi ClaudeParser, CodexParser, GeminiParser |
| `agent.go` | State machine (idle/thinking/running_tool/responding) + idle timer reset |
| `slack.go` | State → Slack message handler, buffer text, chunking >4000 char |
| `stream.go` | SSE broadcaster, fan-out ke semua dashboard clients |
| `store.go` | Simpan conversation turn, command log, raw events |
| `cmd/wick-gate/main.go` | Binary kecil untuk hook, terima stdin JSON, check whitelist, exit 0/2 |

Tidak ada perubahan di `agent.md` atau file persona — semua di kode Go.

### 3.7 Transport

Transport adalah abstraction layer antara Agents dan sumber pesan. Implementasi pertama: Slack. Connector lain bisa ditambah nanti tanpa ubah core.

```go
type Transport interface {
    Listen(ctx context.Context, handler MessageHandler) error
    Send(ctx context.Context, msg OutgoingMessage) error
}

type IncomingMessage struct {
    SessionKey string    // unique key untuk routing session (e.g. thread ID)
    UserID     string    // pengirim
    GroupIDs   []string  // user groups si pengirim (untuk access check)
    Text       string
    Raw        any       // payload original dari transport
}
```

**Implementasi Slack:**

- Terima message event dari Slack (Socket Mode atau HTTP Event API)
- Route berdasarkan `thread_ts` → session key
- Thread baru → create session baru
- Stream response dari subprocess stdout → kirim ke Slack thread sebagai reply
- Resolve user groups (`GroupIDs`) dari Slack API untuk setiap pesan masuk — dipakai untuk access control

**Access Control:**

Setiap pesan masuk di-check sebelum diproses. Config dikelola dari UI (section Slack → Access).

| Mode | Perilaku |
|---|---|
| `everyone` | Semua member workspace boleh trigger agent |
| `users` | Hanya user ID yang ada di allowed users list |
| `groups` | Hanya member dari Slack User Group yang ada di allowed groups list |

Pesan dari user yang tidak diizinkan → diabaikan (tidak di-reply, tidak di-log ke conversation).

### 3.8 Web Dashboard

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

## 4. Alur Lengkap

### 4.1 Pesan Masuk dari Slack

```
1. Slack event masuk (message di thread atau channel)
2. Slack Bot extract (channel_id, thread_ts, text, user)
3. Session Manager lookup session by thread_ts
   - Tidak ada → buat AgentSession baru dengan config default
4. Cek status session:
   - running → teruskan input ke subprocess stdin langsung
   - idle    → masuk Agent Pool
     - Ada slot → spawn subprocess, status = running
     - Pool penuh → status = queued, pesan masuk message buffer
5. Kalau status = queued dan pesan baru masuk → append ke message buffer (tidak diproses satu-satu)
6. Saat slot tersedia → spawn subprocess → kirim semua buffered messages sekaligus sebagai satu input
7. Subprocess di-spawn dengan:
   - binary: claude / codex / gemini
   - flag resume: `--resume <session_id>` kalau ada di DB, else tanpa flag
   - output format: `--output-format stream-json` (claude) untuk capture session ID
   - working dir: temp agent dir (`~/.wick/agents/sessions/<thread-id>/<agent-name>/`)
   - hook config: wick-gate hook di-inject via settings sebelum spawn
8. Input ditulis ke subprocess stdin
9. Subprocess stdout di-baca per chunk → stream ke Slack + SSE dashboard
10. Command Gate intercept setiap shell exec sebelum dieksekusi
11. Idle timer reset setiap ada aktivitas
12. Kalau idle > TTL → subprocess.Kill(), status = idle, slot dibebaskan
    → queue di-proses (session berikutnya di-spawn)
```

### 4.1.1 Message Buffer saat Queue

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

### 4.2 Session Revival & Context Management

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
| **Reset conversation** | ⚠️ Harus clear `~/.claude/projects/` | ✅ Hapus DB records, clean |
| **Tampil di dashboard** | ⚠️ Harus parse format internal claude | ✅ Wick punya copy, langsung tampil |
| **Multi-agent context sharing** | ⚠️ Shared via workspace, bisa conflict | ✅ Wick bisa kontrol apa yang di-share |
| **Implementasi** | ✅ Simple, spawn aja | ❌ Complex: inject format, truncation, edge cases |
| **Prediktabilitas** | ⚠️ Bergantung behavior internal claude | ✅ Wick yang kontrol penuh |
| **Storage** | claude manage sendiri | DB wick bertambah per conversation |

**Keputusan: Hybrid**

- Claude native memory tetap jalan (tidak dioverride) → context revival, tools, skills, MCP semua otomatis
- Wick **juga** simpan copy conversation ke DB → **hanya untuk dashboard display**, tidak di-inject balik ke subprocess
- Reset conversation: hapus DB records + clear `~/.claude/projects/<hash>/` untuk session tersebut

### Session Management per CLI — Riset

Semua tiga CLI support resume via session ID. Wick **wajib simpan mapping `thread_id → CLI session_id`** di DB — bukan cuma rely on working directory — karena workspace bisa di-share antar thread dan tanpa ID yang tepat, resume bisa ambil sesi yang salah.

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

| | **A: Wick simpan session ID di DB** | **B: Rely on CLI latest session** |
|---|---|---|
| **Cara kerja** | Wick simpan `thread_id → CLI session_id`, revival pakai `--resume <id>` | Spawn di dir yang sama, CLI ambil sesi terakhir otomatis |
| **Workspace sharing** | ✅ Aman — setiap thread punya session ID sendiri | ❌ Berbahaya — dua thread di workspace sama bisa cross-resume sesi yang salah |
| **Akurasi resume** | ✅ Selalu resume sesi yang benar | ⚠️ Hanya benar kalau 1 thread per workspace |
| **Implementasi** | Moderate — perlu ambil + simpan session ID saat subprocess start | Simple — spawn aja |
| **Claude CLI** | ✅ `--resume <id>` | ✅ `--continue` |
| **Codex CLI** | ✅ `codex resume <UUID>` | ⚠️ Ambil sesi terakhir, bisa salah |
| **Gemini CLI** | ✅ `--resume <UUID>` | ⚠️ Ambil sesi terakhir, bisa salah |
| **Fallback kalau ID tidak ada** | Inject last N turns via stdin (Claude: stream-json, lainnya: plain text) | Mulai sesi baru |

**Keputusan: Approach A** — wick simpan `thread_id → CLI session_id` di DB.

#### Flow Resume dengan Session ID

```
Pertama kali session dibuat:
  spawn subprocess (tanpa --resume)
  subprocess start → cetak session ID di awal output
  wick tangkap session ID → simpan ke DB: thread_id=T123, session_id=abc-456

Subprocess di-kill (TTL 2 menit):
  session_id=abc-456 tetap ada di DB

Pesan baru masuk ke thread T123:
  wick lookup DB → session_id=abc-456
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
Wick parse stream JSON, ambil `session_id` dari event pertama, simpan ke DB.

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
       │                               └──→ simpan ke Wick DB (untuk dashboard)
       │
       └──→ Claude tulis ke ~/.claude/projects/<hash>/ (otomatis, internal)
```

Wick tidak inject apapun ke subprocess waktu revival. Claude yang handle sendiri via file-nya.

### Simulasi

```
T+00:00  User: "clone repo dan setup project"
         wick → tulis ke stdin subprocess
         subprocess stdout → wick tangkap → Slack reply + simpan ke DB

T+00:45  User: "tambah error handling di auth.go"
         subprocess stdout → wick tangkap → Slack reply + simpan ke DB

T+02:46  Tidak ada aktivitas 2 menit → wick kill subprocess
         Memory bebas, DB tetap ada

T+02:51  User: "tadi auth.go sudah kita ubah apa aja?"
         wick spawn subprocess baru, working dir sama
         Claude baca ~/.claude/projects/<hash>/ → jawab natural
         wick tangkap response → Slack reply + append ke DB
```

### Apa yang Tersimpan di Mana

| Waktu | Wick DB | `~/.claude/projects/<hash>/` |
|---|---|---|
| T+00:00 | turn 1: user + assistant | turn 1 |
| T+00:45 | turn 1–2 | turn 1–2 |
| T+02:46 (killed) | turn 1–2, read-only di dashboard | turn 1–2, siap di-resume |
| T+02:51 (revived) | turn 1–3 (append) | turn 1–3 |
| Reset conversation | hapus rows di DB | hapus folder `<hash>/` |

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

Wick DB (conversation turns)
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

## 5. Struktur Modul

```
internal/agents/               ← core engine, tidak ada UI
  agent.go                     // Agent struct + lifecycle (spawn, kill, idle timer)
  pool.go                      // Agent Pool: slot management + queue
  gate.go                      // Command Gate: whitelist check
  session.go                   // Session Manager: CRUD + persistence
  project.go                   // Project: CRUD, git clone, worktree management
  persona.go                   // Persona: CRUD, snapshot, merge ke CLAUDE.md
  metacmd.go                   // Meta-command parser (ganti agent, pakai project, dll)
  events.go                    // Parse raw CLI events (stream-json) → internal state
  transport.go                 // Transport interface
  slack.go                     // Slack transport: receive, typing indicator, chunked reply
  stream.go                    // HTTP SSE broadcaster (dashboard real-time)
  store.go                     // Conversation turns + command log + raw events ke DB
  config.go                    // Config structs + bootstrap seed

cmd/wick-gate/                 ← binary kecil dipanggil CLI hooks (PreToolUse, BeforeTool, dll)
  main.go                      // Terima tool context via stdin, check whitelist, exit 0/2

internal/tools/agents/         ← wick Tool: manager UI
  tool.go                      // Tool registration + route handler
  // halaman: overview, sessions, projects, personas, queue, raw stream view
```

**Pembagian tanggung jawab:**

| | `internal/agents/` | `internal/tools/agents/` |
|---|---|---|
| Spawn/kill subprocess | ✅ | — |
| Pool + queue | ✅ | — |
| Slack bot | ✅ | — |
| SSE stream events | ✅ | — |
| Simpan conversation ke DB | ✅ | — |
| Halaman UI (sessions, queue) | — | ✅ |
| Config pages (General, Slack, Workspace) | — | ✅ |
| HTTP routes `/agents/...` | — | ✅ |

---

## 6. Integrasi MCP

### 6.1 Default: Pakai Config Claude/Codex yang Sudah Ada

Secara default agent di-spawn **tanpa inject config tambahan** — claude/codex CLI otomatis membaca config native mereka (`~/.claude/`, `~/.codex/`, dll). Semua MCP, skills, dan memory yang sudah dipasang user langsung tersedia tanpa konfigurasi tambahan.

```bash
# Claude CLI — pakai config bawaan, tidak ada flag tambahan
claude

# Codex CLI
codex
```

Ini intentional: kalau user sudah pasang banyak MCP di claude-nya, agent langsung dapat semua itu gratis.

### 6.2 Wick MCP & Custom MCP

Ikut config native CLI masing-masing. Kalau user mau agent bisa akses wick MCP atau MCP tambahan lainnya, daftarkan langsung di config CLI-nya:

- **Claude**: tambah di `~/.claude/claude_desktop_config.json` atau via `claude mcp add`
- **Codex**: tambah di `~/.codex/config.json`
- **Gemini**: tambah di `~/.gemini/settings.json`

Wick tidak generate atau inject config MCP — agent spawn as-is, semua MCP yang sudah terdaftar di CLI langsung tersedia.

---

## 7. Konfigurasi

Config dipecah menjadi tiga struct terpisah — masing-masing punya section sendiri di UI. Semua pakai `wick:"..."` tag, masuk ke `configs` table di DB, muncul otomatis di admin UI. Tidak ada YAML config. Default di-seed via bootstrap waktu modul pertama kali diinisialisasi (bukan hardcode di tag, karena kvlist tidak support `default=`).

### 7.1 General

```go
type GeneralConfig struct {
    Enabled        bool   `wick:"desc=Enable the Agents feature."`
    MaxConcurrent  int    `wick:"desc=Max concurrent agent subprocesses. Default: 2."`
    IdleTimeoutSec int    `wick:"desc=Seconds of inactivity before subprocess is killed. Default: 120."`
    DefaultBackend string `wick:"desc=Default CLI backend.;dropdown=claude|codex|gemini"`
    AllowedCmds    string `wick:"kvlist;desc=Allowed shell command patterns. Unlisted commands are auto-blocked."`
}
```

Bootstrap seed untuk `AllowedCmds`:
```json
[{"value":"git *"},{"value":"ls *"},{"value":"cat *"},{"value":"mkdir *"}]
```

### 7.2 Slack

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

### 7.3 Workspace

```go
type WorkspaceConfig struct {
    BaseDir string `wick:"desc=Base directory for all agent workspaces. Default: ~/.wick/agents/workspaces/."`
}
```

---

## 8. UI & Tool Manager

Agents punya menu sendiri di nav wick. Terdiri dari dua bagian:

### 8.1 Config Pages

Tiga halaman config terpisah, masing-masing punya section sendiri:
- **General** — enable/disable, pool size, idle TTL, backend, allowed commands
- **Slack** — credentials, connection mode, access control
- **Workspace** — base directory

### 8.2 Manager Tool

Halaman manager di-expose sebagai wick Tool — user bisa lihat dan kelola agent dari UI web:

| Halaman | Isi |
|---|---|
| **Overview** | Pool status, queue length, total sessions aktif |
| **Projects** | List project: nama, repo, worktrees aktif, disk usage. Create/edit/delete/git pull |
| **Personas** | List persona: nama, preview agent.md. Create/edit/delete. Bisa diedit langsung dari UI |
| **Sessions** | List semua session: thread ID, project, active agent, status, last active |
| **Session detail** | Conversation history + command log real-time via SSE |
| **Queue** | Urutan antrian, agent mana yang nunggu slot |

Real-time updates via HTTP stream (SSE) — `GET /agents/stream` → `text/event-stream`.

---

## 9. Meta-Commands

Pesan dari Slack yang di-intercept wick sebelum masuk ke subprocess. Case-insensitive, support bahasa Indonesia dan Inggris.

| Command | Contoh | Aksi |
|---|---|---|
| **Agent** | | |
| Ganti agent | `ganti agent backend` / `switch to reviewer` | Set active agent, spawn kalau belum ada |
| Ganti agent + reset | `ganti agent backend reset` | Switch + clear conversation history |
| List agents | `list agents` / `agent apa aja` | Reply list agent + persona dalam session ini |
| Stop agent | `stop agent backend` | Kill subprocess agent, status idle |
| **Project** | | |
| Buat project | `buat project frontend` | Create project baru tanpa repo |
| Buat project + clone | `buat project frontend https://github.com/...` | Create + git clone |
| Pakai project | `pakai project frontend` | Attach session ke project, buat worktree |
| Ganti project | `ganti project api` | Switch session ke project lain |
| List project | `list project` | Reply list semua project |
| **Session** | | |
| Reset | `reset` / `mulai ulang` | Clear conversation + kill subprocess, snapshot persona diperbarui |
| Status | `status` / `agent status` | Reply status pool, slot tersedia, queue position |

Kalau tidak match meta-command → forward ke active agent subprocess.

---

## 10. DB Schema

Tabel yang dibutuhkan modul agents di DB wick:

```sql
-- Project (workspace + default persona)
CREATE TABLE agent_projects (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    repo_url        TEXT,
    workspace_dir   TEXT NOT NULL,
    default_persona TEXT NOT NULL DEFAULT 'default',
    created_at      DATETIME NOT NULL
);

-- Sesi per Slack thread
CREATE TABLE agent_sessions (
    id           TEXT PRIMARY KEY,   -- thread_ts dari Slack
    channel_id   TEXT NOT NULL,
    project_id   TEXT REFERENCES agent_projects(id),
    active_agent TEXT NOT NULL DEFAULT 'default',
    status       TEXT NOT NULL,      -- idle | queued | running
    created_at   DATETIME NOT NULL,
    last_active  DATETIME NOT NULL
);

-- Agent per sesi (bisa banyak per sesi)
CREATE TABLE session_agents (
    id             TEXT PRIMARY KEY,
    session_id     TEXT NOT NULL REFERENCES agent_sessions(id),
    name           TEXT NOT NULL,    -- e.g. "backend", "reviewer"
    backend        TEXT NOT NULL,    -- claude | codex | gemini
    cli_session_id TEXT,             -- ID dari CLI untuk --resume
    status         TEXT NOT NULL,    -- idle | queued | running
    created_at     DATETIME NOT NULL,
    last_active    DATETIME NOT NULL
);

-- History percakapan (untuk dashboard)
CREATE TABLE agent_conversations (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES agent_sessions(id),
    agent_name TEXT NOT NULL,
    role       TEXT NOT NULL,        -- user | assistant
    content    TEXT NOT NULL,
    created_at DATETIME NOT NULL
);

-- Log command gate
CREATE TABLE agent_command_logs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    command    TEXT NOT NULL,
    status     TEXT NOT NULL,        -- allowed | blocked
    created_at DATETIME NOT NULL
);
```

---

## 11. Error Handling

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
- Setelah itu di-kill
- Message buffer yang belum terkirim di-persist ke DB

---

## 12. Retention & Cleanup

| Data | Retention | Cleanup |
|---|---|---|
| Conversation history | 30 hari (configurable) | Job harian hapus rows lama |
| Command log | 7 hari | Job harian |
| Session records | Selamanya (soft delete) | Manual dari UI |
| Workspace folders | Manual | User hapus dari UI atau filesystem |
| Temp agent dirs (`sessions/<id>/`) | Ikut session | Hapus saat session di-delete |
| CLI session files (`~/.claude/projects/`) | Dikelola CLI sendiri | Wick tidak touch |

---

## 14. Storage

| Data | Storage |
|---|---|
| General / Slack / Workspace config | `configs` table di DB wick (via wick tag system) |
| Session state (status, workspace, active agent) | DB bawaan wick |
| Conversation history | DB bawaan wick |
| Command log | DB bawaan wick |
| Auth & permission | Ikut sistem auth wick |
| Workspace folders | Filesystem `~/.wick/agents/workspaces/` |
| Session temp dirs | Filesystem `~/.wick/agents/sessions/<thread-id>/` |


untuk pembuatan tools bisa refrnce ke @docs/guide/tool-module.md ya, sama nanti menu nya di sebelah kiri tools nya, jadi bisa banyak danngak penuh soalnya list ke bawa kan dan content nya ke sebelah kanan