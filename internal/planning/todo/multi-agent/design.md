# Multi-Agent — Sub-Agent Delegation, Rail UI, Interrupt (design + implementasi)

Status: **Fase 1–8 backend mendarat.** Bagian A–C jadi catatan desain, bukan
proposal lagi. Sisa pekerjaan = UI + beberapa knob yang belum ter-wire (§Status).
Dokumen tunggal: dari konsep sampai langkah implementasi.
Update terakhir: 2026-08-02.

---

# STATUS

Tiga kategori. Yang penting dibaca duluan: **⚠️ ada kodenya tapi belum
ter-wire** — di situ knob-nya terlihat hidup di API padahal runtime tak ada
yang membacanya.

## ✅ Jalan end-to-end

| Item | Isi | File utama |
|---|---|---|
| B1 Storage | `agent_profiles`, `agent_delegations`, `agent_squads`, `agent_boards`, `agent_tasks` (GORM AutoMigrate) | `internal/entity/agent_*.go`, `pkg/postgres/migrate.go` |
| B2 MCP | `wick_delegate`, `wick_agents`, `wick_delegate_collect`, `wick_tasks` | `internal/mcp/handlers/delegation*.go` |
| B3 Governor | depth, budget turn/root, cycle-guard, parallel, `max_turns` provider-agnostik, kill-switch live | `delegation/governor.go` |
| B4.1 #2 | **Scoped MCP token** — gap enforcement admin-bypass ditutup | `internal/mcp/scopedtoken.go`, `auth.go` |
| D1 | `pool.KillAgent` + `ErrAgentNotActive` | `pool/pool.go` |
| D2 | Interrupt 3 outcome + guard idempotensi di storage | `delegation/interrupt.go` |
| D3 | `Meta.ParentSessionID`, filter sebelum cap, redirect 303 | `session.go`, `api_conversation.go` |
| D4 | Rail tab + `SubAgentPanel` + badge live-only + coalesced refetch | `fe/.../SubAgentPanel.svelte`, `tools/agents/subagents.go` |
| D5 | Kartu ringkas `wick_delegate` di thread | `ToolCard.svelte` |
| D6 | Cascade kill leader → keturunan (kecuali detached) | `handler.go` `killAgent` |
| D8 | Grace → force **tanpa syarat**, process group POSIX + Windows kelas satu | `provider/terminate_*.go`, `provider/procgroup/` |
| Take-over | `allow_take_over` per-profil + `user_steered`; manusia saja | `delegation/takeover.go` |
| Fase 2 | `mode=async`, `delivery_sink`, `Detached`, context `WithoutCancel` | `delegation/{mode,run}.go` |
| Fase 3 | `workspace=worktree`, non-git → fallback shared + note | `delegation/worktree.go` |
| Fase 4 | `wick_delegate_collect` + callback; serah-terima tepat sekali | `delegation/collect.go` |
| Fase 6 | Cap token per-delegasi + per-root; usage ditulis sebelum cek cancel | `delegation/cost.go` |
| Fase 7 | Board: enqueue/claim/start/complete, claim exclusive, sweeper stale-claim | `delegation/{board,sweeper}.go` |
| Fase 8 | Kanban: stage ≠ column id, evidence-gate di jalur MCP **dan** drag | `delegation/board.go`, `tools/agents/api_boards.go` |
| Fleet monitor | `GET /api/monitor/snapshot` (data + ACL) | `tools/agents/monitor.go` |
| Config | Semua knob governor di `GeneralConfig`, key snake_case terverifikasi test | `agents/config/general.go` |

## ⚠️ Ada kodenya, BELUM ter-wire — knob mati

Doc ini menolak "menambah knob governor tanpa kode yang membacanya"
([§Rejected](#rejected-alternatives), bukti: stoa punya dua setting mati).
Empat item di bawah **persis kasus itu** dan harus diselesaikan atau dihapus:

| Item | Kondisi sekarang | Akibatnya |
|---|---|---|
| **Fase 5 squad — roster narrowing** | `FilterBySquad` + `SquadAllows` ada + diuji, CRUD API ada. **Tak dipanggil** dari `WickAgents`/`WickDelegate` | Squad tersimpan rapi tapi **runtime-nya nol**: leader tetap lihat semua profil yang lolos tag. Fase 5 baru separuh |
| **`board.AutoDelegate`** | Kolom ada, disimpan, dikembalikan API. Tak ada yang membaca | Toggle "auto-delegate saat stage=ready" di UI nanti akan terlihat aktif tapi tak melakukan apa-apa |
| **`Request.TaskID`** | Field dideklarasi. Tak ada satu pun caller yang mengisi | Delegasi yang lahir dari task board tak bisa dilacak balik ke task-nya |
| **`StartTask(..., delegationID)`** | Handler MCP mengirim `""` | Task `started` tak pernah tertaut ke delegasi yang mengeksekusinya — kolom `delegation_id` selalu kosong |

Tiga yang terakhir saling terkait: menyambung task → delegasi menutup ketiganya
sekaligus.

## ❌ Belum ada sama sekali

| Item | Catatan |
|---|---|
| **UI board / kanban** | API + data lengkap (`/api/boards`, `/api/tasks/*`), **nol komponen Svelte**. Bikin board pertama sekarang harus lewat `POST /api/boards` |
| **UI fleet monitor** | Endpoint snapshot siap; halaman `/agents/monitor` + grid kartu belum ada |
| **Editor squad** | CRUD API siap; halaman editor belum ada. (**Editor profil sudah mendarat** — SPA `fe/agents/agent-profiles/` + tab di project-settings, lihat `in-progress/agent-scope/`) |
| **Kill-switch tidak sesuai deskripsinya** | Desc `SubAgentsEnabled` menjanjikan "the tools disappear entirely". Nyatanya connector `sub-agents` tetap terlihat, `list_agents`/`create_agent` sukses, hanya `delegate` yang ditolak `governor.go:186`. Agent bisa bikin role, list-nya jalan, baru mentok di langkah terakhir — bacaan yang wajar dari situ: "diblokir admin", bukan "belum dinyalakan". Pilih: sembunyikan connector saat off, atau perbaiki desc-nya |
| **Section prompt tidak kondisional** | Blok Sub-agents di `system-prompt/immutable.md:120-147` selalu ikut terkirim, termasuk saat `sub_agents_enabled=false` → prompt menyuruh delegasi ke fitur yang pasti menolak |
| **Roster tidak pernah disuntik** | Desain menjanjikan roster masuk system context leader; implementasinya nol. Agent wajib `list_agents` dulu, kalau lupa ya menebak key. Ditangani di [agent-mention](../agent-mention/design.md) §2.2 |
| **Tiga knob mati di editor profil** | `can_delegate`, `strict_mcp`, `allowed_native_tools` tersimpan dan tampil di form, tak ada yang membacanya (`in-progress/agent-scope/design.md`). Persis pola yang doc ini tolak — selesaikan atau hapus dari form |
| **Approval/AskUser dari sub-agent** | [OQ #2](#open-questions) belum diputuskan — usul: naik ke level session + badge |
| **Gemini sebagai leader** | [OQ #1](#open-questions) belum diverifikasi; sementara `can_delegate` dipaksa off (bukan leader-capable) |
| **mockup.html** | Belum disinkronkan dengan Bagian C sekarang |

## Koreksi terhadap dokumen ini (ditemukan saat implementasi)

- `internal/entity/migrations/NNNN_*.go` **tidak ada** — repo pakai GORM
  `AutoMigrate` di `internal/pkg/postgres/migrate.go`.
- Setelan governor **bukan** halaman `/manager/agents/settings` terpisah; masuk
  `GeneralConfig` group *Sub-agents* supaya dapat UI setelan gratis.
- **OQ #3 terjawab: `ConversationThread` reusable.** 236 baris, murni props, tak
  ada dependensi ke state `DetailView` — panel tak perlu dikirim degraded.
- `session_id` diambil dari header `X-Wick-Session-Id`, **bukan** argumen model —
  kalau dari model, leader bisa menempel delegasi ke pohon milik orang lain dan
  mewarisi identitas pohon itu.
- `InterruptAll` dipecah dua: **cascade** (kill leader) melewati sub-agent
  `Detached`; **Stop all** eksplisit tetap menghentikan semuanya.
- Token ceiling boleh `0` = "tanpa cap" (beda dari brake lain yang 0-nya
  di-normalize ke default) — provider yang tak melapor usage tak bisa di-cap,
  memaksa floor cuma bikin limit yang diam-diam tak pernah nyala.

## Bug yang ketangkep test (bukan produksi)

- **`ParseUsage` case-sensitive** — guard `Contains(raw, "tokens")` tak match
  `inputTokens`. Provider camelCase dilaporkan **0 token = dianggap gratis =
  lolos semua cap biaya**. Yang paling mahal dari ketiganya.
- **`setProcessGroup` duplikat** dengan package `procgroup` → build gagal di test
  pool.
- **Guard arsitektur `TestNoDirectOSExec`** — repo mewajibkan `safeexec`, bukan
  `os/exec`. Kena di worktree + taskkill.

## Verifikasi terakhir (2026-08-02)

- **86 paket Go pass**, build + vet bersih.
- Satu-satunya fail: `provider/codex` integration (2 test) — dibuktikan
  **pre-existing** lewat stash-compare uncached; butuh binary/kredensial codex.
- FE: svelte-check 21 error = **identik baseline**; vitest 2 fail / 672 pass =
  identik baseline (`browser.test.ts`, pre-existing).

---

**Paradigma sekarang:** wick = **1 conversation → 1 active agent** yang spawn
CLI subprocess (`internal/agents/provider/*`). Konsep multi-agent persisten
per-session **sudah ada** (`session/agents.go:19-30` `AgentEntry` list +
`pool.Send(sessionID, agentName)` `pool.go:338-363`). Yang belum ada: cara satu
agent **memanggil** agent lain, governor turn lintas-agent, dan visualisasinya.

Desain ini menambah **delegasi sub-agent sinkron** (gaya Task-tool) di atas
fondasi itu, plus **UI rail** dan **interrupt granular**:

1. **Profil agent reusable** — role (researcher, coder, reviewer) didefinisikan
   sekali, dipakai lintas session.
2. **MCP tool `wick_delegate`** — leader panggil tool, MENUNGGU, hasil akhir
   sub-agent balik sebagai tool result. Reuse `pool` untuk spawn sub-session
   terisolasi.
3. **Governor** — nested + budget turn per-root + `max_turns` per sub-agent +
   `max_depth` + cycle-guard, di-enforce **wick-side** (tak bergantung flag provider).
4. **Rail UI** — sub-agent = satu tab di rail kanan; main thread cuma kartu
   ringkas. Child session disembunyikan dari daftar conversation.
5. **Interrupt 3 tingkat** — stop satu anak / semua anak / seluruh pohon, dengan
   **hasil partial dikembalikan ke leader**, bukan error.

Paired mockup: [`mockup.html`](mockup.html) — update barengan dokumen ini.
Riset pembanding (bukti `file:line` dari routa/stoa/multica): [Bagian E](#bagian-e--prior-art).

---

## TODO

### Sudah diputuskan — Fase 1

- ✓ **Delegasi SINKRON** — leader panggil `wick_delegate(profile, task)`, blocks,
  terima hasil akhir sebagai tool result. Bukan task-board asinkron.
- ✓ **Profil reusable** — tabel `agent_profiles`. Role = provider + model +
  system_prompt + allowed_tag_ids + default_max_turns.
- ✓ **Sub-session terisolasi** — tiap delegasi spawn session baru, konteks bersih
  (hanya task prompt + roster), project/cwd sama parent. Sub-agent TIDAK lihat
  history leader.
- ✓ **Nested + budget** — sub-agent boleh delegasi lagi, dibatasi `max_depth`,
  budget turn per-root, cycle-guard.
- ✓ **`max_turns` provider-agnostik** — hitung `event.Done` + `pool.Kill`. Flag
  CLI native hanya optimisasi.
- ✓ **Paralel** — leader boleh emit beberapa `wick_delegate` sekaligus, dibatasi
  `max_parallel`.
- ✓ **ACL via tag** — profil gated tag (pola connector `IsVisibleTo`).
  Create/edit profil = admin-only.
- ✓ **Audit seragam** — tiap delegasi tulis row `agent_delegations`.
- ✓ **UI = rail tab kanan**, bukan nested penuh di transcript ([§C1](#c1--rail-tab)).
  Ini **mengganti** rencana awal "pohon delegasi nested".
- ✓ **Child session disembunyikan** dari daftar conversation ([§C3](#c3--child-session-disembunyikan)).
- ✓ **Interrupt = partial ke leader, bukan error** ([§C4](#c4--interrupt)).
  Preseden produksi: stoa `claude-session.js:171-185`.
- ✓ **Config berlapis** — global=plafon, profil=identitas role, project=roster,
  session=eksperimen. **Bukan** per-provider ([§C5](#c5--scoping-config)).

### Ditahan sampai sign-off

- ✓ **Take-over** — SUDAH ADA. Per-profil `allow_take_over` (default false),
  hasil ditandai `user_steered` dan leader diberitahu. Manusia saja, bukan agent.
- ⏸ **Agent bikin profil sendiri lewat MCP** — privilege escalation lewat tag ACL.
  v1: hanya **spawn ad-hoc** dengan tag warisan ([§C6](#c6--agent-spawn-lewat-mcp)).
- ⏸ **Human-in-the-loop pada sub-agent** — v1 sub-agent warisi gate config parent.
- ✓ **Grace period + process group** — SUDAH ADA, dikerjakan **sebagai perubahan
  terpisah** setelah Fase 1 mendarat (persis maksud catatan aslinya: jangan
  digabung ke PR fitur UI). Windows kelas satu, bukan jalur kedua
  ([§D8](#d8--catatan-teardown-proses)).

### Fase berikutnya — backend mendarat (2026-08-02), UI belum

- ✓ **Fase 2 — Async fire-and-forget** (`mode`, `delivery_sink`, detached).
- ✓ **Fase 3 — Workspace isolation** (`workspace=worktree`, fallback shared).
- ✓ **Fase 4 — Async collect** (callback ke leader + `wick_delegate_collect`).
- ⚠️ **Fase 5 — Squad eksplisit** — storage + logika + CRUD API ada, tapi
  `FilterBySquad` **belum dipanggil** dari jalur delegasi. Lihat §Status.
- ✓ **Fase 6 — Token budget** (parse usage raw CLI, cap per-delegasi + per-root).
- ✓ **Fase 7 — Async task-board** (enqueue/claim/start/complete).
- ✓ **Fase 8 — Kanban** (stage discriminator, evidence-gate, policy satu evaluator).

Sisa yang belum: **UI Svelte** untuk board/kanban/monitor, dan halaman editor
profil/squad/board. API + data sudah lengkap.

---

## Roadmap

| Fase | Fitur | Bergantung | Effort |
|---|---|---|---|
| **1 — Core sync delegation + rail UI + interrupt** | Profiles + `agent_delegations`; `wick_delegate` + `wick_agents`; isolated sub-session; max-turns provider-agnostik; governor; ACL via tag; **rail tab + interrupt granular**; child session hidden | pool, event, SSE, tags | Sedang |
| **2 — Async fire-and-forget** | `default_mode` per-profil + `mode` per-call; `delivery_sink`; sub-agent detached | Fase 1 | Kecil–sedang |
| **3 — Workspace isolation** | `workspace = shared \| worktree`; paralel coding tanpa tabrakan | Fase 1 | Sedang |
| **4 — Async collect** | `delivery_sink=session` → callback re-prompt leader; `wick_delegate_collect(id)` | Fase 2 | Sedang |
| **5 — Squad eksplisit** | Squad bernama = leader + member tetap; leader-routing | Fase 1 | Sedang |
| **6 — Token budget** | Capture usage dari raw CLI; cap biaya | Fase 1 | Kecil–sedang |
| **7 — Async task-board** | enqueue/claim/start/complete di atas `agent_delegations` | Fase 2/4 | Besar |
| **Boundary (future)** | Distributed: ganti pipe lokal dgn message bus + auth antar-node | Fase 1 | Besar |

**Mode per use-case:** role analisis/report yang hasilnya untuk manusia → async
(Fase 2). Orchestrator yang **mengumpulkan** banyak hasil → sync (Fase 1) atau
async-collect (Fase 4). Agentic-code dengan task dependen → sync + worktree (Fase 3).

---

## Naming

| Konteks | Pilihan |
|---|---|
| Fitur (UI label) | **"Sub-agents"** di nav dan rail tab |
| Go package | **`internal/agents/delegation/`** |
| MCP tools | `wick_delegate` (action) + `wick_agents` (roster) |
| Tabel | `agent_profiles`, `agent_delegations` |
| Rail tab id | `"subagents"` |

---

# BAGIAN A — Konsep

## A1 · Tujuan & non-goal

**Tujuan:**

- Satu agent bisa **mendelegasikan sub-tugas** ke agent lain dengan role berbeda,
  lalu lanjut dengan hasilnya — tanpa human switch agent manual.
- Pakai infrastruktur existing: `pool` (spawn/lifecycle/Kill), event
  ter-normalisasi, SSE hub, tags ACL, MCP dispatch, **rail UI**.
- Role reusable lintas conversation.
- Aman & terkendali: budget turn, depth limit, cycle-guard — semua wick-side,
  **independen dukungan provider**.
- Observability: user lihat realtime agent mana kerja/idle/mati, dan **bisa
  menghentikannya per-agent**.

**Non-goal:**

- **Fase 1 sinkron** — async di Fase 2+. Task-board penuh di Fase 7 opsional.
- Bukan **chatroom multi-agent** (stoa) — sub-agent tak saling ngobrol bebas;
  komunikasi cuma lewat hasil delegasi. (Bukti kenapa: [§E3](#e3--stoa).)
- Bukan **runtime plugin / provider baru** — sub-agent = provider existing.
- Bukan **scheduler** — delegasi dipicu leader saat runtime, bukan cron.

## A2 · Terminologi

```
AgentProfile (role reusable)
├─ Key, Name, Description, Icon       — admin-set
├─ Provider     — "claude" | "codex" | "gemini"
├─ Model        — provider-specific model id
├─ SystemPrompt — role instruction
├─ AllowedTagIDs — OPSIONAL penyempit (subset tag user). Kosong=warisi (§B4)
├─ AllowedNativeTools — OPSIONAL allowlist tool native provider (§B4.2)
├─ StrictMCP     — default true: abaikan MCP eksternal host (§B4.2)
├─ DefaultMaxTurns — budget turn default
├─ DefaultMode      — "sync" | "async"  (Fase 2)
├─ DefaultWorkspace — "shared" | "worktree" (Fase 3)
├─ CanDelegate  — boleh jadi leader?
└─ AllowTakeOver — user boleh kirim pesan langsung? (⏸ ditahan, §F)

Delegation (satu pemanggilan wick_delegate)
├─ RootID            — id delegasi paling atas
├─ ParentSessionID   — session leader yang memanggil
├─ ProfileKey        — role yang di-spawn
├─ ChildSessionID    — execution context terisolasi
├─ Task, Depth, Mode, DeliverySink, Workspace
├─ Status  — queued|running|done|failed|interrupted|stopped_max_turns|stopped_budget
├─ TurnsUsed
└─ Result — teks hasil akhir / error / partial
```

**Kepemilikan.** Session `agents/session/{id}` adalah **leader** = pemilik
percakapan. Sub-agent bernaung di bawahnya. Runtime-nya tiap sub-agent punya
`ChildSessionID` terisolasi (konteks bersih), tapi **di-link** via
`parent_session_id` + `root_id`. Isolasi = pemisahan **konteks**, bukan
pemisahan **kepemilikan**.

| Term | Arti |
|---|---|
| **Leader** | Agent yang memanggil `wick_delegate`. Harus provider yang dukung MCP tool-use |
| **Sub-agent** | Agent yang di-spawn untuk satu task |
| **Root delegation** | Delegasi level-0. Budget dihitung per-root |
| **Isolated sub-session** | Session baru, konteks bersih, cwd sama |

**Hubungan ke yang sudah ada:**

```
pool.Send(sessionID, agentName, …)   (existing — routing ke 1 agent)
session/agents.go AgentEntry[]       (existing — N named agent / session)
        │
        ▼  delegation menambah:
delegation.Run(ctx, parentSess, profileKey, task, depth, rootID)
  → resolve AgentProfile → cek governor → spawn isolated child session
  → pool.Send(childSess, task) → tunggu event.Done terakhir
  → enforce max_turns: count Done; pool.Kill kalau lewat
  → tulis agent_delegations row → return Result ke caller
```

## A3 · Batas vs workflow engine

wick sudah punya workflow DAG (*agent node* + *classify node*) = orkestrasi
**deterministik**. Delegation = orkestrasi **dinamis (LLM yang putuskan)**.

Rule of thumb: **alur tetap & bercabang jelas → workflow**; **pembagian kerja
ad-hoc oleh leader saat runtime → delegation**.

---

# BAGIAN B — Runtime & storage

## B1 · Storage

```sql
agent_profiles (
  id                uuid primary key,
  key               text unique not null,        -- "researcher", "code-reviewer"
  name              text not null,
  description       text,
  icon              text default '🤖',
  provider          text not null,               -- "claude" | "codex" | "gemini"
  model             text,                        -- null = provider default
  system_prompt     text not null,
  allowed_tag_ids      jsonb not null default '[]', -- []=warisi penuh tag user; isi=persempit (§B4)
  allowed_native_tools jsonb not null default '[]', -- allowlist tool native (§B4.2)
  strict_mcp           boolean not null default true, -- abaikan MCP eksternal host
  default_max_turns int  not null default 12,
  default_mode      text not null default 'sync',     -- Fase 2
  default_workspace text not null default 'shared',   -- Fase 3
  can_delegate      boolean not null default false,
  allow_take_over   boolean not null default false,   -- ⏸ ditahan (§F)
  created_by        uuid not null,
  created_at        timestamptz,
  updated_at        timestamptz,
  disabled          boolean default false
)

agent_delegations (
  id                 uuid primary key,
  root_id            uuid not null,              -- self jika depth=0
  parent_session_id  text not null,
  parent_agent       text not null,
  profile_key        text not null,
  child_session_id   text not null,
  task               text not null,
  depth              int  not null default 0,
  mode               text not null default 'sync',
  delivery_sink      text,
  workspace          text not null default 'shared',
  workspace_path     text,
  -- interrupted = MANUSIA yang menghentikan; beda dari stopped_* (governor)
  -- dan failed (error runtime). Lihat §C4.
  status             text not null,              -- queued|running|done|failed|interrupted|stopped_max_turns|stopped_budget
  turns_used         int  not null default 0,
  result             text,
  error_msg          text,
  started_at         timestamptz not null,
  ended_at           timestamptz,
  triggered_by       uuid                        -- untuk ACL monitor + interrupt
)

create index idx_agent_delegations_root   on agent_delegations(root_id);
create index idx_agent_delegations_status on agent_delegations(status);
create index idx_agent_delegations_parent on agent_delegations(parent_session_id);
```

**Reuse tanpa perubahan skema:** `pool.active` + `runEntry` (status live),
`internal/agents/event` (turn counting), SSE `Broadcaster` (live monitor),
`tags`/`tool_tags`/`user_tags` (ACL), `sessions` (child ikut layout existing).

⚠️ **Session wick berbasis FILE, bukan SQL.** `session.Meta`
(`session/session.go:54`) diserialisasi ke `meta.json`; `session.List`
(`session.go:247`) hanya `storage.ScanDirNames`. Field `ParentSessionID` masuk
ke `Meta`, dan filternya di Go — **bukan** `WHERE parent_session_id IS NOT NULL`.
Lihat [§C3](#c3--child-session-disembunyikan).

**Retensi:** `agent_delegations` bisa tumbuh cepat (async/paralel) → siapkan
cron prune by `ended_at` atau cap row per-root.

### Profil → spawn config

| Profil | Param pool/agent |
|---|---|
| `provider` + `model` | resolve ke factory pool existing |
| `system_prompt` | initial system context child (disuntik saat session-start) |
| `allowed_tag_ids` | filter tools yang terlihat sub-agent |
| `default_max_turns` | governor counter (bukan hanya `--max-turns`) |
| `can_delegate` | apakah `wick_delegate` masuk allowlist sub-agent |

### Relasi ke Presets

Hari ini **belum ada** "agent profile" reusable. Analog terdekat = **Presets**
(`presets/<name>/agent.md` — persona saja) + **Project Defaults**
(`project/project.go:28-32`). `agent_profiles` = **generalisasi Preset** (Preset
hanya system-prompt; Profile menambah provider+model+tag+max_turns+mode+
workspace+can_delegate) — bukan duplikat. Opsi: Profile boleh **mereferensikan**
Preset sebagai system-prompt-nya.

> **Layak dipertimbangkan (dari routa, [§E2](#e2--routa)):** resolution chain
> berprioritas — DB=100 / `~/.wick/profiles/`=75 / bundled=50 / hardcoded=25,
> merge by `key`. Efeknya: profil bawaan bisa di-override user per-file tanpa
> migrasi, dan sistem tetap jalan sebelum ada row DB apa pun.

## B2 · MCP surface

Dua tool baru di `internal/mcp/handlers`, muncul di `handleToolsList`, dispatch
di `handleToolsCall`. ACL server-side.

### `wick_agents` — roster yang boleh dipanggil

```jsonc
// input: {} | {"include_disabled": false}
{ "agents": [
    { "key":"researcher", "name":"Researcher", "description":"Web + docs research. Returns a cited summary.", "provider":"claude" },
    { "key":"code-reviewer","name":"Code Reviewer","description":"Reviews a diff. Returns findings list.","provider":"codex" } ] }
```

Hanya profil enabled yang **lolos tag** caller. Roster juga **disuntik ke system
context leader** saat spawn supaya leader tahu siapa yang bisa dipanggil tanpa
call tool dulu.

### `wick_delegate` — delegasikan satu task (blocking)

```jsonc
// input:
{
  "profile": "researcher",          // required
  "task": "Cari changelog breaking lib X v3→4, ringkas + sitasi.", // required
  "context": "Repo pakai X v3.2.",  // optional — bukan history penuh leader
  "max_turns": 8,                   // optional — ≤ cap global
  "mode": "async",                  // optional (Fase 2)
  "delivery_sink": "channel",       // optional (Fase 2/4)
  "workspace": "worktree"           // optional (Fase 3)
}

// sukses:
{ "profile":"researcher", "status":"done", "turns_used":5, "result":"Breaking changes v3→v4: …" }

// turns habis:
{ "profile":"researcher", "status":"stopped_max_turns", "turns_used":8,
  "result":"<partial>", "note":"Dihentikan saat mencapai max_turns=8. Hasil parsial." }

// DI-INTERRUPT MANUSIA (§C4) — bentuk persis, leader membaca ini:
{ "profile":"researcher", "status":"interrupted", "turns_used":3,
  "result":"<partial>",
  "note":"Interrupted by the user. Decide: continue without it, re-delegate, or ask the user. Do NOT silently retry." }
```

**Paralel:** beberapa block `wick_delegate` dalam satu turn jalan **konkuren**
sampai cap `max_parallel`. Tak ada tool batch khusus — paralelisme alami dari
multiple tool_use.

**Mode async (Fase 2+):** tak blocking, balik handle `delegation_id`. Hasil
dikirim lewat `delivery_sink`: `channel` (post ke thread asal), `none` (cuma
monitor), `session` (callback re-prompt leader — Fase 4). Sub-agent async
**detached**: tetap jalan walau leader idle-kill.

## B3 · Governor

Lima rem independen, semua wick-side.

### B3.1 `max_turns` per sub-agent (provider-agnostik) ⭐

- **Universal:** hitung `event.Done` per child session dari stream
  ter-normalisasi (`event/types.go:41-42`; tiap provider emit `Done` di akhir
  agentic turn via `agent.go:854-860`). Counter capai `effective_max_turns` →
  `pool.Kill(childSession, agentName)` → hasil parsial + `stopped_max_turns`.
- **Optimisasi per-provider:** kalau ada flag native (claude `--max-turns` via
  `SetMaxTurns` `session/agents.go:87-105`), pasang juga supaya CLI berhenti rapi
  SEBELUM force-kill. Untuk codex/gemini yang tak punya → counter+Kill **satu-satunya**
  mekanisme, dan itu cukup.
- `effective = min(input.max_turns || profile.default_max_turns, cap_global)`.

### B3.2–B3.5 Depth, budget, cycle, paralel

| Rem | Default | Aturan |
|---|---|---|
| `max_depth` | 3 | `Run` tolak kalau `depth > max_depth` |
| Budget turn per-root | 40 | Counter agregat semua sub-agent di pohon. Habis → delegasi baru ditolak `stopped_budget`; **yang jalan dibiarkan selesai** |
| Cycle-guard | — | Child mewarisi ancestor chain (profile_key root→parent). Tolak kalau `profile` sudah ada di chain (cegah A→B→A) |
| `max_parallel` | 4 | Cap konkuren per-root. Lewat → antre lalu jalan saat slot bebas |

⚠️ **Depth limit ini pembeda penting.** multica **tak punya** `depth`/`root_id`
dan tak punya guard rekursi — dua agent yang saling mention bisa spawn task tanpa
henti ([§E1](#e1--multica)). Jangan hilangkan.

### B3.6 Async/detached (Fase 2+)

Mode async menaikkan risiko: "fire banyak async" bisa meledakkan jumlah
sub-agent. Maka `max_parallel` + budget per-root jadi rem utama (bukan blocking
alami seperti sync). Sub-agent detached tetap dihitung ke budget root.

### Dua tingkat setelan — jangan dicampur

- **Governor = GLOBAL/system-wide** (`max_depth`, budget/root, `max_parallel`,
  cap `max_turns`, kill-switch). Plafon **keras** untuk SEMUA delegasi.
  UI: `/manager/agents/settings` — **bukan** di profile editor.
- **Per-profil** (`default_mode`, `default_workspace`, `default_max_turns`,
  `can_delegate`, tag). UI: profile editor.

Profil **tak boleh melampaui** plafon global (di-clamp). Scope-nya beda: sistem
vs role. Detail lengkap: [§C5](#c5--scoping-config).

**Global kill-switch:** toggle env/setting untuk mematikan delegation total
(mirip `WICK_DISABLE_SHARED_MCP`) — aman untuk rilis bertahap + emergency stop.

## B4 · Tags / ACL

Pola **sama persis** dengan connector (`internal/connectors/service.go`
`ListVisibleTo`/`IsVisibleTo`).

- **Create/edit/delete profil** = admin-only.
- **Siapa boleh delegasi ke profil X** = filter tag terpisah (auto-create
  `agent:<key>` saat save, tiru `CreateOwnerTag` `tags/service.go:114-164`).
  Default admin-only sampai admin assign tag.
- **Monitor + interrupt** = non-admin lihat/hentikan delegasi yang
  `triggered_by` = dirinya; admin semua.

### B4.1 Pewarisan hak + scoped MCP identity (enforcement-critical)

1. **Akses melekat ke user; profil hanya menyempit.**
   - Default: sub-agent **mewarisi tag user pemicu root** → otomatis dalam batas
     izin manusia yang memulai. Ini plafon keamanannya.
   - `profile.allowed_tag_ids` = **penyempit OPSIONAL**, bukan grant terpisah.
     Rumus: `efektif = user_tags ∩ (allowed_tag_ids ?: user_tags)`.
   - Profil **tidak pernah bisa menambah** akses di luar tag user.

2. ⚠️ **Identitas MCP ber-scope, BUKAN `MCPToken` admin.** Agent yang di-spawn
   wick **sekarang** autentikasi ke loopback MCP pakai `MCPToken` global →
   principal admin sintetis (`mcp/auth.go:85-88`) → `isAdmin=true` → **filter tag
   di-BYPASS**. Kalau sub-agent diberi `MCPToken` apa adanya, kolom "Tool access
   (tags)" tak berefek. Sub-agent **wajib** dapat token MCP yang membawa tag
   ber-scope. **Item implementasi nyata, bukan UI.**

**Cakupan tag:** seluruh permukaan tag wick — **connector** + **tools built-in**
(`internal/tools/*`) + **jobs**, semuanya lewat `tool_tags`/`ToolPath` +
`IsVisibleTo`. Yang **TIDAK** tercakup: tools native provider & MCP eksternal
host → §B4.2.

**Alignment v0.16.0 (diverifikasi):** owner-tag sudah ada sebagai precedent
(`tags/service.go:114-164`); multi-identity sudah ada (`ConnectorAccount`,
`entity/connector.go:176-206`) dan bisa jadi fondasi token ber-scope — tapi
caveat #2 **masih gap**: spawn tetap pakai `MCPToken` global (`spawn.go:63-124`).
Tag filter masih row-level, bukan account-aware. Destructive ops kini default
`Enabled=true` (LLM konfirmasi) — sub-agent ikut model ini.

### B4.2 Tools di luar MCP wick

| Sumber tool | Lever | Sifat |
|---|---|---|
| Connector / MCP internal wick | tag ACL server-side (`IsVisibleTo`) | **hard** |
| Tools native provider | `allowed_native_tools` → `--allowedTools`/`--disallowedTools` + command-gate | CLI-enforced + gate server-side |
| MCP eksternal di `~/.claude.json` host | `strict_mcp=true` → `--strict-mcp-config` | **hard** (arg spawn) |
| strict OFF + tanpa allowlist | prompt saja | **lemah** — hindari |

**Default sub-agent:** `strict_mcp=true` + `allowed_native_tools` per-profil.
Konsekuensi: Slack dsb hanya terjangkau kalau di-ekspos sebagai **connector wick**
(tag-gated), bukan via MCP eksternal.

**Catatan kejujuran:** boundary yang benar-benar server-side = connector wick
(tag) + command-gate. `--allowedTools` di-enforce CLI (mencegah model memanggil —
client-side tapi nyata); `--strict-mcp-config` efektif **hard**. Provider tanpa
flag allowlist → jatuh ke command-gate + prompt.

## B5 · Provider compatibility

| Peran | Syarat | claude | codex | gemini |
|---|---|---|---|---|
| **Leader** (`wick_delegate`) | MCP tool-use | ✅ | ✅ | ⚠️ verifikasi |
| **Sub-agent** | bisa di-spawn pool | ✅ | ✅ | ✅ |
| `--max-turns` native | flag CLI | ✅ | ⚠️ | ⚠️ |
| Turn-enforcement wick (Done+Kill) | universal | ✅ | ✅ | ✅ |

**Fallback:** provider tanpa MCP tool-use → tak bisa leader (`can_delegate=false`
dipaksa di UI + validasi save); tetap valid jadi sub-agent. Provider tanpa
`--max-turns` → counter+Kill; bedanya cuma berhenti via kill alih-alih exit rapi
→ partial di-capture dari event yang sudah masuk.

## B6 · Backward compat

- Single-agent flow existing **tidak berubah**. Session tanpa delegasi = persis
  seperti sekarang.
- `pool.Send` / `AgentEntry` / event model **tidak berubah** — delegation adalah
  consumer di atasnya.
- Migration: tambah 2 tabel. No drop/alter tabel existing.
- MCP: tambah 2 meta-tool. Tool lain tak tersentuh.
- `session.Meta` tambah 1 field opsional (`ParentSessionID`) — session lama tetap
  valid (kosong = bukan anak).

---

# BAGIAN C — UI & interrupt

Bagian ini **mengganti** rencana awal "pohon delegasi nested di transcript".

**Masalah rencana awal:** kalau leader delegasi 4 sub-agent, main thread penuh
kartu spinner + transcript expand → hasil akhir leader terkubur ratusan baris.
Dan tak ada tempat natural untuk tombol interrupt per-anak.

| Situasi | Nested (rencana awal) | Rail tab (sekarang) |
|---|---|---|
| 4 sub-agent jalan | 4 transcript expand → ratusan baris | 4 baris ringkas |
| Baca hasil akhir leader | scroll lewat semua anak | langsung |
| Depth 2–3 | indent bertumpuk | tree + breadcrumb |
| Interrupt satu anak | tak ada tempat natural | tombol di baris panel |
| Tahu ada aktivitas tanpa membuka | — | badge angka di tab |

Yang **tidak** berubah: ownership (`parent_session_id` + `root_id`), isolasi
konteks, governor, ACL tag. Ini murni perubahan **tempat menampilkan** + interrupt.

## C1 · Rail tab

Rail kanan hidup di `DetailView.svelte`. Sub-agent jadi anggota baru `RailTab` —
semua mekanismenya **sudah ada**:

| Kebutuhan | Sudah ada | Baris |
|---|---|---|
| Daftar tab + ikon | `railTabsAll` | `:1016` |
| Buka/tutup panel | `toggleRail(tab)` | `:921` |
| Badge angka | `railCount(id)` | `:1070` |
| Tab kondisional | `railTabs = $derived(hasBrowserInstance ? …)` | `:1049` |
| Auto-close saat tab hilang | `$effect` → `railTab = null` | `:1053` |
| Aksi per-baris (contoh interrupt) | `ProcessPanel onKill/onDequeue` | `:1297` |
| Coalesce refetch SSE | `scheduleProcessReload()` | `:481` |
| Keyboard shortcut | map `"panel:process"` | `:186` |

**Badge = jumlah HIDUP, bukan total.** Ikuti `liveProcesses` yang memfilter baris
idle — anak `done` tak boleh menaikkan badge, kalau tidak badge nyangkut setelah
semua selesai. Tapi `railTabs` pakai **total** supaya tab tetap terlihat untuk
membaca hasil.

Detail langkah: [§D4](#d4--railtab--panel).

## C2 · Isi panel — sama seperti conversation, tapi jangan sama persis

Reuse **`ConversationThread`**, bukan `DetailView`.

| Elemen | Ikut? | Alasan |
|---|---|---|
| Transcript, tool card, thinking, artifact, imagecard | ✅ reuse | rendering harus identik |
| Composer | ❌ kecuali `allow_take_over` | v1 read-only |
| Rail sendiri | ❌ | **rail bersarang di dalam rail** |
| Header session (kill/delete/provider switch) | ❌ | ganti breadcrumb + Stop |
| Approvals / AskUser modal | ⚠️ perlu keputusan | naik ke session atau di panel? → [OQ](#open-questions) |

Navigasi pakai **breadcrumb** (`Main › image #2`), bukan tab per-anak — depth
bisa >1 dan jumlah anak tak terbatas.

## C3 · Child session disembunyikan

Sub-agent **secara teknis** session penuh (punya store, transcript, workspace
sendiri — bukan objek kelas dua), tapi **secara tampilan** difilter dari daftar
conversation dan muncul di rail induknya.

- **Bukan** field `type: subagent` baru — `parent_session_id` sudah cukup; dua
  sumber kebenaran bisa bertentangan.
- Seam: **`apiSessionList`** (`api_conversation.go:73`), bukan `GET /sessions`
  (itu render HTML).
- Filter dipasang **sebelum** `sessionListCap` (`:86`) — kalau sesudah, child
  memakan kuota cap dan induk yang sah bisa terpotong.
- `session.List` **jangan** disentuh — reaper/sweeper/migrasi harus tetap lihat
  semua sesi.
- URL child yang dibookmark **jangan 404** → redirect ke induk dengan rail
  terbuka pada anak itu.

> Presedennya jelas: stoa menaruh agent di daftar room dan **kehilangan**
> kemampuan melihat transcript per-agent ([§E3](#e3--stoa)). Rail memberi
> dua-duanya.

## C4 · Interrupt

Tiga tingkat, semua memakai `pool.Kill`/`KillAgent`:

| Aksi | Di mana | Efek | Jalur |
|---|---|---|---|
| **Stop** sub-agent | baris panel | 1 anak berhenti, **partial** balik ke leader | `pool.KillAgent(child, agent)` |
| **Stop all** | header panel | semua anak root berhenti, leader lanjut | loop per `root_id` |
| **Kill** leader | header conversation (existing) | seluruh pohon (**cascade baru**) | `/kill` + cascade |

### Hasil partial WAJIB dikembalikan, bukan error

Leader yang menerima error cenderung **retry buta** — user klik Stop, agent malah
mulai ulang. Preseden produksi: stoa `claude-session.js:171-185` — `abort()`
**resolve** (bukan reject) dengan `{content, aborted:true}`, sehingga semua
konsumen hilir memperlakukannya sebagai penyelesaian normal berisi partial.

Bentuk tool result: lihat [§B2](#b2--mcp-surface) (`status:"interrupted"`).

### Tiga kasus yang wajib dibedakan

1. **`running`** → `KillAgent` + partial + status `interrupted`.
2. **`queued`** (belum dapat slot `max_parallel`) → **keluarkan dari antrean** +
   hasil `(cancelled before processing)`. **Bukan** `KillAgent` — prosesnya belum
   ada. Tanpa cabang ini tombol Stop **diam-diam no-op** (bukti: stoa
   `stoa.js:479-490`; multica malah menyembunyikan tombolnya untuk state ini,
   `activity-tab.tsx:528-533`).
3. **Sudah terminal** (race: anak selesai tepat saat klik) → **409**, UI tampilkan
   hasil sukses apa adanya. Jangan menimpa hasil sukses dengan `interrupted`.

### Idempotensi: guard di penyimpanan, bukan di handler

Tulis status hanya dengan `WHERE status = <yang diharapkan>` → completion
terlambat untuk row yang sudah `interrupted` (dan sebaliknya) jadi **no-op secara
konstruksi**, bukan karena pemeriksaan defensif yang bisa lupa ditulis di satu
jalur. Pola multica `agent.sql:701`. 409 tetap dikembalikan ke UI, tapi
kebenarannya tak bergantung handler.

### Status `interrupted` terpisah

Beda dari `stopped_max_turns`/`stopped_budget` (governor) dan `failed` (error).
Ini **manusia** yang menghentikan.

⚠️ **Cegah enum drift.** multica sudah kena: DB izinkan 8 status, TS daftar 7,
`deferred` ter-render "Queued" (`activity-tab.tsx:518`). Generate union TS dari
CHECK DB, atau tambah test yang membandingkan kedua daftar.

### Cascade dan budget: dua perlakuan berbeda (disengaja)

- **Stop manual** = **cascade** (user minta berhenti = berhenti semua).
- **Budget habis** = **biarkan yang jalan selesai**, tolak yang baru.

Jangan satukan dua jalur ini.

### Persist-then-kill

Simpan transcript/partial **sebelum** kill; gagal simpan tak boleh memblokir
teardown (pola routa `disconnect/route.ts:38-43`). Lebih kuat lagi: multica
stream transcript ke DB tiap 500ms (`daemon.go:5874`) sehingga partial durable
tanpa bergantung jalur cancel sama sekali.

**Laporkan usage sebelum cek cancel** — multica `daemon.go:3783-3790`, komentarnya
eksplisit: melewatkan ini *"silently under-reports billing"*. Relevan saat Fase 6.

## C5 · Scoping config

Pertanyaan "config agent per apa — project, provider, atau global?" → **berlapis**,
dan wick sudah punya lapisannya (`project.Defaults`, `sessionoverride`).

| Level | Isi | Siapa | Kenapa di situ |
|---|---|---|---|
| **Global** | `max_depth`, budget/root, `max_parallel`, cap `max_turns` | admin | guardrail — user **tak boleh** menaikkan |
| **Profil** ⭐ | provider, model, system_prompt, `default_max_turns`, tag ACL, `allow_take_over` | admin | **identitas role** — reusable lintas project & conversation |
| **Project** | roster profil aktif + override model | pemilik project | tim/repo beda butuh roster beda; `project.Defaults` sudah pola ini |
| **Session** | override sekali pakai | user | eksperimen tanpa mengubah profil |

Presedensi: session > project > profil > global-cap (global selalu **plafon**).

❌ **Bukan per-provider.** Provider adalah pilihan *di dalam* profil ("researcher
pakai claude"). Kalau config per-provider: satu role tak bisa punya dua varian
model, dan pindah provider = kehilangan seluruh setelan role. Bukti nyata: stoa
menaruh model di **room** (`schema.sqlite.sql:26`) → dua agent di satu room
dipaksa model sama, **melumpuhkan** premis "agent dengan role berbeda".

## C6 · Agent spawn lewat MCP

"AI bisa buat sub model sendiri pakai MCP kalau task-nya besar?" — bisa, tapi
bedakan dua hal:

| | Spawn ad-hoc | Bikin profil persisten |
|---|---|---|
| Apa | delegasi task + model, sekali pakai | menulis row `agent_profiles` |
| Tag/izin | **diwarisi** dari user pemicu, tak bisa menambah | agent menentukan sendiri → **privilege escalation** |
| v1 | ✅ izinkan | ❌ tunda |

Alasan: [§B4.1](#b41-pewarisan-hak--scoped-mcp-identity-enforcement-critical) tegas —
akses melekat ke user, profil hanya menyempit. Agent yang boleh menerbitkan
profil bisa membuat profil dengan tag lebih luas dari user pemicunya.

**Kill oleh agent:** leader boleh menghentikan **anaknya sendiri** (dia yang
mendelegasikan, dia yang tahu hasilnya cukup). **Tak boleh:** sibling, parent,
atau delegasi user lain. Interrupt oleh **manusia** jalur terpisah dan selalu menang.

Model bebas atau harus dari daftar? → [OQ](#open-questions).

## C7 · Fleet monitor (read-only)

Observability murni — consumer, tanpa infra baru.

| Data | Sumber |
|---|---|
| Daftar agent hidup | `pool.ActiveSnapshot()` `pool.go:1075-1102` |
| Status lifecycle | `ActiveEntry.Lifecycle` `state/state.go:47-78` |
| Substate | `ActiveEntry.Substate` `state/state.go:20-45` |
| Update live | SSE `Broadcaster` `stream.go:58-126` |
| Riwayat task | `agent_delegations` |

| Status UI | Aturan |
|---|---|
| 🟢 Running | `Lifecycle == working` |
| 🟡 Idle | `Lifecycle == idle` && PID hidup |
| ⚪ Spawning | `Lifecycle == spawning` |
| 🔴 Dead | `killed` \|\| PID==0 \|\| exit |

Endpoint: `GET /agents/monitor` (halaman), `/agents/monitor/snapshot` (JSON),
live via SSE `/stream` existing — **tak menambah hub**.

**Read-only = observe, dengan satu pengecualian disengaja:** **interrupt**
diizinkan (§C4). Menghentikan ≠ mengirim pesan. Take-over tetap ditahan.

**Cost/observability ringan** walau token-budget di-defer: tampilkan per-root
total turns, wall-clock, jumlah sub-agent. Murah, langsung kelihatan kalau ada
yang meledak.

## C8 · UI states

Detail visual: [`mockup.html`](mockup.html).

| State | Where |
|---|---|
| ① Profil list | `/manager/agents/profiles` — card per role, provider/model badge, enabled toggle |
| ② Profil editor | Meta + provider + model + system_prompt + tag picker + default_max_turns + can_delegate (auto-off untuk provider non-leader) |
| ③ **Kartu ringkas di thread** | `agents/session/{id}` — 1 baris per delegasi: badge profil + chip status + hasil truncate + "Open" |
| ④ **Rail panel** | tab "Sub-agents" — daftar tree per depth + Stop per baris + Stop all + transcript anak terpilih |
| ⑤ Fleet monitor | `/agents/monitor` — grid kartu, live SSE |
| ⑥ Settings governor (GLOBAL) | `/manager/agents/settings` — halaman terpisah |

**Design-system rules:** Inter (400/500/600), accent `green-500` `#27B199`,
page bg `white-200`/`dark:navy-800`, cards `white-100`/`dark:navy-700`, borders
`white-300`/`dark:navy-600`. Status chip pakai ramp status (**bukan** green untuk
success): running→`pos`, idle→`cau`, spawning→`prog`, dead/failed/interrupted→`neg`.
Depth indent 14px/level + garis `white-400`. Spacing 8-grid, cards `rounded-xl`,
chips `rounded-full`. Mobile-first.

---

# BAGIAN D — Implementasi

**Prasyarat:** Bagian B (tabel + `delegation.Run`) sudah mendarat. Bagian D ini
lapis rail-UI + interrupt di atasnya.

Semua path relatif `d:\code\work\wick`. Referensi `file:line` diverifikasi
2026-07-29.

Urutan: **D1 blocker** → D2 → … → D7.

## D1 · `pool.KillAgent` (blocker)

**Masalah.** `pool.go:1435-1453` menerima `agentName` tapi **tak pernah
memakainya** — memungut semua entry ber-prefix `sessionID::` lalu `Stop()` semua.

**Kerjakan.** Jangan ubah `Kill` (pemanggilnya mengandalkan semantik "seluruh
session" — itu yang cascade butuhkan). Tambah:

```go
// KillAgent stops exactly one agent inside a session, leaving its
// siblings running. Kill() remains "stop the whole session" — cascade
// and the existing /sessions/{id}/kill route depend on that.
//
// Returns ErrAgentNotActive when the key is absent: already finished,
// never spawned, or still queued. Callers MUST treat that as a distinct
// outcome, not an error — a queued sub-agent is cancelled by dropping it
// from the queue, not by killing a process that does not exist yet.
func (p *Pool) KillAgent(sessionID, agentName string) error

var ErrAgentNotActive = errors.New("agent not active in pool")
```

Implementasi: kunci `p.mu`, ambil `p.active[sessionID+"::"+agentName]`, **lepas
kunci**, lalu `e.agent.Stop()`. Jangan `Stop()` sambil memegang `p.mu` — `Kill`
yang ada pun melepas dulu, dan `Stop` memblokir sampai 5 detik (`agent.go:552`).

**Selesai kalau:** kill satu agent tak menyentuh sibling; agent tak ada →
`ErrAgentNotActive`; `Kill(sess, "")` masih mematikan seluruh sesi.

## D2 · `delegation.Interrupt`

File baru `internal/agents/delegation/interrupt.go`:

```go
type InterruptOutcome string

const (
	OutcomeKilled      InterruptOutcome = "killed"       // proses dihentikan, ada partial
	OutcomeDequeued    InterruptOutcome = "dequeued"     // belum mulai; keluar antrean (§C4)
	OutcomeAlreadyDone InterruptOutcome = "already_done" // race → HTTP 409
)

// Interrupt stops one delegation on human request.
// Idempotent by construction: the status write is guarded on the expected
// prior status, so a concurrent completion wins and this returns
// OutcomeAlreadyDone rather than overwriting a successful result.
func Interrupt(ctx context.Context, delegationID, actorID string) (InterruptOutcome, error)
```

**Urutan operasi — persist SEBELUM kill:**

```
1. Load row. Tak ada → not-found.
2. ACL: actorID == triggered_by ATAU admin. Gagal → forbidden.
3. Status sudah terminal → return OutcomeAlreadyDone (jangan tulis apa pun).
4. Status == "queued":
     keluarkan dari antrean → tulis "interrupted" +
     result "(cancelled before processing)" → OutcomeDequeued.
5. Status == "running":
     a. Ambil teks partial dari store sub-agent      ← SEBELUM kill
     b. Flush store. Gagal flush TIDAK memblokir kill — log lalu lanjut.
     c. pool.KillAgent(childSessionID, agentName)
        ErrAgentNotActive → jangan gagal; proses sudah mati sendiri.
        Tetap tulis status dengan partial yang sudah diambil.
     d. UPDATE … SET status='interrupted', result=?
          WHERE id=? AND status='running'
        0 row → completion menang → OutcomeAlreadyDone
     → OutcomeKilled
```

Langkah 3 dan guard 5d adalah dua lapis untuk race yang sama; **keduanya wajib**.
Langkah 3 optimisasi (hindari kill sia-sia), 5d yang menjamin kebenaran.

**Ubah `run.go`:** `delegation.Run` yang sedang blocking harus unblock dan
mengembalikan **tool result**, bukan error. Bentuk persis: [§B2](#b2--mcp-surface).
Baris `Do NOT silently retry.` sengaja ada — tanpa itu sebagian model tetap
mencoba ulang.

**Selesai kalau:** anak `running` → status `interrupted` + partial sampai ke
leader; anak `queued` → `OutcomeDequeued` tanpa `KillAgent`; interrupt 2× → yang
kedua 409 dan result pertama utuh; non-pemilik → forbidden.

## D3 · Sembunyikan child session

1. `session.Meta` tambah field:

```go
// ParentSessionID links a sub-agent's isolated session back to the session
// that delegated it. Non-empty = this is a child; children are hidden from
// the conversation list and surfaced in the parent's Sub-agents rail panel.
//
// Relasi ini SATU-SATUNYA penanda "ini anak" — sengaja tidak ada field
// `type: subagent` (dua sumber kebenaran bisa bertentangan, §C3).
ParentSessionID string `json:"parent_session_id,omitempty"`
```

2. Di `apiSessionList` (`api_conversation.go:73`), buang yang `ParentSessionID`
   non-kosong — **sebelum** `sessionListCap` (`:86`).
3. `session.List` jangan disentuh.
4. `GET /sessions/<childID>` → `303` ke
   `/sessions/<parentID>?rail=subagents&sub=<childID>`.

**Selesai kalau:** sidebar tak menampilkan child; URL child mendarat di induk
dengan panel terbuka (bukan 404); reaper masih memproses child; cap 200 tak bisa
dihabiskan child.

## D4 · RailTab + panel

### Endpoint

```
GET  /api/sessions/{id}/subagents                 → daftar untuk panel + badge
POST /api/delegations/{delegationID}/interrupt    → stop satu
POST /api/sessions/{id}/subagents/interrupt-all   → stop semua anak
```

Daftarkan di `handler.go:260` bersama rute `/api/sessions` lain supaya
`sessionAccessMW` (`:218`) ikut berlaku.

### DTO

Ikuti pola `SessionListItem` (flat, string-typed, waktu RFC3339 string):

```go
type SubAgentItem struct {
	DelegationID   string `json:"delegation_id"`
	ChildSessionID string `json:"child_session_id"`
	ProfileKey     string `json:"profile_key"`
	Label          string `json:"label"`      // task, truncate 60 rune
	Status         string `json:"status"`
	Lifecycle      string `json:"lifecycle"`  // dari pool.ActiveSnapshot(); "" kalau tak aktif
	Depth          int    `json:"depth"`
	TurnsUsed      int    `json:"turns_used"`
	MaxTurns       int    `json:"max_turns"`
	Detached       bool   `json:"detached,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
}
```

`Lifecycle` digabung dari `pool.ActiveSnapshot()` — pola sama sudah dipakai
`apiSessionList` (`:94-101`). Jangan bikin sumber status baru.

| Outcome | HTTP |
|---|---|
| killed / dequeued | 200 `{"outcome":"…"}` |
| already_done | **409** |
| ACL gagal | 403 |
| tak ada | 404 |

### Wiring `DetailView.svelte` — 6 titik

| # | Baris | Perubahan |
|---|---|---|
| 1 | `:148` | `type RailTab = … \| "subagents"` |
| 2 | `:186` | `"panel:subagents": () => toggleRail("subagents")` |
| 3 | `:1016` | entry `railTabsAll` — taruh **paling atas** |
| 4 | `:1049-1052` | `railTabs` filter juga `"subagents"` kalau `subAgents.length === 0` |
| 5 | `:1053-1056` | `$effect` auto-close saat tab hilang |
| 6 | `:1070` | `railCount` += cabang `"subagents"` |

Titik 4–5 = **pola tab Browser yang sudah ada**; ikuti persis, termasuk `$effect`
— tanpa itu panel bisa yatim saat anak terakhir selesai.

### `api/subagents.ts` (baru)

Cermin `api/processes.ts` (Effect + `apiGetE`/`apiPostE`):

```ts
export const getSubAgents = (base: string, id: string) => …
export const interruptSubAgent = (base: string, delegationId: string) => …
export const interruptAllSubAgents = (base: string, sessionId: string) => …

// Sub-agent selesai tak boleh menaikkan badge — kalau tidak badge nyangkut
// setelah semua anak beres. Pola liveProcesses() yang membuang kind==="idle".
export const liveSubAgents = (subs: SubAgentItem[]): SubAgentItem[] =>
  subs.filter((s) => s.status === "queued" || s.status === "running");
```

`railCount` pakai `liveSubAgents(...).length`; `railTabs` pakai **total**.

`interruptSubAgent` **harus** memperlakukan 409 sebagai hasil normal
(`already_done`), bukan toast error.

**Refetch:** ikuti `scheduleProcessReload` (`:481`) — satu fetch ~200ms setelah
transisi terakhir dalam burst. Mount + pasca-interrupt tetap langsung. Reuse SSE
`/stream`, **jangan** hub baru.

### `SubAgentPanel.svelte` (baru)

Props gaya `ProcessPanel` (data + callback, tanpa fetch di dalam):

```ts
type Props = {
  subAgents: SubAgentItem[];
  selectedId: string | null;
  onSelect: (childSessionId: string) => void;
  onInterrupt: (delegationId: string) => void;
  onInterruptAll: () => void;
};
```

Struktur: header (`Sub-agents` + `Stop all` kalau ada yang hidup) → daftar baris
→ transcript anak terpilih. Indent per `depth` (14px/level, border-left). Tombol
Stop untuk `queued` **dan** `running` — jangan cuma `running` (multica melakukan
itu dan menghasilkan task yang tak bisa dibatalkan dari UI).

Peta warna status: pakai `lifecycleCls`/`lifecycleDotCls` dari `ProcessPanel` —
ekstrak ke helper bersama, jangan salin dua peta yang bisa menyimpang.

⚠️ **Belum diverifikasi** (cek sebelum mulai): apakah `ConversationThread` punya
dependensi implisit ke state `DetailView` (file context, approvals, override
popover). Kalau ada → prop-drilling atau varian read-only. Ini menentukan effort;
kalau berat, kirim D4 dengan daftar + Stop dulu, transcript menyusul.

**Selesai kalau:** tab muncul hanya saat ada sub-agent dan hilang (panel tertutup)
saat nol; badge kosong saat semua selesai tapi tab tetap ada; Stop pada `queued`
tak error; 409 tak memunculkan toast; burst 20 event SSE → 1 fetch.

## D5 · Kartu ringkas di thread

Di `ConversationThread.svelte`, event `tool_use` bernama `wick_delegate`
di-render **satu baris**:

```
[badge profil] [chip status · n turn] <hasil 1-baris, truncate> [Open]
```

`Open` → `toggleRail("subagents")` + select anak itu.

Pertahankan kartu ini walau transcript pindah ke panel — transcript leader harus
tetap jadi catatan audit utuh ("di sini dia delegasi, ini hasilnya").

## D6 · Cascade kill + Stop all

`killAgent` (rute `/sessions/{id}/kill`, `handler.go:234`) harus juga
menghentikan anak — tanpa ini sub-agent jadi orphan, terutama async Fase 2 yang
**detached by design**.

```
POST /sessions/{id}/kill
  → kumpulkan delegasi aktif dengan root/parent = id (semua depth)
  → Interrupt tiap anak (deepest-first, supaya cucu mati sebelum induknya)
  → pool.Kill(id, "")   ← semantik "seluruh session" yang lama
```

`Stop all` = bagian anak saja, tanpa mematikan leader.

**Selesai kalau:** kill leader dengan 3 anak (depth 1 dan 2) → tak ada proses di
`ActiveSnapshot()`, semua delegasi terminal.

## D7 · Refactor surface

### Baru

| File | Isi |
|---|---|
| `internal/agents/delegation/` | `delegator.go` (Run + governor), `profile.go` (CRUD), `interrupt.go` (D2), `monitor.go` (snapshot+history) |
| `internal/entity/agent_profile.go`, `agent_delegation.go` | structs |
| `internal/entity/migrations/NNNN_multi_agent.go` | 2 tabel + index |
| `internal/mcp/handlers/delegation.go` | `WickDelegate`, `WickAgents` |
| `fe/.../lib/api/subagents.ts` | fetch + interrupt + `liveSubAgents` |
| `fe/.../lib/components/SubAgentPanel.svelte` | daftar + Stop + transcript |

### Diubah

| File | Perubahan |
|---|---|
| `internal/agents/pool/pool.go` | + `KillAgent`, + `ErrAgentNotActive` (D1). Mungkin helper spawn-with-system-prompt |
| `internal/agents/delegation/run.go` | unblock interrupted → tool result partial |
| `internal/agents/session/session.go` | + `Meta.ParentSessionID` |
| `internal/tools/agents/api_conversation.go` | filter child **sebelum** cap |
| `internal/tools/agents/handler.go` | 3 rute baru; cascade di `killAgent`; redirect URL child |
| `internal/mcp/handler.go` | descriptors di `handleToolsList`; branch `handleToolsCall` (+ SSE path) |
| `internal/tags/service.go` | auto-create `agent:<key>` filter tag — tiru `CreateOwnerTag` |
| `fe/.../components/DetailView.svelte` | 6 titik (D4) |
| `fe/.../components/ConversationThread.svelte` | kartu ringkas + Open |
| `fe/.../types/agents.ts` | + `SubAgentItem` |
| `internal/manager/` | `/manager/agents/profiles*`, `/manager/agents/settings` + views |
| `internal/tools/agents/` | `/agents/monitor*` |
| `internal/agents/event` | **no change** — reuse `Done` |

## D8 · Catatan teardown proses

`agent.Stop()` → `terminateProc` (`provider/agent.go:542`) melakukan
**hard-kill** (`proc.Kill()`); timer 5 detik di `:552` **bukan** grace period —
itu batas menunggu *reader goroutine* keluar *setelah* kill, lewat batas cuma
`log.Warn`. Tak ada SIGTERM. Dan `grep Setpgid` di `internal/agents/` **nol
hasil** → tak ada process group, cucu proses (MCP server, tool subprocess) bisa
orphan.

Pembanding: multica SIGTERM → 5s → SIGKILL ke **process group**
(`pkg/agent/claude.go:186-213`, `proc_other.go:16-42`); komentar `:201-207`
mendokumentasikan bug yang mereka perbaiki — eskalasi yang dikunci ke "leader
sudah exit" membocorkan descendant yang mengabaikan SIGTERM. routa sama
(`acp-process.ts:429-445`).

**Ini gap nyata, tapi DI LUAR scope Bagian D** — jangan gabung perubahan teardown
proses ke PR fitur UI ini. Catat isu terpisah. Untuk provider `wick` (in-process)
yang relevan bukan sinyal proses melainkan cancel `context` + reap `bgRegistry`.

## D9 · Uji yang wajib

| Area | Kasus |
|---|---|
| `KillAgent` | dua agent satu sesi → kill satu, sibling hidup; agent tak ada → `ErrAgentNotActive`; `Kill` lama tanpa regresi |
| Race interrupt | selesai tepat saat interrupt → hasil sukses menang, `OutcomeAlreadyDone` |
| Queued vs running | queued → dequeued tanpa `KillAgent`; running → killed + partial |
| Partial | interrupt di tengah → partial non-kosong sampai ke tool result leader |
| Idempotensi | interrupt 2× → kedua 409, result pertama utuh |
| ACL | non-pemilik non-admin → 403 |
| Sembunyikan child | `apiSessionList` tak memuat child; `session.List` **memuat** |
| Cap | 200 child tak memotong induk yang sah |
| URL child | `/sessions/<child>` → redirect, bukan 404 |
| Cascade | kill leader → semua keturunan terminal |
| Badge | semua anak selesai → badge kosong, tab tetap ada |
| Enum | daftar status Go dan TS identik |
| Governor | depth/budget/cycle/max_parallel table-driven; turn-counter (Done→Kill) |
| Provider-agnostik | fake provider tanpa `--max-turns` → Done-count+Kill berhenti tepat |
| Integration | delegate end-to-end; nested 2-level; budget-exceeded; parallel 3 |
| Security | sub-agent tak bisa akses tool di luar tag; monitor ACL non-admin |

## D10 · Acceptance gate

- [ ] `delegation` package: `Run` sinkron + governor lengkap
- [ ] Migration `agent_profiles` + `agent_delegations` + index
- [ ] `max_turns` provider-agnostik — verified pada provider tanpa flag native
- [ ] Parallel — multiple `wick_delegate` konkuren, dibatasi `max_parallel`
- [ ] MCP `wick_delegate` + `wick_agents` di `tools/list`, dispatch JSON + SSE, ACL server-side
- [ ] Profil editor lengkap (`can_delegate` auto-off untuk provider non-leader)
- [ ] Sub-agent least-privilege sesuai `allowed_tag_ids` — **termasuk token MCP ber-scope** (§B4.1 #2)
- [ ] **`pool.KillAgent`** + `ErrAgentNotActive` (D1)
- [ ] **Interrupt 3 tingkat** + partial ke leader + guard idempotensi (D2)
- [ ] **Child session hidden** + redirect URL (D3)
- [ ] **Rail tab + panel** + badge live-only + coalesced refetch (D4)
- [ ] **Kartu ringkas** di thread (D5)
- [ ] **Cascade kill** (D6)
- [ ] Fleet monitor live (SSE) + detail transcript read-only
- [ ] Settings governor page + kill-switch global
- [ ] Tags auto-create `agent:<key>`
- [ ] Tests pass (D9)
- [ ] Docs: `docs/guide/sub-agents.md` + sidebar; design.md + mockup.html sinkron

## D11 · De-risk sebelum mulai

Buktikan 3 unknown dengan spike throwaway 1–2 jam:

1. Inject `system_prompt` per child di pool — ada jalurnya atau perlu helper?
2. Spawn-with-cwd untuk worktree (Fase 3).
3. Gemini sebagai leader — MCP tool-use didukung?

Plus: **mulai dari satu vertical slice konkret** sebagai acceptance Fase 1 (mis.
"orchestrator delegasi code-reviewer di thread Slack") supaya tidak over-build.

---

# BAGIAN E — Prior art

Analisis kode tiga repo playground, bukti `file:line`. Ringkas; yang penting
sudah diserap ke Bagian A–D di atas.

| Repo | Stack | Bentuk | Kedewasaan |
|---|---|---|---|
| **multica** | Go + Next/Electron | Task-board Postgres + daemon spawn CLI | **Produksi** — 234 migrasi, ~46k baris `pkg/agent/` |
| **routa** | Rust + TS | Kanban lane = trigger otomasi | Matang di kanban, in-memory di queue |
| **stoa** | Node.js | Chatroom, agent = WS client | Berjalan, banyak sudut setengah jadi |

## E1 · multica

**Diserap:** guard transisi di WHERE clause (`agent.sql:508-517`, `:701`) →
[§C4](#c4--interrupt); slot-before-claim (`daemon.go:3443-3459`); stream
transcript 500ms (`daemon.go:5874`) + usage sebelum cancel (`:3783-3790`);
SIGTERM→grace→SIGKILL group-wide (`claude.go:186-213`) → [§D8](#d8--catatan-teardown-proses);
pisah identitas authz vs audit + fail-closed (`task.go:600-614`).

**Ditolak:** cancel lewat **polling 5 detik** (`daemon.go:3637-3684`, `:459`) —
server flip row DB, daemon *menemukan* lewat ticker padahal WS sudah terbuka;
worst case ~10 detik agent terus membakar token. Wick in-process → panggil langsung.

⚠️ **Nesting tanpa depth limit** — tak ada kolom `depth`/`root_id`, grep
`max_depth|recursion` **nol hasil**. Keamanan siklus ditangani di lapis atribusi
(*copy, don't chain*, `attribution.go:39`) tapi **tak pernah di lapis eksekusi**.
Mengonfirmasi [§B3](#b3--governor) wick benar.

⚠️ **Enum drift sudah live** — DB 8 status, TS 7, `deferred` render "Queued".

## E2 · routa

**Diserap:** persist-then-kill (`disconnect/route.ts:38-43`); watchdog
**memancarkan event** bukan bertindak (`workflow-orchestrator.ts:612-625`) +
`lastMeaningfulActivityAt` (`:586-588`); **resolution chain berprioritas**
(`specialist-db-loader.ts:54-105`) → [§B1](#b1--storage).

**Ditolak:** cancel yang **cuma menulis status tanpa membunuh proses**
(`background-tasks/[id]/route.ts:92-94`) — proses ACP terus jalan; `force=true`
malah hard-delete row = menghancurkan audit. Kapabilitasnya ada
(`canvas/specialist/route.ts:233`), cuma tak di-wire. **Pelajaran: setiap
transisi state yang meninggalkan pekerjaan wajib menyentuh process manager.**

**Ditolak:** queue state murni in-process (`kanban-session-queue.ts:32-33`) —
limit per-board hilang begitu ada replika kedua.

## E3 · stoa

**Diserap (model interrupt terbaik dari ketiganya):** partial-as-resolve
(`claude-session.js:171-185`); **bedakan queued vs running**
(`stoa.js:479-490`); **cancel the sequence** via token `{cancelled}` diperiksa
sebelum & sesudah tiap turn (`server.js:2504-2518`, `:3609`, `:3614`); tiga
orphan reaper idempoten (`server.js:2600`, `:3457`, `:4274`); dua tingkat timeout
(`stoa.js:903-905`).

**Ditolak:** model di **room** bukan profil (`schema.sqlite.sql:26`) →
[§C5](#c5--scoping-config); session pool di-key by **workdir** bukan agent
(`stoa.js:291-302`) — dua participant berbagi direktori diam-diam berbagi satu
proses; **shuffle acak** saat tak ada @mention (`server.js:3568-3573`) →
non-deterministik; `--dangerously-skip-permissions` sebagai default
(`claude-session.js:35`); dua setting mati (`max_active_rooms_per_ai`,
`rooms.max_ai_turns`).

## E4 · Perbandingan interrupt

| | stoa | multica | routa | **wick (usulan)** |
|---|---|---|---|---|
| Jalur | WS 3-hop, **push** | **polling 5s** | JSON-RPC cancel | **in-process `KillAgent`** |
| Latensi Stop→mati | ~instan | **~10s** | ~instan | ~instan |
| Partial work | **disimpan** | **disimpan** (500ms) | disimpan | **disimpan** → tool result |
| Bentuk ke caller | resolve `{aborted}` | result dibuang | — | **tool result partial** |
| Queued vs running | **dibedakan** | — | — | **wajib dibedakan** |
| Grace period | ❌ | ✅ 5s group-wide | ✅ 5s | ⏸ (§D8) |
| Batalkan fan-out | ✅ token | — | — | **Stop all** |

## E5 · Kanban (untuk nanti)

Hanya **routa** memakai kanban sebagai mesin orkestrasi. multica punya kanban
untuk **issue** (bukan agent-task); stoa tak punya.

Kalau kanban masuk wick (Fase 8), ikut model routa:

- **Stage discriminator dipisah dari column id** (`kanban.ts:134`, `:349-358`) —
  user bebas rename/urut kolom, state machine mengunci ke `stage`. Menghindari
  `if (columnId === "dev")` bertebaran.
- **Kolom = trigger otomasi** (`docs/adr/0004:18`).
- **Policy-as-data + SATU evaluator** dipakai REST **dan** MCP
  (`docs/adr/0007:17`) — alasannya persis relevan: **agent memanggil MCP
  langsung**, jadi guard yang cuma ada di jalur UI akan dilewati agent.
- **Evidence-gated completion** (`kanban.ts:13-16`) — board bisa mewajibkan
  `verification_report`, bukan percaya klaim "turn complete" agent.
- **`gateMode: blocking | warning`** — soft-launch.
- **Jangan** ulangi enum drift multica: generate union TS dari CHECK DB.

## E6 · Yang tak perlu ditiru

- **18 wrapper CLI per-backend** (multica ~46k baris) dengan tabel kapabilitas
  manual. Wick punya 3 adapter — cukup.
- **Windows kelas dua** (multica `proc_windows.go:39-56`): tanpa process group,
  tanpa grace, `codexInitializeRetrySupported()` = false. Wick target utamanya
  Windows — ini peringatan, bukan contoh.
- **Budget token: tak satu pun punya.** multica ukur → billing saja; routa rekam
  `inputTokens`/`outputTokens` tapi grep `costLimit|tokenLimit` **nol hasil**,
  padahal mode `ralph_loop` + retry = jalur belanja tak terbatas. **Argumen
  menaikkan prioritas Fase 6**, bukan menurunkan.

---

# BAGIAN F — Lampiran: profil `image`

Pertanyaan: *"lebih bagus ada sub-agent khusus image, ngak sih?"* → **Ya, tapi
sebagai profil — bukan pengganti tool.** Dua lapis, dua-duanya perlu:

| | tool `generate_image` | sub-agent profil `image` |
|---|---|---|
| Isi | satu HTTP call ke vendor | agent: system prompt + akses tool |
| Kerja | prompt → PNG | susun prompt, generate, **nilai hasil**, ulangi |
| Hasil jelek | tak tahu (tak melihat output) | bisa evaluasi + retry |
| Biaya | 1 image call | banyak turn × banyak image call |
| Dependensi | — | **butuh** `generate_image` sebagai tangannya |

**Urutan terkunci:** [wick-image-gen](../wick-image-gen/plan.md) Fase 1 (tool) →
Fase 1 dokumen ini (delegation) → profil `image` = **satu row `agent_profiles`,
nol kode baru**:

```
key: "image"          provider: <yang dukung MCP tool-use>
system_prompt:        "You generate images. Draft a precise prompt, call
                       generate_image, evaluate the result against the request,
                       and iterate at most N times. Return the best images as
                       an imagecard fence."
allowed_tag_ids:      [tag yang memuat generate_image]   ← least-privilege
default_max_turns:    6      (iterasi visual boros turn)
default_mode:         sync   (user menunggu gambarnya)
allow_take_over:      true   ← satu-satunya field baru (⏸ ditahan)
```

**Kenapa profil ini pembenaran terkuat untuk rail + interrupt:**

- **Boros turn** — generate → nilai → ulang. Nested langsung banjir.
- **Output visual per turn** — main cuma perlu hasil final; percobaan tengah
  adalah kebisingan yang tepat untuk panel rail.
- **Sering dihentikan di tengah** — "udah, yang kedua aja" = interaksi **normal**,
  bukan kasus runaway. Ini yang menutup pertanyaan lama "kill manual perlu tidak".
- **Butuh koreksi manusia** — "yang ketiga tapi lebih gelap" → menggeser usulan
  take-over ke per-profil.
- **Guardrail biaya berlapis** — `default_max_turns` (profil) × cap `n` (tool).

**Ditolak:** profil `image` yang memanggil vendor **langsung** tanpa lewat tool —
duplikat key management, dan sub-agent tak bisa dibatasi lewat tag ACL.

---

# Open questions

Butuh keputusan sebelum/selama implementasi.

**Blocking untuk Fase 1:**

1. **Gemini sebagai leader** — gemini CLI versi wick dukung MCP tool-use? Kalau
   belum: gemini = sub-agent only di v1. **Perlu verifikasi** (D11).
2. **Approval/AskUser dari sub-agent muncul di mana?** Di panel rail (bisa
   terlewat kalau rail tertutup) atau naik ke level session (mengganggu main
   thread)? Usul: **naik ke session** + badge di tab — approval terlewat
   memblokir sub-agent tanpa jejak jelas.
3. **`ConversationThread` reusable read-only?** Belum diverifikasi (D4).
   Menentukan effort panel.
4. **System prompt injection per child** — pool sudah punya jalurnya atau perlu
   helper baru? (D11)

**Bisa diputuskan sambil jalan:**

5. **Roster profil per-project wajib atau opsional?** Kalau project tak
   menetapkan roster: leader pakai **semua** profil yang lolos tag, atau **tak
   ada** sampai di-set? Usul: semua yang lolos tag (opt-out) supaya langsung jalan.
6. **Spawn ad-hoc: model bebas atau dari daftar?** Kalau bebas, agent bisa pilih
   model termahal. Usul: batasi ke model terdaftar + tunduk cap global.
7. **Async detached (Fase 2) tampil di rail session mana?** Usul: tetap di
   conversation asal dengan chip `detached`. Konsekuensi: rail harus bisa
   menampilkan anak walau parent sudah idle/killed.
8. **Default angka governor** — `max_depth=3`, budget/root=40, `max_parallel=4`,
   `default_max_turns=12` — masuk akal?
9. **Default mode per profil** (Fase 2) — researcher/reporter `async`,
   coder/reviewer `sync`?
10. **Take-over** — (a) read-only v1, (b) per-profil `allow_take_over` + tandai
    `user_steered` *(usulan setelah §F)*, atau (c) sub-agent lepas jadi
    conversation mandiri?
11. **Kontrak kegagalan + retry** — apa yang leader terima saat sub-agent
    `failed`/`stopped_*`, dan boleh apa (retry/lanjut/abort)?
12. **Workspace non-git** (Fase 3) — `worktree` fallback ke `shared`+warning atau
    salin direktori? Auto-merge ke parent atau kembalikan diff?
13. **Allowlist tool native per-provider** — verifikasi semantik `--allowedTools`/
    `--strict-mcp-config` di codex/gemini.
14. **Squad eksplisit fase berapa?** Usul: Fase 5.

---

# Rejected alternatives

**Arsitektur:**

- **Task-board asinkron penuh (multica)** — beda paradigma dari "leader nunggu
  hasil"; jauh lebih besar. v1 sinkron dulu; board bisa Fase 7 di atas
  `agent_delegations`.
- **Chatroom multi-agent (stoa)** — butuh injeksi konteks antar-agent +
  turn-governor room. Use-case beda (diskusi, bukan delegasi tugas).
- **Token-budget di v1** — event ter-normalisasi tak bawa usage
  (`types.go:76-86`). Turn-count cukup sebagai rem v1. (Tapi lihat
  [§E6](#e6--yang-tak-perlu-ditiru) — prioritas Fase 6 naik.)
- **Streaming hasil parsial sub-agent ke leader** — leader terima hasil akhir
  saja. Progress terlihat di panel/monitor. Streaming ke konteks leader =
  kompleksitas + token bleed.
- **Sub-agent provider baru / runtime plugin** — sub-agent = provider existing.
- **Session id sub-agent predictable** — child pakai id baru unik (bukan
  thread_ts) supaya tak bisa di-spoof.
- **File sebagai jalur komunikasi** — leader↔sub-agent lewat pipe + MCP
  in-memory; file JSON hanya state/audit. "File kanal" = IPC redundan + race.
- **Config per-provider** — provider adalah pilihan *di dalam* profil ([§C5](#c5--scoping-config)).
- **Agent menerbitkan profil persisten** — privilege escalation ([§C6](#c6--agent-spawn-lewat-mcp)).

**UI:**

- **Nested penuh di transcript** (rencana awal) — main thread banjir, tak ada
  tempat untuk tombol interrupt. Diganti [Bagian C](#bagian-c--ui--interrupt).
- **Satu rail tab per sub-agent** — rail habis ruang. Satu tab + tree di panel.
- **Pane kiri / level-2 di `SessionList`** — sidebar kiri adalah daftar
  *conversation*, dan sub-agent justru **disembunyikan** dari daftar itu.
- **Tab per anak, bukan breadcrumb** — depth >1 dan jumlah anak tak terbatas.
- **Reuse `DetailView` utuh untuk panel** — rail bersarang di dalam rail.
- **Field `type: subagent` pada session** — `parent_session_id` sudah cukup.
- **Sub-agent jadi conversation mandiri di daftar utama** — mengaburkan ownership.
- **Menghapus kartu ringkas dari thread** — transcript leader harus tetap jadi
  catatan audit utuh.

**Interrupt:**

- **Interrupt = error ke leader** — memicu retry buta.
- **Cancel lewat polling** — multica ~10s. Wick in-process.
- **Cancel yang cuma menulis status** — routa; proses terus jalan.
- **Melupakan cabang queued** — tombol Stop yang diam-diam no-op.
- **Hub SSE baru** — `/stream` sudah menyiarkan lifecycle.
- **Menambah knob governor tanpa kode yang membacanya** — stoa punya dua setting mati.

## Boundary — kapan butuh bus nyata

v1 mengasumsikan **satu daemon wick**: leader + semua sub-agent di proses yang
sama, jadi pipe lokal + MCP loopback cukup. **Kalau** sub-agent di-distribusi ke
mesin lain (model daemon-terhubung ala stoa/multica), butuh **message bus** +
auth antar-node. Di luar scope v1; `agent_delegations` menyediakan record durable
yang bisa jadi fondasinya.

⚠️ Dan itu **sumber kerumitan terbesar** di dua repo pembanding: multica ~46k
baris untuk 18 wrapper CLI, Windows jadi jalur kelas dua. Wick in-process =
keunggulan nyata. **Jangan tergoda daemon sebelum benar-benar butuh multi-host.**

---

# Threat model

1. **Pewarisan hak** — sub-agent ≤ akses user pemicu (tag di-intersect), dan
   identitas MCP sub-agent **ber-scope, bukan `MCPToken` admin**
   ([§B4.1](#b41-pewarisan-hak--scoped-mcp-identity-enforcement-critical)).
   Vektor yang harus diuji: prompt-injection bikin leader spawn banyak sub-agent,
   atau role ber-tag luas jadi jalur exfil.
2. **Interrupt authorization** — hanya `triggered_by` atau admin. Leader boleh
   menghentikan anaknya sendiri, bukan sibling/parent/delegasi user lain.
3. **Runaway spawn** — `max_depth` + cycle-guard + `max_parallel` + budget/root.
   multica tak punya depth limit dan itu jalur nyata ke spawn tak terhingga.
4. **Kill-switch global** untuk emergency stop + rilis bertahap.
