# Audit Workflow — gap vs design

Tanggal audit: 2026-05-15. Bandingin `internal/tools/agents/workflows.go` + `internal/agents/workflow/*` sama `../workflow-design.md` (index) + `*.md` (split sections) + `mockup.html`. User flag 4 area utama plus struktur canvas + draft/publish. Doc ini track gap + TODO konkret.

## Status implementasi (2026-05-15)

| Area | Status | Catatan |
|---|---|---|
| Channel single-source | ✅ done | `internal/agents/channels/Channel` extended dgn `WorkflowTriggerProvider/WorkflowActionProvider/WorkflowSessionOriginator`. Slack moves to `channels/slack/workflow.go`. Workflow `channel.Registry` jadi adapter. |
| Cron registration | ✅ done | `trigger.CronScheduler` in-process, sync via Bootstrap + HotReload. |
| Webhook mount | ✅ done | `wftrigger.NewWebhookHandler` mounted at `/hooks/` in server.go. |
| Classify/agent via pool | ⏸ deferred | Pool.Send fire-and-forget; workflow node butuh sync response. Butuh `Pool.SendSync` API baru. |
| Draft/Publish lifecycle | ✅ done | `workflow.draft.yaml` + `workflow.yaml`. Save→draft, Publish→promote, Discard→revert. Router cuma register published. |
| Background save | ✅ done | JSON endpoint + 800ms debounce + `wf-save-status` indicator (Saving/Saved/Failed). |
| Pickers | ✅ done | `GET /workflows/api/registry` returns channels/connectors/providers. Inspector pakai `<select>`, cascade Op per Channel/Module. |
| Canvas auto-format | ✅ done | Kahn topological → layered L→R, tombol `⇒` di canvas controls. |
| Guard enable/disable | ✅ done | Default `ModeOff`. Admin toggle via `agents.workflow_guard_mode` (off/warn/block). |

## TODO (urut prioritas)

### P0 — runtime salah (workflow `cron` ga jalan)

- [ ] **Cron scheduler**: bikin `internal/jobs/workflow/registry.go` sesuai design §17 (baris 4197–4227). Pas boot + `HotReload`, scan `workflow.Triggers`, tiap `type: cron` panggil `jobs.Register(job.Module{Key: "workflow:<id>:cron-<idx>", DefaultCron: tr.Schedule, Run: func(ctx) { router.RunNow(ctx, id, evt) }})`. Tambah `jobs.UnregisterPrefix("workflow:<id>:")` di `internal/jobs/registry.go` buat delete.
- [ ] **Webhook mount**: wire `trigger.NewWebhookHandler(router)` ke HTTP mux di path `/hooks/` di `internal/pkg/api/server.go`. Handler udah ada di [webhook.go:20](../agents/workflow/trigger/webhook.go#L20) tapi belum pernah di-mount — webhook trigger di YAML mati semua.
- [ ] **schedule_at trigger**: scheduler belum ada; perlu one-shot timer yang register ke jobs (atau pkg khusus). File baru `internal/agents/workflow/trigger/schedule_at.go` + integrasi `jobs.Register`.

### P0 — channel single source: `internal/agents/channels/Channel`

Aturannya: **semua yang bisa di-trigger workflow harus dideklarasi di `internal/agents/channels/`**. Pkg `internal/agents/workflow/channel/` dilebur — workflow konsumen, bukan source-of-truth. Channel yang ga deklarasi `TriggerSpecs/Actions` = ga muncul di workflow editor (jadi opt-in per channel).

Tambahan: **ga semua channel bisa jadi new session**. Misal channel webhook/rest mungkin bisa terima trigger tapi ga punya konsep sesi multi-turn — capability ini harus eksplisit di Channel interface.

- [ ] **Extend `internal/agents/channels/Channel`** (file `channels/channel.go:59`) dengan method opsional via setter/probe interfaces:
  - `TriggerSpecs() []TriggerSpec` — event apa yang bisa di-trigger (channel non-trigger return nil)
  - `Actions() []ActionSpec` — outbound op yang bisa dipanggil dari workflow node
  - `SupportsSession() bool` — true kalau channel bisa jadi origin sesi pool baru (Slack/Telegram ya; REST one-shot ga)
  - `Send(ctx, op, args) (any, error)` — invoke outbound op
- [ ] **Hapus `internal/agents/workflow/channel/`**: registry workflow lebur ke `agentchannels.Registry`. Node executor channel ([nodes/channel.go:14](../agents/workflow/nodes/channel.go#L14)) pakai langsung `*agentchannels.Registry`. Buang `channel/slack.go` workflow — spec Slack pindah ke `internal/agents/channels/slack/specs.go` deket channel-nya.
- [ ] **MCP `workflow_channels`**: tarik dari `agentchannels.Registry`, filter cuma channel yang `len(TriggerSpecs())>0 || len(Actions())>0`. Channel UI/API ga muncul.
- [ ] **Filter trigger node UI**: dropdown channel di trigger node cuma list channel yang `len(TriggerSpecs())>0`. Buat node action, list channel yang `len(Actions())>0`.
- [ ] **Validator**: parse.Validate reject trigger channel ke channel yang `SupportsSession()=false` kalau trigger butuh sesi multi-turn (mis. trigger butuh balasan ke thread sumber).
- [ ] **Implementasi awal**: Slack jadi referensi (TriggerSpecs + Actions + SupportsSession=true). Telegram & REST nyusul.

### P0 — classify/agent harus masuk queue `internal/agents/pool`, bukan `exec` mentah

Sekarang: [setup/providers.go:54](../agents/workflow/setup/providers.go#L54) spawn one-shot `exec.CommandContext(<bin>, --print, prompt)` di goroutine workflow. Ini bypass pool — pool udah batasin jumlah CLI spawn (`max_concurrent`), track sesi, handle crash recovery. Workflow harus *enqueue ke pool* sama kayak channel inbound, bukan spawn sendiri.

Workflow node = client biasa dari sisi pool. Pool yang nentu: spawn baru atau reuse session yang udah ada, kapan terminate, dll.

- [ ] **Adapter via pool**: bikin `internal/agents/workflow/setup/pool_provider.go` yang delegate ke `internal/agents/pool.SendFunc` (yang udah dipakai channel — `channels/channel.go:26`). Workflow tinggal panggil `pool.Send(ctx, sessionID, agentName, "workflow", role, text)`. Pool yang ngurus queue + spawn limit.
- [ ] **SessionID = key ke pool sesi**: 
  - `SessionNew` → generate UUID per call, `pool.SendFunc` bikin sesi baru.
  - `SessionRoot` → `workflow:<id>:run:<runID>:root`, pool reuse selama run hidup.
  - `SessionPersistent` → `workflow:<id>:persistent`, pool reuse lintas run (TTL/idle handled pool).
- [ ] **Tracking di UI**: sesi workflow bakal muncul di list sessions agents karena tracked di pool — user bisa lihat interaksi (transcript, status, crash) sama kayak sesi channel.
- [ ] **Skills via provider, jangan stub**: [providers.go:110](../agents/workflow/setup/providers.go#L110) return `[]`. Wire ke discovery `~/.claude/skills/` beneran biar picker ada datanya.
- [ ] **Buang heartbeat/respawn manual**: udah di-handle pool, ga perlu duplikasi di workflow.

### P1 — picker dimana-mana (no free-text)

Mockup §3 (baris 1255–1452) + design §11.4 wajib dropdown + typeahead. Inspector sekarang [editor_inspector.templ:62](../tools/agents/view/workflow/editor_inspector.templ#L62) semua `<input type="text" placeholder="slack">`.

- [ ] **Channel dropdown**: ambil dari `mcp.ChannelsList()` (Describe()). Render `<select>` dengan satu option per channel registered.
- [ ] **Op dropdown (per channel)**: cascading select — pas Channel dipilih, list `Actions()`-nya (via MCP juga), tampilin ID + Description.
- [ ] **Connector module dropdown**: dari `mcp.ConnectorsList()`. Op dropdown cascade dari module yang dipilih.
- [ ] **Provider dropdown** (classify/agent): dari `mcp.ProvidersList()`. Default = `IsDefault`-nya registry.
- [ ] **Preset dropdown**: ganti hardcoded `classifier-cheap/classifier/support-responder` ([editor_inspector.templ:36](../tools/agents/view/workflow/editor_inspector.templ#L36)) sama option dari registry `internal/agents/preset`.
- [ ] **Skill multi-select**: skills agent node — multi-pick dari `provider.ListSkills(ctx)`.
- [ ] **Workspace dropdown**: agent node — dari registry `internal/agents/workspace`.
- [ ] **Trigger kind dropdown + form per-kind**: trigger node sekarang cuma `triggerKind` data attr. Bikin form bertipe: cron→schedule + timezone; channel→channel+event+target; webhook→path+method.
- [ ] **Data table dropdown**: node datatable_* — dari `workflow.DataTables[]` yang dideklarasi di workflow, bukan free text.
- [ ] **JS endpoint**: tambah `GET /tools/agents/workflows/api/registry` return JSON `{channels, connectors, providers, presets, data_tables, skills}` supaya editor.js bisa hydrate select pas load + abis save.

### P1 — save background (no full reload, error inline)

Sekarang: [editor_toolbar.templ:42](../tools/agents/view/workflow/editor_toolbar.templ#L42) plain `<form POST>` yang redirect pas sukses dan render `c.Error(400, ...)` halaman penuh pas validation fail ([workflows.go:140](../tools/agents/workflows.go#L140)). User kehilangan state canvas tiap kali error.

- [ ] **Endpoint POST return JSON**: ubah `saveWorkflow` jadi respond `{ok:true}` atau `{ok:false, errors:[{node,field,msg}]}`. Behavior redirect dipertahanin lewat `Accept: text/html` buat fallback no-JS.
- [ ] **Auto-save debounce**: editor.js — debounce 800ms abis node-data berubah, fetch POST body JSON. Pas error, gambar badge inline per-node (drawflow node border merah + tooltip).
- [ ] **Indicator status save**: toolbar tampilin `✓ Saved 2s ago` / `⟳ Saving…` / `✕ Save failed (retry)`. Mockup baris 1463.
- [ ] **Tombol Save manual tetap ada**: trigger flush + Validate instan.

### P1 — lifecycle draft vs publish (cuma 2 state)

Cuma dua state: **Draft** (lagi diedit, bisa live-test) dan **Published** (live, dipakai router).

Flow user:
```
edit di canvas ─► auto-save ke workflow.draft.yaml ─► Run Now (live test, jalanin draft)
                                                   └► Publish ─► promote draft → workflow.yaml
```

- [ ] **Layout file**: `workflow.yaml` = published, `workflow.draft.yaml` = work-in-progress. Editor selalu load draft kalau ada, fallback ke published. Publish copy draft → main + hapus draft file.
- [ ] **Service API**: `service.LoadDraft(id)` / `service.SaveDraft(id, w)` / `service.Publish(id)`. Save dari canvas selalu nulis ke draft, ga pernah ke `workflow.yaml` langsung.
- [ ] **Router behavior**: router register trigger cron/webhook/channel cuma buat versi *published*. Edit draft ga ngaruh ke run live sampai di-Publish.
- [ ] **Run Now (live test)**: tombol Run Now jalanin **draft** (bukan published), bypass Enabled — itu cara user test sebelum publish. Kalau draft ga ada, jalanin published.
- [ ] **UI toolbar**: badge `Draft` kalau `draft.yaml` exist. Tombol `Publish` di samping `Save` — disable sampai Validate lolos. Tombol `Discard draft` revert ke published.
- [ ] **Publish gate**: Publish wajib Validate ok. Itu doang.

### P1 — struktur canvas: clean layout + auto-format

Mockup §3 nunjukin DAG L→R rapi. Canvas sekarang free-form, ga ada auto-layout.

- [ ] **Tombol auto-format**: toolbar `⌘L` / `Layout` — jalanin Sugiyama / dagre layered layout di graph Drawflow. Persist posisi hasilnya ke `_canvas:` ([types.go:88](../agents/workflow/types.go#L88) field Canvas udah ada).
- [ ] **Format manual**: support grid snap (16px) pas drag-end.
- [ ] **Auto-layout di first load**: kalau `_canvas:` kosong, auto-layout dulu sebelum render.
- [ ] **Pilihan library**: pakai `dagre` (kecil, terkenal) wrap di `editor.js`. Alternatif: tulis layered layout minimal sendiri (~120 LOC).
- [ ] **Edge case: cycle** — fallback ke grid layout kalau ada cycle.

### P2 — polish

- [ ] **YAML round-trip preserve `_canvas:`**: cek `parse.Marshal` ngejaga posisi (sekarang `Workflow.Canvas` itu `map[string]any` — verify dia survive canvas→YAML→canvas).
- [ ] **Implicit reply node**: [channel/inject.go:16](../agents/workflow/channel/inject.go#L16) hardcode `op: reply_thread` — verify masih jalan abis channel registry live (channel non-Slack mungkin nama op-nya beda).
- [ ] **API catalog trigger registry**: expose `TriggerTypesCatalog()` lewat HTTP buat dropdown trigger node.
- [ ] **MCP op `workflow_publish`**: counterpart AI buat tombol Publish UI.

### P2 — Guard opsional (enable/disable)

Guard tetep ada tapi default off; user bisa enable per-install lewat settings.

- [ ] **Setting global enable/disable**: tambah config `agents.workflow.guard_enabled` (bool, default false) ke `internal/agents/config` atau settings page. Pas false, `Guard.Review` ga dipanggil, semua tombol Publish/Run lewat tanpa cek guard.
- [ ] **Mode field**: kalau enabled, mode `warn`/`block` di `guard.Config` tetap dipakai. Default mode = `warn` pas pertama enable.
- [ ] **UI toggle**: settings page agents tambahin checkbox "Enable workflow guard" + dropdown mode. Bottom-tab Guard di editor disembunyiin kalau disabled.
- [ ] **Publish gate**: Validate selalu wajib. Guard cuma wajib kalau enabled + mode=block.
- [ ] **Skip `Guard.Review` call**: di [workflows.go:92](../tools/agents/workflows.go#L92) + [workflows.go:183](../tools/agents/workflows.go#L183), guard nil-check udah ada — tinggal sambungin ke setting.

## Keputusan design (dari user, 2026-05-15)

- **Session = client pool agents**: workflow ga spawn CLI sendiri. Semua call classify/agent enqueue ke `internal/agents/pool` pakai sessionID sesuai mode (`new` UUID, `root` per-run, `persistent` per-workflow). Pool yang ngurus limit, queue, crash recovery, tracking sesi. Sesi workflow nongol di UI sessions agents.
- **Channel single-source**: deklarasi trigger + action cuma di `internal/agents/channels/Channel`. Pkg `internal/agents/workflow/channel/` dibuang. `SupportsSession()` per-channel — ga semua channel bisa origin sesi baru.
- **Draft & Publish doang**: dua state. Edit → auto-save ke `workflow.draft.yaml`. Run Now jalanin draft (live test). Publish promote draft → `workflow.yaml`, baru router register trigger.
- **Guard opsional**: pkg `internal/agents/workflow/guard/` tetap ada tapi default off. Toggle enable/disable + mode (warn/block) di settings. Publish gate wajib Validate; Guard cuma wajib kalau enabled + mode=block.

## File yang kebaca pas audit (read-only)

- internal/tools/agents/workflows.go
- internal/tools/agents/workflows_codec.go
- internal/tools/agents/view/workflow/* (editor, palette, inspector, toolbar)
- internal/tools/agents/js/workflow/editor.js
- internal/agents/workflow/types.go
- internal/agents/workflow/nodes/{agent,classify,channel}.go
- internal/agents/workflow/channel/{channel,slack,inject}.go
- internal/agents/workflow/provider/provider.go
- internal/agents/workflow/setup/{manager,connectors,providers}.go
- internal/agents/workflow/trigger/{router,webhook}.go
- internal/agents/workflow/mcp/mcp.go
- internal/jobs/registry.go
- internal/pkg/api/server.go (blok wiring workflow ~L370–399)
- internal/agents/channels/channel.go (interface Channel base)

## Quick reference — section design

- §7 Channel registry: workflow-design.md baris 2043–2138
- §8 Triggers (cron/webhook/dll): baris 743–945
- §9 MCP catalog: sekitar baris 2609
- §10 Session management: baris 1304–1401
- §11 Pickers (widget vocab): baris 3053–3074
- §13 Save semantics: baris 3147–3169
- §17 Bootstrap + jobs registration: baris 4197–4227, 124
- Approval/Guard: baris 2978–2983, 4301–4355
