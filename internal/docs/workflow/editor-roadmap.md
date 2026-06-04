# Workflow editor roadmap — run observability + UI improvements

Tracks the next batch of editor improvements requested after the
draft/publish + canvas refactor landed. Focused on **runtime
observability** (see per-node input/output during/after a run) and
**editor UX polish** (modal inspector, run-without-save flow).

## Design north star

Borrow heavily from **n8n**'s debug experience:
- Per-node input/output panel with Schema/Table/JSON tabs
- Execution history sidebar with auto-refresh + filters
- Pulsing borders + edge labels during runs
- "Execute step" for single-node iteration
- "Copy to editor" for snapshot restore
- Read-only canvas in Executions view with status-colored nodes

n8n's weakness we're fixing:
- Their logs live in DB only — if you nuke the workspace, run history
  is gone with it
- Wick mirrors every run event to **structured logs** (file +
  optionally Loki) so the artifact survives DB / file deletion. The
  Loki mirror is a passive sink today (P0); the "import from logs"
  flow that rebuilds run history from those entries is a separate
  follow-up feature — tracked here so it's visible, but scoped out
  of the first observability cut.

## TODO (urut prioritas)

### P0 — Run Now pakai draft (auto-save flush + run)

Run Now jalanin **draft** persist di disk (workflow.draft.yaml). Auto-save sudah debounce 800ms, jadi sebelum Run Now fire, pending save di-flush dulu supaya consistent dengan yang di kanvas. Ngga perlu in-memory transient run buat full graph — itu hanya nambah duplicate path.

- [ ] **Client flush-then-run**: tombol Run Now di JS: kalau ada pending autosave timer, cancel + fire save segera → tunggu response sukses → baru POST /run. Garansi disk == kanvas saat run kick off.
- [ ] **Server**: tetap pakai `LoadDraft(id)` (existing), tetap gate validate. Tidak butuh endpoint baru.
- [ ] **UX**: status indicator `⟳ Saving + running…` saat flush+run combo jalan.

### P0 — Execute step (run single node dari kanvas)

Beda dari Run Now. User klik node → tombol "Execute step" → server jalanin **node itu doang** dengan input dari either:
- output node parent (kalau parent udah ada run history terakhir), ATAU
- mock data yang user isi manual

Ini in-memory transient run karena scope-nya per-node + sering banget user iterate node body tanpa save full workflow.

- [ ] **Endpoint `POST /workflows/edit/<id>/exec-node`**: body `{node: <drawflow node JSON>, input: <map>}`. Server resolve executor by node type → eksekusi sekali → return `{output, latency_ms, error}`. Ngga write ke disk.
- [ ] **Input source pilihan**:
  - **From parent**: ambil output node parent dari run history terakhir (StateStore.Load id, last run, `Outputs[parentID]`)
  - **Mock data**: user paste JSON di input panel
  - **Empty**: jalanin dengan input `{}`
- [ ] **UI**: di inspector saat node selected, tombol "Execute step" + dropdown source. Output muncul di right pane (Schema/Table/JSON tabs mirip n8n screenshot).
- [ ] **Output preview**: 3 view mode toggle:
  - **Schema**: tree view dengan type icons (string, number, object, array)
  - **Table**: tabular kalau output array of objects
  - **JSON**: raw dengan syntax highlight
- [ ] **"No output data" empty state**: tombol `Execute step` + `or set mock data` link → modal buat paste JSON manual.
- [ ] **Set mock data**: tab "Settings" di parameters area buat saved mock per node (persist di `<id>/mocks.yaml`).

### P0 — Run progress indicator (no page reload)

Saat Run Now / Execute Step jalan, user harus lihat progress live di kanvas — pulsing border di node yang lagi running, edge labels "1 item" muncul saat data flowing, status badge update tiap node selesai. Ngga ada page reload — semuanya di-handle JS lewat SSE stream.

Saat ini Run Now → redirect ke `/edit/<id>` (server-side reload). Wajib ganti jadi fire-and-stream:

- [ ] **Endpoint return run ID**: `POST /run` (atau `/run-once`) return `{run_id}` segera setelah enqueue, status 202. Ngga redirect.
- [ ] **SSE stream**: pakai `/stream` endpoint existing → JS subscribe ke `wf:<id>:<runID>` channel, terima events: `node_started`, `node_completed`, `node_failed`, `node_skipped`, `edge_traversed`, `workflow_completed`.
- [ ] **Per-node visual state** (event-driven):
  - `node_started` → pulsing red border (1.5s loop) + ⟳ icon di pojok
  - `node_completed` → green border + ✓ icon, latency badge "Xms" di bawah node
  - `node_failed` → red border + ✕ icon
  - `node_skipped` → grey border + ○ icon
- [ ] **Edge state**:
  - `edge_traversed` → green stroke 2s flash + "1 item" label muncul
  - Else stays grey idle
- [ ] **Toolbar status**: `⟳ Running…` selama run jalan, jadi `✓ Completed in Xms` atau `✕ Failed at <node>` saat selesai. Bottom panel "Logs" auto-tail (lihat next section).
- [ ] **Cancel button**: muncul saat running, POST `/runs/<id>/cancel` → engine context cancel.
- [ ] **No reload**: semua state update via JS, URL ngga berubah, user bisa edit canvas saat run jalan (edits ngga affect current run karena run-from-snapshot).

### P0 — Per-node input/output panel ala n8n

Setiap node di canvas habis run nampilin status badge + bisa click → buka "Logs" view (input + output JSON, latency, error). Mirip n8n inspector di bottom panel.

- [ ] **Node badge per status**: success (✓ green), failed (✕ red), skipped (○ grey), running (⟳ amber). Pakai SVG marker di pojok kanan atas node.
- [ ] **Edge label**: "1 item" / "N items" / "skipped" — informasi flow dari output ke input. Render via path label.
- [ ] **Bottom panel "Logs" tab**: 
  - Daftar node sesuai urutan eksekusi (timestamp ascending)
  - Click node → expand: Input JSON + Output JSON + latency_ms + error
  - Status filter (All / Failed / Skipped)
- [ ] **Click node on canvas → jump ke Logs entry**: highlight + scroll bottom panel.
- [ ] **Live tail**: saat run masih `running`, append events real-time via SSE (sudah ada `/stream` endpoint di handler).

### P0 — Run logs di-mirror ke Loki

Selain file-based state, push run events ke Loki supaya:
- Backup kalau file dihapus
- Searchable via Grafana
- Survive disk wipes

- [ ] **Loki client config**: tambah `agents.workflow_loki_url` + `agents.workflow_loki_labels` di general config. Empty = disabled (default).
- [ ] **Event pusher**: hook ke engine's `events.jsonl` writer — paralel write ke Loki via push endpoint. Async batched (e.g., 5s flush atau 100 entries).
- [ ] **Label scheme**: `{wick_workflow=<id>, wick_run=<runID>, wick_node=<nodeID>, wick_status=<success|failed|...>}`.
- [ ] **Payload schema**: 1 line per event, JSON: `{ts, id, run_id, node_id, type, latency_ms, input_size, output_size, error}`. Input/output JSON body tidak dipush penuh (terlalu besar) — push hash + first N bytes; full payload tetap di disk.
- [ ] **Restore**: docs note "kalau file `runs/<id>/` hilang, queries Loki `{wick_workflow=...,wick_run=...}` untuk audit trail (tidak bisa replay run, hanya recap)".

### P1 — Inspector → modal / drawer

Right-side inspector makan 320px. Canvas jadi sempit. Ganti ke:
- **Modal/drawer overlay** yang muncul saat node selected, hilang saat deselect
- Default: drawer dari kanan slide-in (Figma style), bisa di-close
- Atau: floating popup persis di sebelah node yang di-click

- [ ] **Default state**: hide inspector → canvas full width
- [ ] **Click node** → slide-in drawer (300px) dari kanan, ESC / klik canvas kosong → close
- [ ] **Drag node** → drawer follow OR auto-close
- [ ] **Toggle button** di toolbar untuk pin inspector (lock open kalau power-user mau)
- [ ] **Mobile/narrow viewport**: jadi full-screen modal

### P1 — Save status indicator pakai versi-aware

Sekarang status global "Saved Xs ago". Tambahkan:
- Indicator `vN draft` saat punya draft unpublished
- Indicator `vN published` saat sama dengan published
- Last-modified-by (kalau ada user tracking)

- [ ] **Version counter di server-side**: bump `Workflow.Version` tiap publish (auto-increment).
- [ ] **Render `vN draft` / `vN published` di status pill** sebelah `✓ Saved Xs ago`.

### P2 — Replay / Re-run from a specific node

n8n style "run from this node onwards" — pakai output cache dari run sebelumnya untuk node-node sebelumnya, jalanin ulang dari node yang dipilih.

- [ ] **State store**: tetap simpan output per-node di `runs/<id>/state.json` (sudah ada).
- [ ] **Right-click node menu**: "Replay from here" → load state + override entry node, jalanin engine partial.
- [ ] **Engine support**: tambahkan `Engine.RunFrom(ctx, w, evt, fromNodeID, prevOutputs)` yang skip nodes sebelum `fromNodeID` + inject `prevOutputs` ke RunContext.

### P2 — Schema-driven Input form per op

Connector args sekarang plain text inputs. Tingkatin:
- [ ] **Pakai `entity.Config` widget type** dari connector spec (dropdown, number, checkbox, picker) — bukan cuma text
- [ ] **Validate type per field** sebelum kirim ke engine
- [ ] **Auto-complete `{{.Node.X.field}}` references** di textarea — popup saat ketik `{{` 

### P3 — Polish

- [ ] **Search nodes** di bottom panel logs (Cmd+F)
- [ ] **Export run log** ke clipboard / file (JSON + Markdown)
- [ ] **Compare two runs**: diff input/output antar 2 run IDs
- [ ] **Run summary at top**: total latency, node count, success rate

### P1 — Floating action rail (right side)

Mirror n8n's right-side icon column. Floating vertical bar with quick
toggles, di-layer di atas canvas (ngga ngorbankan canvas width).

Icons (atas ke bawah):
- `+` Add node (alternative ke palette drag)
- 🔍 Search nodes — open node-search modal (lihat sub-bullet)
- 📄 Notes / docs sidebar — markdown notes per workflow
- ▭ Toggle layout: minimap / fit-to-screen / split-view

- [ ] **Position**: `position: absolute; right: 12px; top: 50%; transform: translateY(-50%);` di canvas wrapper
- [ ] **Style**: dark mode panel, rounded, vertical stack 4 icons @ 40×40px
- [ ] **Add note panel** (📄 icon): drawer dari kanan, markdown editor, simpan ke `notes.md` di workflow folder
  - Notes ke-render di list view sebagai description
  - Markdown preview toggle
- [ ] **Node search (🔍)**: open modal/popup di canvas — daftar semua node di workflow + filter by name/type. Click → pan canvas ke node + select.

### P1 — Node search di bottom panel ("What happens next?")

n8n style: ketika klik output port tanpa drop, popup search nodes muncul untuk pilih next node ke spawn + auto-connect. Existing palette di kiri bukan satu-satunya cara add node.

- [ ] **Trigger**: click di output port (atau drag short distance) + lepas di canvas kosong → spawn search popup di posisi cursor
- [ ] **Group by category**: AI / Action / Data transformation / Flow / Core / Human review / Triggers (sama struktur dengan palette)
- [ ] **Filter input**: search by node type name or description
- [ ] **Click hasil**: spawn node di lokasi popup + auto-connect dari source port
- [ ] **Keyboard**: arrow keys + Enter (no mouse needed)

### P1 — Executions panel ala n8n ✅ **shipped 2026-05-16**

Tab "Executions" di toolbar (sebelah Editor). List semua run dengan auto-refresh, filter, dan detail view per-run.

- [x] **Top-level tabs**: `Editor | Executions` di header (next to breadcrumb) — lazy-loaded on first click
- [x] **Executions sidebar**: 
  - List runs descending by timestamp
  - Each row: timestamp, status icon (✓/✗/⟳/○), duration, short run ID
  - Auto-refresh checkbox (5s interval) + manual refresh button
- [x] **Right pane**: 
  - Header: status, full run ID, duration, link ke run detail page
  - Node status list (completed ✓ / failed ✗ / skipped ○) dengan output count hint
  - Click node row → output JSON panel collapsible (hidden by default)
  - Events timeline (semua events.jsonl entries, color-coded per event type)
- [x] **Storage**: re-use existing `runs/<id>/` folder + `StateStore.ListRuns` + sharded index

**Deferred dari spec asli:**
- Canvas read-only view per-run (node coloring on Drawflow) — komplex, deferred ke P2
- Rating (👍/👎) + tag per run — deferred ke P2
- Filter by status/time range — deferred ke P2

**Files:** `executions.templ`, `workflows.go` (`executionsPanel`, `executionDetail`), `handler.go`, `editor.templ`, `editor_toolbar.templ`, `editor.js`, `editor.css`

### P1 — Copy to editor (restore run snapshot)

Tombol "Copy to editor" di Executions pane: ambil graph version yang dipakai run itu + state per-node, push ke editor draft sebagai snapshot baru. Workflow buat debug regresi setelah edit yang nge-break sesuatu.

Use case:
1. User edit workflow, publish, jalan beberapa run
2. User edit lagi → run baru fail
3. Buka Executions panel → pilih run lama yang Succeeded
4. Click "Copy to editor" → editor draft di-replace dengan graph version run itu
5. Diff visible vs current published; user tau apa yang berubah → fix

- [ ] **Snapshot store**: tiap run, simpan `workflow.snapshot.yaml` di dalam `runs/<id>/` — copy persis graph yang jalan. Tidak share file dengan `workflow.yaml` karena published version bisa berubah after the run.
- [ ] **Endpoint baru `POST /workflows/edit/<id>/runs/<runID>/copy-to-editor`**: load snapshot → write ke `workflow.draft.yaml` (overwrite draft, dengan confirm dialog kalau draft punya unsaved changes)
- [ ] **Diff highlight**: setelah copy, badge node yang BEDA dari published muncul (e.g., `modified` badge). User tau persis node mana yang berubah antara run snapshot vs current published.
- [ ] **Node-level state include**: copy snapshot juga restore `state.json.Outputs[nodeID]` — kalau user re-run, output node yang ngga di-rerun bisa di-mock dari snapshot (lihat P2 Replay-from-node).
- [ ] **Confirmation flow**: kalau draft existing → modal "Replace draft with snapshot from <timestamp>? Current draft will be lost." → confirm/cancel.

### P2 — Polish lanjutan

- [ ] **Diagonal stripe background** di canvas read-only mode (mark "not editable") — `linear-gradient(45deg, rgba(255,255,255,0.04) 25%, transparent 25%)` repeating
- [ ] **Run rating (👍/👎)** per execution → simpan ke state.json sebagai metadata, query-able buat statistik
- [ ] **Tag system**: per workflow + per run, dropdown filter. Tags disimpan di `state.json.tags`

### P2 — Split editor.js jadi modular files

`editor.js` sekarang ~1100 LOC dengan 8+ concerns dicampur:
canvas init, palette drag, inspector form, autosave, rAF reconciler,
align guides, validation badges, dropdowns, history button.

Saat fitur run progress (P0 above) + per-node logs landing, file
ini bakal lewat 2000 LOC. Susah maintain. Pecah jadi modules
ES dengan `<script type="module">` (Drawflow bisa di-load global
seperti sekarang, atau wrap module-style):

- [ ] **`editor/init.js`** — bootstrap Drawflow, graph import, expose `editor` ke modules
- [ ] **`editor/palette.js`** — palette drag-drop + node spawn helpers (`addNodeOfType`, `nodeMeta`)
- [ ] **`editor/inspector.js`** — node inspector form (showInspectorFor, updateNodeData, args renderer, refs panel)
- [ ] **`editor/save.js`** — autosave + validation badge paint + status indicator
- [ ] **`editor/canvas.js`** — rAF reconciler + alignment guides + arrow markers + createCurvature override
- [ ] **`editor/run.js`** — Run Now / Execute step + SSE listener + per-node progress state (new)
- [ ] **`editor/layout.js`** — auto-format button + topological layout
- [ ] **`editor/toolbar.js`** — dropdown menus + reverse-connect
- [ ] **`editor/main.js`** — entry point that wires modules together

Tidak buru-buru — keep monolithic dulu sampai run progress feature landed (1 more growth spurt OK), baru split. Kalau dipecah pre-emptive, churn-nya ngga sebanding karena structure belum settle.

**Kontrak split**:
- Tiap module export 1 init function yang accept `{editor, baseURL, id, ...}`
- Cross-module communication lewat events di `editor` (sudah pakai Drawflow events) atau via shared mutable state object `state = {selectedID, mouseIsDown, ...}` di-pass eksplisit
- Tidak ada global var; window.__wfDebug stays for live debugging

## Future / separate features (deferred)

### Import run history from logs

Mirror of run events ke Loki (P0 di atas) bikin payload survive di
luar wick — kalau `runs/<id>/` folder hilang atau wick di-redeploy
fresh, log entries masih ada. Feature buat **rebuild** run history
view dari log entries = task tersendiri, BUKAN bagian dari first
observability cut.

Scope import flow (jadwal nanti):
- [ ] Loki client buat query: `{wick_workflow=<id>}` + time range
- [ ] Reconstruct `runs/<runID>/state.json` + `events.jsonl` dari log entries
- [ ] UI: tombol "Import from logs" di Executions panel header → modal dengan time range picker
- [ ] Conflict handling: kalau `runs/<runID>/` sudah ada lokal, skip vs overwrite
- [ ] Partial reconstruction OK: kalau cuma sebagian event yang ke-push (karena Loki batching atau truncated payload), tag run sebagai "imported (partial)"

Why deferred: bikin observability flow yang **forward-looking** dulu
(log entries ke Loki + view in-editor). Backward import flow butuh
extra serialization contract antara wick events ↔ Loki rows yang
belum settled. Lebih bagus stabilize forward-write dulu, baru bikin
reader.

## Out of scope (ditunda dulu)

- ~~Workflow versioning lebih dari sekedar draft/published~~ — sudah cukup buat sekarang
- ~~Multi-user collaboration~~ — single-user dulu
- ~~Time-travel debugger (step-by-step)~~ — replay-from-node cukup

## Open questions

- [ ] Format input/output preview di Logs panel: table (key-value) atau raw JSON dengan syntax highlight? n8n pakai keduanya (toggle Schema/Table/JSON di tab).
- [ ] Loki push: kirim full payload atau hash+truncated? Trade-off antara debug-ability vs storage cost.
- [ ] Inspector modal vs floating popup — yang mana lebih disukai sambil drag?

## File map (target)

- `internal/agents/workflow/engine/` — extend untuk `RunOnce(w, evt)` tanpa disk write
- `internal/agents/workflow/state/` — extend dengan Loki adapter
- `internal/tools/agents/workflows.go` — endpoint `/run-once`
- `internal/tools/agents/js/workflow/editor.js` — node status overlays, edge labels, drawer inspector, logs panel wire-up
- `internal/tools/agents/view/workflow/editor_inspector.templ` — convert ke drawer
- `internal/tools/agents/view/workflow/editor_bottom.templ` — Logs tab content

## Design references

- n8n editor (screenshot user) — pattern utama
- Drawflow doesn't ship status badges; pasang manual via DOM overlay
- Loki HTTP push API: `POST /loki/api/v1/push` dengan body `{streams: [{stream, values}]}`
