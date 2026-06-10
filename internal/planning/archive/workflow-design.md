# Workflow — Desain & State (index)

Status: implementasi sebagian besar sudah jalan; doc ini split dari
monolith lama jadi 1 file per section di [`workflow/`](workflow/).
Update terakhir: 2026-06-02.

> **Pas implement:**
> - Baca section yg relevan di [`workflow/`](workflow/) — bukan semua sekaligus
> - [`workflow/mockup.html`](workflow/mockup.html) masih satu file utuh — visual reference (UI layout, canvas, run timeline, anatomy diagrams)
>
> **Catatan staleness:** monolith lama 4982 baris ditulis sebelum
> implementasi mostly done. Beberapa section udah ngak match kode
> sekarang (ditandai 🟡 di tabel). Kalau ragu, check `internal/agents/workflow/`
> sumber kebenaran.

---

## TODO (doc-level)

- [ ] Sweep section 🟡 — banyak masih ngomong "workflow.yaml" / file-based; update ke DB+JSON paradigm (lihat `## Migrasi 2026-06: JSON + DB primary` di bawah)
- [ ] Section §22 pertanyaan terbuka — putusin yg masih `[open]`, archive yg `[decided]`
- [ ] [`workflow/mockup.html`](workflow/mockup.html) — kalau perlu, split per-page biar gampang di-iterate (saat ini 181KB monolith)

---

## Migrasi 2026-06: JSON + DB primary

Workflow storage pindah dari file-based YAML ke **DB-primary JSON**.
Ringkasan perubahan, biar tau garis besarnya tanpa baca tiap section:

### Format
- Codec **YAML → JSON**. `parse.Parse` / `parse.Marshal` JSON-only.
  `gopkg.in/yaml.v3` ngak dipakai lagi di workflow path.
- File output rename: `workflow.yaml` → `workflow.json`, `env.yaml` → `env.json`.

### Storage (3 tabel)
- `workflows` — `id`, `name`, `enabled`, `version`, `body_published`, `body_draft`, `has_draft`, timestamps. Body kolom = JSON text.
- `workflow_versions` — history append-only. `kind = draft | published`, `body`, `content_hash`, `created_at`. Powers compare/restore.
- `workflow_test_cases` — `(workflow_id, name)` PK, `body` JSON. Test fixtures DB-native, **bukan file** `__tests__/*.json` lagi.

### Apa yang tetap file
- `runs/<id>/state.json` + `events.jsonl` + run artefacts — volume tinggi, append cheap.
- `env.json` — sensitive, OS-perm protected.

### MCP surface changes
- Hapus: `workflow_read_file`, `workflow_write_file`, `workflow_list_files`, `workflow_delete_file`. Workflow body dari sini ngak addressable sebagai file lagi.
- Tambah:
  - `workflow_lock` — toggle `_canvas.locked` (freeze edit, run tetap jalan)
  - `workflow_guard` — standalone guard report (beda dari `workflow_validate`)
  - `workflow_versions` + `workflow_version_detail` — history
  - `workflow_restore_version` — restore snapshot → draft
  - `workflow_diff_versions` — body diff buat compare
  - `workflow_exec_node` — n8n-style execute single node
- Test ops sekarang full name-addressable:
  - `workflow_list_test_cases` returns nama saja
  - `workflow_save_test_case`, `workflow_delete_test_case` pakai nama langsung

### Feature dropped
- `prompt_file: nodes/x.md` di agent + classify. Runtime ngak pernah load file-nya — prompt inline only.
- Auto-import file → DB di boot. Legacy workflow folder di disk diabaikan.

### SPA endpoints baru
- `GET /api/workflows/versions/{id}/diff?from=&to=` — version body diff
- History tab di editor: checkbox 2 versi → modal side-by-side

### Service interface
```go
type Service interface {
    // Workflow CRUD
    List() ([]string, error)
    Load(id string) (Workflow, error)
    Create(id string, w Workflow) error
    Update(id string, w Workflow) error
    Delete(id string) error
    Toggle(id string, enabled bool) error
    FindByName(name, exceptID string) (string, error)

    // Draft lifecycle
    LoadDraft(id string) (Workflow, error)
    HasDraft(id string) bool
    SaveDraft(id string, w Workflow) error
    Publish(id string) (Workflow, error)
    DiscardDraft(id string) error

    // Test fixtures (DB-native, name-addressable)
    ListTests(id string) ([]string, error)
    GetTest(id, name string) ([]byte, error)
    SaveTest(id, name string, body []byte) error
    DeleteTest(id, name string) error

    // Runtime (still file)
    LoadState/SaveState, LoadEnvValues/SaveEnvValues, BaseDir
}
```

Dua impl: `FileService` (legacy, dipakai tests) + `DBService` (production via
`Manager.WithDB(db)`).

---

## Section index

### Pondasi

| # | File | Status | Catatan |
|---|---|---|---|
| 00 | [Roadmap](workflow/00-roadmap.md) | 🟢 current | Phase 1-19 ✅ done; deferred items masih relevan |
| 01 | [Latar belakang](workflow/01-background.md) | 🟢 current | Konteks; routine → workflow |
| 02 | [Prinsip](workflow/02-principles.md) | 🟢 current | Edge-first, single-source-of-truth YAML |
| 03 | [Use cases](workflow/03-use-cases.md) | 🟢 current | UC1-UC4 canonical examples |

### Schema & engine

| # | File | Status | Catatan |
|---|---|---|---|
| 04 | [Folder + YAML schema](workflow/04-folder-yaml-schema.md) | 🔴 stale | Pre-migrasi 2026-06. Sekarang: body JSON di DB, bukan workflow.yaml. `runs/` + `env.json` masih file. |
| 05 | [Node catalog](workflow/05-node-catalog.md) | 🟡 partial | Specs basic node OK; agent/classify `prompt_file` field DROPPED — prompt inline only |
| 06 | [Graph & engine](workflow/06-graph-engine.md) | 🟢 current | Walker algo, edge resolution, parallel/merge |
| 07 | [Triggers + router](workflow/07-triggers-router.md) | 🟢 current | Router + queue + dedup |
| 08 | [Domain package](workflow/08-domain-package.md) | 🟡 partial | Struct layout match kode; `Node.PromptFile` dihapus, `WorkflowVersion.YAML` → `Body` |

### Surface

| # | File | Status | Catatan |
|---|---|---|---|
| 09 | [MCP surface](workflow/09-mcp-surface.md) | 🔴 stale | Dropped: `workflow_{read,write,list,delete}_file`. Added: `workflow_lock`, `workflow_guard`, `workflow_versions`, `workflow_version_detail`, `workflow_restore_version`, `workflow_diff_versions`, `workflow_exec_node` |
| 10 | [UI canvas editor](workflow/10-ui-canvas.md) | 🔴 stale | v1 templ+Drawflow editor sudah DROPPED — Svelte SPA di `fe/agents/workflow/` jadi satu-satunya editor. Reference: `internal/tools/agents/spa_*.go` |
| 11 | [Env & secrets](workflow/11-env-secrets.md) | 🟡 partial | Schema resolver + secret leak guard OK; env file rename `env.yaml` → `env.json` (JSON content) |

### Data & state

| # | File | Status | Catatan |
|---|---|---|---|
| 12 | [Data Tables](workflow/12-data-tables.md) | 🟡 partial | In-mem ✅; Postgres backend masih deferred |
| 13 | [State persistence](workflow/13-state-persistence.md) | 🟡 partial | Workflow body sekarang DB (`workflows` table). Run state (`runs/<id>/state.json`, `events.jsonl`) + governance state masih file — design rationale di section §Migrasi 2026-06 |

### Operations

| # | File | Status | Catatan |
|---|---|---|---|
| 14 | [Test framework](workflow/14-test-framework.md) | 🟡 partial | Test cases sekarang di tabel `workflow_test_cases` (bukan `__tests__/*.json`); name-addressable, MCP ops `workflow_save_test_case` / `workflow_list_test_cases` / `workflow_delete_test_case`. TestRunner ada, CLI `wick workflow test` belum |
| 15 | [Bootstrap + hot-reload](workflow/15-bootstrap-hotreload.md) | 🟡 partial | `Manager.WithDB(db)` swap FileService → DBService at boot. Entrypoints ada, fsnotify watcher tidak relevan lagi (workflow di DB) |
| 16 | [Implicit reply-to-source](workflow/16-implicit-reply.md) | 🟢 current | Synthetic reply node injection |
| 17 | [AI guard](workflow/17-ai-guard.md) | 🟢 current | 5 rule packs + ContentHash + override |
| 18 | [Manager & governance](workflow/18-manager-governance.md) | 🟢 current | Whitelist, audit, version pin |
| 19 | [Failure & timeout](workflow/19-failure-timeout.md) | 🟢 current | on_failure policy, timeout cascade |
| 20 | [Security](workflow/20-security.md) | 🟢 current | Secret handling, gate, scope |
| 21 | [Replay](workflow/21-replay.md) | 🟢 current | Re-run dari history dgn prefill |
| 22 | [Open questions](workflow/22-open-questions.md) | 🟡 audit | Beberapa udah `[decided]`, archive |

### Add-ons (di luar split monolith)

| File | Status | Catatan |
|---|---|---|
| [Pool integration](workflow/pool.md) | 🟢 implemented | Agent node → `agentpool.Pool` (queue, FIFO, session reuse, sidebar) + `session_init` node |
| [Plugin arch](workflow/plugin-arch.md) | 🟢 implemented | Per-node folder pattern (registry, embed, templ partial, JS module) — drop folder = add node |
| [Skill: node-builder](workflow/skill-node-builder.md) | 🟡 template | Claude Code skill spec untuk AI bantu bikin node/workflow baru (template; skill belum dibuat) |
| [Mockup HTML](workflow/mockup.html) | 🟢 reference | Visual reference — canvas + run timeline + inspector + anatomy |
| [Audit TODO](workflow/audit-todo.md) | 🟡 working | Gap audit doc-vs-code (2026-05-15) |
| [Editor roadmap](workflow/editor-roadmap.md) | 🟡 working | Executions panel ✅ shipped; P0 items done; P1 Copy-to-editor + canvas read-only masih pending |
| [Svelte FE migration](workflow/svelte-migration.md) | 🟡 in-progress | Replace templ+plain-JS with Svelte 5 SPA at `fe/agents/`, 1:1 component split, JSON protocol, BaseNode + per-type nodes |
