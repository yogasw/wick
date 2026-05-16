# Workflow — Desain & State (index)

Status: implementasi sebagian besar sudah jalan; doc ini split dari
monolith lama jadi 1 file per section di [`workflow/`](workflow/).
Update terakhir: 2026-05-16.

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

- [ ] Audit section 🟡 di tabel — verifikasi vs kode atau update
- [ ] Section §22 pertanyaan terbuka — putusin yg masih `[open]`, archive yg `[decided]`
- [ ] [`workflow/mockup.html`](workflow/mockup.html) — kalau perlu, split per-page biar gampang di-iterate (saat ini 181KB monolith)

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
| 04 | [Folder + YAML schema](workflow/04-folder-yaml-schema.md) | 🟢 current | File layout + workflow.yaml shape |
| 05 | [Node catalog](workflow/05-node-catalog.md) | 🟡 partial | Specs basic node OK, periksa field tambahan di `internal/agents/workflow/types.go` |
| 06 | [Graph & engine](workflow/06-graph-engine.md) | 🟢 current | Walker algo, edge resolution, parallel/merge |
| 07 | [Triggers + router](workflow/07-triggers-router.md) | 🟢 current | Router + queue + dedup |
| 08 | [Domain package](workflow/08-domain-package.md) | 🟡 partial | Struct layout match kode, signature drift mungkin ada |

### Surface

| # | File | Status | Catatan |
|---|---|---|---|
| 09 | [MCP surface](workflow/09-mcp-surface.md) | 🟡 partial | Lookup vs `internal/agents/workflow/mcp/` aktual |
| 10 | [UI canvas editor](workflow/10-ui-canvas.md) | 🟡 partial | Drawflow integration sudah jauh lebih lengkap dari doc (lock, marquee, fit-to-view, picker, dll) — lihat `internal/tools/agents/js/workflow/` |
| 11 | [Env & secrets](workflow/11-env-secrets.md) | 🟢 current | Schema resolver + secret leak guard |

### Data & state

| # | File | Status | Catatan |
|---|---|---|---|
| 12 | [Datasets](workflow/12-datasets.md) | 🟡 partial | In-mem ✅; Postgres backend masih deferred |
| 13 | [State persistence](workflow/13-state-persistence.md) | 🟢 current | Sharded index, runs/, events.jsonl |

### Operations

| # | File | Status | Catatan |
|---|---|---|---|
| 14 | [Test framework](workflow/14-test-framework.md) | 🟡 partial | TestRunner ada, CLI `wick workflow test` belum |
| 15 | [Bootstrap + hot-reload](workflow/15-bootstrap-hotreload.md) | 🟡 partial | Entrypoints ada, fsnotify watcher belum mount |
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
