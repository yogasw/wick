# Wick Development Plan
> Version: v0.8.11 | Reviewed: 2026-05-09 | Author: Software Architect Review

---

## Kondisi Aktual (v0.8.11)

Berdasarkan eksplorasi kode aktual, Wick sudah memiliki fondasi yang sangat solid:

| Komponen | Status | Catatan |
|---|---|---|
| MCP Server (stdio + HTTP) | ✅ Solid | 7 meta-tools, SSE streaming, protocol negotiation 2024/2025 |
| Connector System | ✅ Solid | pkg/connector contract, encrypted fields, per-op toggle |
| Admin UI | ✅ Ada | Templ + Tailwind, tag-based access control |
| Job Runner | ✅ Ada | Cron scheduler, runtime config |
| Tool Modules | ✅ Ada | Stateless pattern, embedded static assets |
| wick.yml Task Runner | ✅ Ada | if_missing, bg tasks, arch-aware vars |
| Cross-platform Builder | ✅ Ada | Mac DMG, Windows MSI, Linux DEB |
| System Tray | ✅ Ada | Autostart support |
| SSO / OAuth | ✅ Ada | Multiple provider support |
| Access Token Management | ✅ Ada | MCP auth via Bearer token |
| Skill Sync (AI agent) | ✅ Ada | 5 skills bundled: tool, connector, design, config, encrypted |
| Built-in Connectors | ⚠️ Minim | Hanya 2: crudcrud (demo) + wickmanager (self-manage) |
| Agent Orchestration | ❌ Belum | Design doc sudah ada (commit ae995ab), implementasi belum |
| Observability Dashboard | ❌ Belum | connector_runs table ada, tapi tidak ada UI/metrics |
| HITL Approval Gate | ❌ Belum | Tidak ada mekanisme approval untuk operasi destructive |

---

## Prioritas Pengembangan

---

### PHASE 1 — Connector Ecosystem (Prioritas Tertinggi)
**Target: v0.9.x | Estimasi: 6-8 minggu**

Ini bottleneck terbesar. Wick punya MCP server yang solid tapi hanya 2 connector. Tanpa connector yang berguna, nilai MCP tidak terealisasi.

#### 1.1 GitHub Connector
```
internal/connectors/github/
├── connector.go   # Meta, Configs{BaseURL, Token secret}, Operations
├── service.go     # validation, URL builder
└── repo.go        # http.NewRequestWithContext calls
```
Operations yang perlu dibuat:
- `list_repos` — list repositories (read-only)
- `list_issues` — list issues dengan filter (read-only)
- `create_issue` — buat issue baru (destructive)
- `get_file` — baca file dari repo (read-only)
- `list_prs` — list pull requests (read-only)
- `add_comment` — tambah komentar ke issue/PR (destructive)

#### 1.2 Slack Connector
```
internal/connectors/slack/
├── connector.go   # Configs{BotToken secret, DefaultChannel}
├── service.go
└── repo.go
```
Operations:
- `send_message` — kirim pesan ke channel (destructive)
- `list_channels` — list channels (read-only)
- `get_user_info` — info user by ID (read-only)
- `upload_file` — upload file ke channel (destructive)

#### 1.3 Generic HTTP/REST Connector
Connector yang paling versatile — bisa wrap API apapun tanpa buat connector baru:
```
internal/connectors/httprest/
├── connector.go   # Configs{BaseURL, AuthHeader secret, AuthValue secret}
└── repo.go        # dynamic request builder
```
Operations:
- `get` — HTTP GET ke endpoint + query params
- `post` — HTTP POST dengan JSON body (destructive)
- `put` — HTTP PUT (destructive)
- `delete` — HTTP DELETE (destructive)

#### 1.4 PostgreSQL / Database Connector
```
internal/connectors/postgres/
├── connector.go   # Configs{DSN secret, MaxRows}
├── service.go     # SQL sanitization, row limit enforcement
└── repo.go        # pgx query execution
```
Operations:
- `query` — SELECT query (read-only, enforced LIMIT)
- `explain` — EXPLAIN ANALYZE query (read-only)

> **Security note:** Hanya boleh SELECT. INSERT/UPDATE/DELETE diblok di service layer. MaxRows default 1000.

#### 1.5 Connector SDK Documentation
Buat `docs/connector-sdk.md` yang menjelaskan cara build connector baru, lengkap dengan:
- Template file structure
- `wick:"..."` tag reference
- `OpDestructive` usage
- Testing pattern dengan `t.TempDir()`

---

### PHASE 2 — Agent Orchestration
**Target: v0.10.x | Estimasi: 8-10 minggu**

Design doc sudah ada di `internal/docs/agents-design.md`. Ini tinggal implementasi berdasarkan design yang sudah matang.

#### 2.1 Foundation (Storage + Registry)
Sesuai design Phase 1 yang sudah selesai di branch agents — port ke main:
```
internal/agents/
├── store/          # filesystem state (~/.wick/agents/)
│   ├── preset.go   # Preset CRUD
│   ├── project.go  # Project + git worktree management
│   └── session.go  # Session + JSONL append-only log
├── pool/           # Agent pool, FIFO queue, idle TTL
└── registry/       # In-memory running agent registry
```

#### 2.2 Subprocess + Event Streaming
```
internal/agents/subprocess/
├── claude.go    # claude --output-format stream-json parser
├── codex.go     # codex --json parser (Phase 2.3)
└── events.go    # AgentEvent normalized struct
```

#### 2.3 wick-gate Binary
```
cmd/gate/
└── main.go     # PreToolUse hook — whitelist enforcement
```
- Glob whitelist dari GeneralConfig.AllowedCmds
- Semua keputusan log ke `sessions/<id>/commands.jsonl`
- fail-safe: block on timeout

#### 2.4 Web Dashboard (SSE real-time)
```
internal/tools/agents/
├── handler.go    # SSE stream + form endpoints
├── service.go    # pool query, session management
└── view.templ    # 2-column layout (sidebar + content)
```
Views: Overview | Sessions | Projects | Presets | Queue | Config

#### 2.5 HITL Approval Gate — **Fitur Kunci**
Ini yang membedakan Wick dari semua kompetitor:

```go
// Saat wick-gate menerima operasi destructive:
// 1. Pause agent (block stdout ke subprocess)
// 2. Kirim approval request ke:
//    - Slack: mention authorized user + reaction buttons
//    - Web UI: badge di session detail page
// 3. Timeout 5 menit → auto-reject
// 4. Resume atau terminate berdasarkan keputusan
```

Interface approval:
```
internal/agents/gate/
├── approver.go      # approval request + response handling
├── slack_notifier.go # Slack mention + reaction listener
└── ui_notifier.go   # SSE push ke web dashboard
```

---

### PHASE 3 — Observability & Enterprise Readiness
**Target: v0.11.x | Estimasi: 4-6 minggu**

#### 3.1 Connector Run Dashboard
`connector_runs` table sudah ada. Yang dibutuhkan:
```
internal/tools/analytics/
├── handler.go
└── view.templ    # charts: runs/day, success rate, avg latency, top connectors
```
Metrics yang ditampilkan:
- Total runs per connector per periode
- Success rate & error breakdown
- Avg response time
- Top users by usage
- Cost estimate (token count × rate per provider)

#### 3.2 LLM Cost Tracking
Tambah ke `connector_runs`:
```sql
ALTER TABLE connector_runs ADD COLUMN tokens_in  INTEGER DEFAULT 0;
ALTER TABLE connector_runs ADD COLUMN tokens_out INTEGER DEFAULT 0;
ALTER TABLE connector_runs ADD COLUMN cost_usd   NUMERIC(10,6) DEFAULT 0;
```
Dan tracking di agent subprocess parser untuk capture token usage dari Claude response.

#### 3.3 Prometheus Metrics Endpoint
```
GET /metrics   # prometheus text format
```
Metrics:
- `wick_connector_runs_total{connector, operation, status}`
- `wick_connector_duration_seconds{connector, operation}`
- `wick_agent_sessions_active`
- `wick_mcp_requests_total{method, status}`

#### 3.4 Enhanced Audit Trail
Saat ini `connector_runs` menyimpan basic info. Perlu ditambah:
- `request_hash` — SHA256 dari input params (untuk detect duplicate calls)
- `approval_id` — link ke HITL approval jika ada
- Export endpoint: `GET /manager/audit?format=csv&from=...&to=...`

---

### PHASE 4 — Ecosystem & DX
**Target: v0.12.x | Estimasi: 4 minggu**

#### 4.1 Cross-platform Template Makefile
Saat ini template Makefile Windows-biased (`.exe`, `curl`). Perbaiki dengan arch detection:

```makefile
GOOS   := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)

ifeq ($(GOOS),windows)
  EXE := .exe
  DL  := curl -L -o
else ifeq ($(GOOS),darwin)
  EXE :=
  DL  := curl -L -o
else
  EXE :=
  DL  := wget -O
endif
```

#### 4.2 `wick new connector <name>` Subcommand
Scaffold connector baru dari template:
```bash
wick new connector github
# Creates: internal/connectors/github/{connector,service,repo}.go
# Registers in: internal/connectors/registry.go
```

```bash
wick new tool <name>
# Creates: internal/tools/<name>/{handler,service,repo,config,static}.go + view.templ
```

#### 4.3 `wick new job <name>` Subcommand
```bash
wick new job daily-report
# Creates: internal/jobs/daily-report/{handler,service,config}.go
```

#### 4.4 Integration Test Framework
```
internal/connectors/testutil/
├── server.go    # httptest.Server wrapper untuk mock upstream API
└── fixtures.go  # common response fixtures
```

Pattern untuk setiap connector:
```go
func TestGitHubListRepos(t *testing.T) {
    srv := testutil.MockServer(t, "GET /repos", fixtures.GitHubRepos)
    cfg := github.Configs{BaseURL: srv.URL, Token: "test"}
    // ...
}
```

#### 4.5 `wick doctor` Command
Diagnostic command untuk check environment:
```bash
wick doctor
✓ wick v0.12.0
✓ go 1.25.0
✓ templ installed (bin/templ)
✓ tailwindcss installed (bin/tailwindcss)
✓ wick.yml found
✓ database connection ok
✗ MCP not installed — run: wick mcp install --client claude-code
```

---

## Arsitektur Keputusan

### Mengapa Mulai dari Connector Ecosystem?

Karena **nilai Wick sebagai MCP server** bergantung pada seberapa banyak connector berguna yang tersedia. Saat ini hanya ada `crudcrud` (demo sandbox) dan `wickmanager` (self-management). Tanpa connector real-world, developer yang adopt Wick harus langsung bikin connector dari nol — friction yang tinggi.

### Mengapa HITL Gate adalah Fitur Kunci di Phase 2?

Karena ini adalah **moat** Wick vs kompetitor:
- Hermes: tidak ada
- LangChain/LangGraph: butuh custom implementation
- AutoGPT: tidak ada built-in
- Wick: **built-in, integrated dengan Slack + Web UI**

Setiap enterprise yang akan adopt autonomous agent **membutuhkan ini**. Ini yang akan memenangkan enterprise deal.

### Mengapa Tidak Implement Semua Sekaligus?

Karena tiap phase deliver nilai yang bisa di-demo:
- Phase 1 → demo: "Claude bisa akses GitHub/Slack/DB via Wick MCP"
- Phase 2 → demo: "Claude bisa menjalankan task via Slack, dengan approval gate"
- Phase 3 → demo: "Lihat berapa $ yang dihemat vs manual + siapa yang pakai apa"
- Phase 4 → demo: "Setup project baru dalam 5 menit"

---

## Estimasi Keseluruhan

| Phase | Target Version | Estimasi | Nilai Delivered |
|---|---|---|---|
| 1. Connector Ecosystem | v0.9.x | 6-8 minggu | MCP menjadi useful untuk real use case |
| 2. Agent Orchestration + HITL | v0.10.x | 8-10 minggu | Enterprise-grade autonomous agent |
| 3. Observability | v0.11.x | 4-6 minggu | ROI visibility, compliance-ready |
| 4. DX & Ecosystem | v0.12.x | 4 minggu | Faster adoption, lower friction |

**Total: ~22-28 minggu** untuk full roadmap

---

## Quick Wins (Bisa dikerjakan sekarang, < 1 minggu)

1. **`wick new connector <name>`** — scaffold generator, tidak ada dependency ke phase lain
2. **Cross-platform Makefile** di template — fix Windows bias, pure refactor
3. **`wick doctor`** — pure CLI, tidak butuh perubahan core
4. **Connector Analytics basic** — `connector_runs` table sudah ada, tinggal buat UI-nya
5. **Generic HTTP/REST connector** — paling versatile, effort rendah, value tinggi
