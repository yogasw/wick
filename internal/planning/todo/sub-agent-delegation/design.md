# Sub-Agent Delegation — Squad, Synchronous Task-Tool, Fleet Monitor (design)

Status: **proposal — not implemented**. Awaiting human sign-off on scope,
storage shape, governor model, and provider-compat fallbacks before any code
lands.
Update terakhir: 2026-06-10.

**Paradigm:** wick sekarang = **1 conversation → 1 active agent** yang spawn
CLI subprocess (`internal/agents/provider/*`). Konsep multi-agent persisten
per session **sudah ada** (`internal/agents/session/agents.go:19-30`
`AgentEntry` list + `pool.Send(sessionID, agentName)` di
`internal/agents/pool/pool.go:338-363`). Yang belum ada: cara satu agent
**memanggil** agent lain, governor turn lintas-agent, dan visualisasinya.

Desain ini menambah lapisan **delegasi sub-agent sinkron** (gaya Task-tool /
sub-agent Claude Code) di atas fondasi yang sudah ada:

1. **Profil agent reusable** — role (researcher, coder, reviewer) didefinisikan
   sekali, dipakai lintas session.
2. **MCP tool `wick_delegate`** — leader agent panggil tool, MENUNGGU, hasil
   akhir sub-agent balik sebagai tool result. Reuse `pool` yang ada untuk
   spawn sub-session terisolasi.
3. **Governor** — nested delegation + budget turn per-root + `max_turns` per
   sub-agent + `max_depth` + cycle-guard, di-enforce **di level wick** (bukan
   bergantung flag provider).
4. **Fleet monitor read-only** — lihat sub-agent mana yang running / idle /
   mati, sedang handle task apa, dan riwayat task — murni consumer dari
   `pool.ActiveSnapshot()` + SSE hub yang sudah ada.

Paired mockup: [`mockup.html`](mockup.html). Update keduanya barengan.

---

## Naming note (pilih sebelum implement)

| Konteks | Kandidat | Rekomendasi |
|---|---|---|
| Fitur (UI label) | "Sub-agents" / "Squad" / "Delegation" | **"Sub-agents"** di nav, "Squad" sebagai grup profil (fase-2) |
| Go package | `internal/agents/delegation/` | **`delegation`** — pisah dari `pool`, depends on `pool` |
| MCP tools | `wick_delegate`, `wick_agents` | `wick_delegate` (action) + `wick_agents` (list roster) |
| Tabel | `agent_profiles`, `agent_delegations` | kebab→snake, plural |

---

## TODO

**v1 locked decisions:**

- ✓ **Delegasi SINKRON** — leader panggil `wick_delegate(profile, task)`,
  blocks, terima hasil akhir sub-agent sebagai tool result. Bukan task-board
  asinkron (itu arah multica penuh — lihat §15 Rejected).
- ✓ **Profil reusable** — tabel `agent_profiles`. Role = provider + model +
  system_prompt + allowed_tag_ids + default_max_turns. Dipakai lintas session.
- ✓ **Sub-session terisolasi** — tiap delegasi spawn session baru, konteks
  bersih (hanya task prompt + roster), project/cwd sama dengan parent. Sub-agent
  TIDAK lihat history leader. Selesai → hasil dikembalikan → dibongkar.
- ✓ **Nested + budget** — sub-agent boleh delegasi lagi, dibatasi `max_depth`,
  budget turn global per-root, dan cycle-guard.
- ✓ **`max_turns` provider-agnostik** — di-enforce dengan **hitung `event.Done`
  dari stream ter-normalisasi** (`internal/agents/event/types.go:41-42`) +
  `pool.Kill` (`pool.go:1136-1153`). Flag CLI native (claude `--max-turns`)
  hanya optimisasi; counter wick = backstop universal.
- ✓ **Paralel** — leader boleh emit beberapa `wick_delegate` sekaligus; wick
  jalankan konkuren (pool sudah konkuren), dibatasi `max_parallel`.
- ✓ **Fleet monitor read-only** — observe-only; status running/idle/dead dari
  `pool.ActiveSnapshot()` (`pool.go:1075-1102`) + live via SSE `/stream`
  (`internal/tools/agents/handler.go:1734-1807`). Riwayat dari `agent_delegations`.
- ✓ **ACL via tag** — profil gated tag (sama pola connector
  `service.go::IsVisibleTo`). Create/edit profil = admin-only.
- ✓ **Audit seragam** — tiap delegasi tulis row `agent_delegations` (task,
  status, turns_used, result, depth, root_id).

**Deferred (out of v1 scope):**

- ⏸ **Token-budget** — event ter-normalisasi tidak bawa usage tokens
  (`types.go:76-86`). Butuh parse raw CLI usage di luar event path. v1 =
  turn-count only. Token cap menyusul.
- ⏸ **Squad eksplisit** — grup profil bernama dengan leader + member tetap.
  v1: leader boleh delegasi ke profil enabled mana pun yang lolos tag. Squad
  grouping (routing "leader yang pilih siapa") fase-2.
- ⏸ **Task-board asinkron** — enqueue/claim/start/complete ala multica. Beda
  paradigma (lihat §15). Bisa jadi fase-3 di atas `agent_delegations`.
- ⏸ **Streaming hasil parsial ke leader** — v1 leader terima hasil AKHIR saja
  (blocking). Progress sub-agent terlihat di monitor, bukan di-inject ke leader.
- ⏸ **Human-in-the-loop pada sub-agent** — approval gate per sub-agent. v1
  sub-agent warisi gate config parent.

---

## 1. Tujuan & non-goal

**Tujuan:**

- Satu agent bisa **mendelegasikan sub-tugas** ke agent lain dengan role
  berbeda, lalu lanjut dengan hasilnya — tanpa human switch agent manual.
- Pakai infrastruktur existing: `pool` (spawn + lifecycle + Kill), event model
  ter-normalisasi, SSE hub, tags ACL, MCP dispatch.
- Role reusable: definisikan "researcher" / "reviewer" sekali, panggil dari
  banyak conversation.
- Aman & terkendali: budget turn, depth limit, cycle-guard, semua di-enforce
  wick-side dan **independen dukungan provider**.
- Observability: operator bisa lihat realtime agent mana yang kerja, idle, atau
  mati, dan task apa yang sedang/sudah ditangani.

**Non-goal:**

- Bukan **task-board asinkron** (multica penuh) — v1 sinkron, leader nunggu.
- Bukan **chatroom multi-agent** (stoa) — sub-agent tidak saling ngobrol bebas;
  komunikasi cuma lewat hasil delegasi.
- Bukan **runtime plugin / provider baru** — sub-agent = provider existing
  (claude/codex/gemini) yang di-spawn pool.
- Bukan **scheduler** — delegasi dipicu leader saat runtime, bukan cron
  (Autopilot ala multica = di luar scope).

---

## 2. Konsep & terminologi

```
AgentProfile (role reusable)
├─ Key, Name, Description, Icon       — admin-set
├─ Provider     — "claude" | "codex" | "gemini"
├─ Model        — provider-specific model id
├─ SystemPrompt — role instruction
├─ AllowedTagIDs — tools/connectors yang boleh dipakai sub-agent (via tag)
├─ DefaultMaxTurns — budget turn default tiap delegasi ke role ini
└─ CanDelegate  — bool: role ini boleh jadi leader (panggil wick_delegate)?

Delegation (satu pemanggilan wick_delegate, runtime)
├─ RootID            — id delegasi paling atas (akar pohon)
├─ ParentSessionID   — session leader yang memanggil
├─ ProfileKey        — role yang di-spawn
├─ ChildSessionID    — session terisolasi sub-agent
├─ Task              — prompt tugas
├─ Depth             — 0=leader langsung, 1, 2, …
├─ Status            — running | done | failed | stopped_max_turns | stopped_budget
├─ TurnsUsed         — count event.Done sub-agent ini
└─ Result            — teks hasil akhir (atau error / partial)
```

| Term | Arti | Catatan |
|---|---|---|
| **Leader** | Agent yang memanggil `wick_delegate` | Harus provider yang dukung MCP tool-use (§7) |
| **Sub-agent** | Agent yang di-spawn untuk satu task | Provider apa pun yang bisa di-spawn pool |
| **Root delegation** | Delegasi level-0; akar pohon | Budget global dihitung per-root |
| **Isolated sub-session** | Session baru, konteks bersih, cwd sama | `pool` spawn dengan session id baru |
| **Fleet** | Seluruh agent hidup di proses wick | Leader persisten + sub-agent efemeral |

**Hubungan ke yang sudah ada:**

```
pool.Send(sessionID, agentName, …)   (existing — routing ke 1 agent)
session/agents.go AgentEntry[]       (existing — N named agent / session)
        │
        ▼  delegation menambah:
delegation.Run(ctx, parentSess, profileKey, task, depth, rootID)
  → resolve AgentProfile dari DB
  → cek governor (depth, budget, cycle)
  → spawn isolated child session via pool (provider+model+systemPrompt profil)
  → pool.Send(childSess, task) → tunggu event.Done terakhir
  → enforce max_turns: count Done; pool.Kill kalau lewat
  → tulis agent_delegations row
  → return Result ke caller (tool result)
```

---

## 3. Storage layout

### 3.1 Tables

```sql
-- one row per reusable agent role
agent_profiles (
  id                uuid primary key,
  key               text unique not null,        -- "researcher", "code-reviewer"
  name              text not null,               -- "Researcher"
  description       text,
  icon              text default '🤖',
  provider          text not null,               -- "claude" | "codex" | "gemini"
  model             text,                        -- provider model id; null = provider default
  system_prompt     text not null,               -- role instruction
  allowed_tag_ids   jsonb not null default '[]', -- tag IDs → tools/connectors sub-agent boleh pakai
  default_max_turns int  not null default 12,    -- budget turn default per delegasi
  can_delegate      boolean not null default false, -- boleh jadi leader (nested)?
  created_by        uuid not null,
  created_at        timestamptz,
  updated_at        timestamptz,
  disabled          boolean default false
)

-- one row per wick_delegate invocation (audit + monitor history)
agent_delegations (
  id                 uuid primary key,
  root_id            uuid not null,              -- akar pohon (self jika depth=0)
  parent_session_id  text not null,              -- session leader
  parent_agent       text not null,              -- agent name leader
  profile_key        text not null,              -- role yang dipanggil
  child_session_id   text not null,              -- session terisolasi sub-agent
  task               text not null,              -- prompt tugas (truncatable untuk display)
  depth              int  not null default 0,
  status             text not null,              -- running|done|failed|stopped_max_turns|stopped_budget
  turns_used         int  not null default 0,
  result             text,                       -- hasil akhir / error / partial
  error_msg          text,
  started_at         timestamptz not null,
  ended_at           timestamptz,
  triggered_by       uuid                        -- wick user pemilik root session (untuk ACL monitor)
)

-- index untuk monitor + budget accounting
create index idx_agent_delegations_root   on agent_delegations(root_id);
create index idx_agent_delegations_status on agent_delegations(status);
create index idx_agent_delegations_parent on agent_delegations(parent_session_id);
```

**Reuse existing — tidak ada perubahan skema:**

- `pool.active` map + `runEntry` (`pool.go:65,209-230`) — sumber status live.
- `internal/agents/event` — event ter-normalisasi untuk turn counting.
- SSE `Broadcaster` (`internal/tools/agents/stream.go:58-126`) — live monitor.
- `tags` / `tool_tags` / `user_tags` — ACL profil & monitor (pola connector).
- `sessions` — child session ikut layout session existing (project/cwd parent).

### 3.2 Profil → spawn config

`AgentProfile` dipetakan ke parameter spawn pool yang sudah ada:

| Profil | Param pool/agent | Catatan |
|---|---|---|
| `provider` + `model` | provider type/name + model | resolve ke factory pool existing |
| `system_prompt` | initial system context sub-session | disuntik saat session-start child |
| `allowed_tag_ids` | filter tools yang terlihat sub-agent | sub-agent dapat MCP allowlist sesuai tag |
| `default_max_turns` | governor counter (bukan hanya `--max-turns`) | lihat §6 |
| `can_delegate` | apakah `wick_delegate` masuk allowlist sub-agent | depth-guard tambahan |

---

## 4. MCP surface

Dua tool baru. **Pola sama dengan connector**: didaftarkan di
`internal/mcp/handlers`, muncul di `handleToolsList`, dispatch di
`handleToolsCall`. ACL server-side (bukan client-side allowlist).

### 4.1 `wick_agents` — daftar roster yang boleh dipanggil

```jsonc
// input: {} (atau {"include_disabled": false})
// output:
{
  "agents": [
    { "key": "researcher",   "name": "Researcher",   "description": "Web + docs research. Returns a cited summary.", "provider": "claude" },
    { "key": "code-reviewer","name": "Code Reviewer", "description": "Reviews a diff for bugs. Returns findings list.", "provider": "codex" }
  ]
}
```

Hanya profil enabled yang **lolos tag** caller yang muncul (gating via
`IsVisibleTo`-style, pola `internal/connectors/service.go`). Roster juga
**disuntik ke system context leader** saat spawn supaya leader tahu siapa yang
bisa dipanggil tanpa harus call tool dulu.

### 4.2 `wick_delegate` — delegasikan satu task (blocking)

```jsonc
// input:
{
  "profile": "researcher",          // required — key AgentProfile
  "task": "Cari changelog breaking di lib X versi 3→4, ringkas + sitasi.", // required
  "context": "Repo pakai X v3.2.",  // optional — konteks tambahan, bukan history penuh leader
  "max_turns": 8                    // optional — override default profil (≤ cap global)
}

// output (sukses):
{
  "profile": "researcher",
  "status": "done",
  "turns_used": 5,
  "result": "Breaking changes v3→v4: ...\nSitasi: ..."
}

// output (budget/turns habis):
{
  "profile": "researcher",
  "status": "stopped_max_turns",
  "turns_used": 8,
  "result": "<partial sejauh ini>",
  "note": "Sub-agent dihentikan saat mencapai max_turns=8. Hasil parsial."
}
```

**Paralel:** kalau leader emit beberapa block `wick_delegate` dalam satu turn,
wick jalankan **konkuren** (pool sudah konkuren) sampai cap `max_parallel`.
Tiap call balik hasil sendiri. Tidak ada tool batch khusus — paralelisme alami
dari multiple tool_use (sama seperti tool lain di MCP). *(Opsi sugar
`wick_delegate_many(tasks[])` dipertimbangkan, di-defer — redundan jika paralel
alami sudah jalan.)*

---

## 5. Delegation runtime

Package baru `internal/agents/delegation/`. Inti:

```go
// Run executes one synchronous delegation and returns the sub-agent's final
// result. Blocks until the sub-agent finishes, errors, or hits a budget cap.
func (d *Delegator) Run(ctx context.Context, in DelegateInput) (DelegateResult, error)
```

Alur `Run`:

1. **Resolve profil** dari `agent_profiles` by key. Error rapi kalau tidak
   ada / disabled / caller tak punya tag akses.
2. **Governor pre-check** (§6): depth ≤ `max_depth`? root budget masih ada?
   profil tidak ada di ancestor chain (cycle)? `max_parallel` belum penuh?
   Gagal → return error (bukan panic), status di-record.
3. **Insert `agent_delegations` row** status=`running`, `child_session_id`
   di-generate (session id baru, unik — BUKAN thread_ts).
4. **Spawn isolated child session** via pool: provider+model+system_prompt dari
   profil, cwd/project = parent, MCP allowlist sesuai `allowed_tag_ids`.
   `can_delegate=false` → tool `wick_delegate` TIDAK masuk allowlist child.
5. **Send task** (`pool.Send(childSession, task+context)`), pasang `onEvent`
   hook yang:
   - count `event.Done` (turn counter),
   - update `turns_used` + broadcast ke monitor (SSE),
   - saat counter == effective max_turns → `pool.Kill(childSession)`
     (`pool.go:1136`), status=`stopped_max_turns`.
6. **Tunggu terminal**: `event.Done` final (selesai normal) ATAU proses exit
   (EOF/kill) ATAU `event.Error`. Ambil teks hasil akhir dari state/store agent.
7. **Update row** status + result + turns_used + ended_at. Decrement budget
   counter root.
8. **Return** `DelegateResult` ke handler MCP → jadi tool result leader.

**cwd/project sama dengan parent** supaya sub-agent bisa baca/tulis file proyek
yang sama (use-case utama: researcher/reviewer kerja di repo yang sama). Isolasi
ada di **konteks percakapan** (history bersih), bukan di filesystem.

### 5.1 Jalur komunikasi — in-process, BUKAN file

Penegasan penting: leader dan sub-agent **tidak** berkomunikasi lewat file atau
lewat session JSON. wick = **broker in-process**.

```
LEADER (subprocess)            wick (broker, in-memory)          SUB-AGENT (subprocess)
  │ panggil wick_delegate                                              
  │ ── MCP call (loopback) ──▶  delegation.Run                         
  │                               │ pool.Send(childSess, task)         
  │                               │ ──── tulis ke STDIN ─────────────▶ │ (kerja)
  │                               │ ◀──── events via STDOUT ────────── │ (Thinking/ToolUse/Done)
  │                               │  parse → event.AgentEvent          
  │ ◀── hasil = MCP tool result ─ return Result                        
```

- **Task masuk** → ditulis ke **stdin** subprocess via `pool.Send`
  (`pool.go:338`); handle proses dipegang pool di memori.
- **Hasil keluar** → sub-agent tulis **stdout** → wick parse jadi
  `event.AgentEvent` → ambil teks akhir → balik sebagai **MCP tool result**.
- "Kabel"-nya = **OS pipe (stdin/stdout) + panggilan MCP**, efemeral, dalam satu
  daemon wick.

File/tabel adalah **state & audit, bukan bus pesan**:

| Artefak | Fungsi |
|---|---|
| `sessions/<id>/agents.json` | registry named-agent + `CLISessionID` (respawn `--resume`) + max_turns |
| `~/.claude/projects/...` (claude) | transcript milik CLI, dipakai resume |
| event store / SSE | feed live ke UI/monitor |
| `agent_delegations` | **salinan durable** task + hasil → sumber monitor/audit/recovery |

`agent_delegations.result` hanya **copy** hasil untuk history — exchange aslinya
sudah jalan via pipe+MCP. **Tidak ada file IPC khusus** di v1.

---

## 6. Governor

Lima rem independen, semua di-enforce **wick-side**:

### 6.1 `max_turns` per sub-agent (provider-agnostik) ⭐

Jawaban untuk "provider lain yang tak dukung max-turns":

- **Mekanisme universal:** wick **hitung `event.Done`** per child session dari
  stream ter-normalisasi (`internal/agents/event/types.go:41-42`; tiap provider
  emit `Done` di akhir tiap agentic turn, di-fire via `agent.go:854-860`
  `onEvent`). Saat counter mencapai `effective_max_turns` → panggil
  `pool.Kill(childSession, agentName)` (`pool.go:1136-1153`) → kembalikan hasil
  parsial + status `stopped_max_turns`.
- **Optimisasi per-provider:** kalau provider punya flag native (claude
  `--max-turns`, di-set via `SetMaxTurns` `session/agents.go:87-105`), pasang
  juga supaya CLI berhenti rapi SEBELUM wick force-kill. Untuk codex/gemini yang
  tak punya flag → **counter+Kill adalah satu-satunya mekanisme** dan itu cukup.
- `effective_max_turns = min(input.max_turns || profile.default_max_turns, cap_global)`.

### 6.2 `max_depth`

Setting global (default mis. 3). `Run` tolak kalau `depth > max_depth`.
`depth` diturunkan dari ancestor chain (lihat 6.4).

### 6.3 Budget turn global per-root

Tiap root delegation punya budget total turn (mis. default 40). Counter
agregat semua sub-agent di pohon root itu. Habis → delegasi berikutnya ditolak
status `stopped_budget`, sub-agent berjalan dibiarkan selesai (atau di-kill,
pilih saat implement — default: tolak yang baru, biarkan yang jalan).

### 6.4 Cycle-guard

Tiap child mewarisi **ancestor chain** (list profile_key dari root → parent).
`Run` tolak kalau `profile` sudah ada di chain (cegah A→B→A tak terhingga).
Chain disimpan in-memory per delegasi (tidak perlu kolom DB; bisa direkonstruksi
dari `root_id` + `parent_session_id` kalau perlu).

### 6.5 `max_parallel`

Cap jumlah sub-agent konkuren per-root (default mis. 4). Selaras dengan cap
konkurensi pool yang sudah ada. Lewat → call ke-(N+1) antre atau ditolak (default:
antre singkat lalu jalan saat slot bebas).

**Settings disimpan** di tabel settings existing (global) + override per-profil
(`default_max_turns`). UI di §9.

---

## 7. Provider compatibility matrix

| Peran | Syarat | claude | codex | gemini |
|---|---|---|---|---|
| **Leader** (panggil `wick_delegate`) | dukung MCP tool-use | ✅ | ✅ | ⚠️ verifikasi |
| **Sub-agent** (terima task → hasil) | bisa di-spawn pool | ✅ | ✅ | ✅ |
| `--max-turns` native | flag CLI | ✅ | ⚠️ | ⚠️ |
| Turn-enforcement wick (Done+Kill) | universal | ✅ | ✅ | ✅ |

**Fallback rules:**

- Provider tanpa MCP tool-use → **tak bisa jadi leader** (profil
  `can_delegate=false` dipaksa di UI untuk provider itu; validasi di save).
  Tetap valid jadi sub-agent/leaf.
- Provider tanpa `--max-turns` native → **counter+Kill wick** (§6.1). Tidak ada
  degradasi fungsional; bedanya cuma sub-agent berhenti via SIGKILL alih-alih
  exit rapi → hasil parsial di-capture dari event yang sudah masuk.
- **Gemini sebagai leader: open question** (§14) — verifikasi dukungan MCP
  tool-use di gemini CLI versi yang dipakai wick sebelum mengizinkan.

---

## 8. Fleet monitor (read-only)

Observability murni — **consumer**, tanpa infra baru.

### 8.1 Sumber data

| Data | Sumber | File:line |
|---|---|---|
| Daftar agent hidup | `pool.ActiveSnapshot() []ActiveEntry` | `pool.go:1075-1102` |
| Status lifecycle | `ActiveEntry.Lifecycle` (spawning/working/idle/killed) | `state/state.go:47-78` |
| Substate (saat working) | `ActiveEntry.Substate` (thinking/running_tool/responding) | `state/state.go:20-45` |
| PID + last active | `ActiveEntry.PID`, `.LastActive` | `pool.go:1075-1102` |
| Update live | SSE `Broadcaster` (lifecycle + event) | `stream.go:58-126`, handler `handler.go:1734-1807` |
| Riwayat task | `agent_delegations` (status, task, result, timestamps) | tabel baru |

### 8.2 Status taxonomy (diturunkan, bukan disimpan)

| Status UI | Aturan |
|---|---|
| 🟢 **Running** | `Lifecycle == working` (aktif thinking/tool/responding) |
| 🟡 **Idle** | `Lifecycle == idle` && PID hidup (warm, nunggu input / antar-turn) |
| ⚪ **Spawning** | `Lifecycle == spawning` |
| 🔴 **Dead** | `Lifecycle == killed` \|\| PID==0 \|\| proses exit |

Untuk sub-agent efemeral (Task-style): hidup `running` → selesai → `dead`
(dengan `agent_delegations.status` membedakan done vs stopped vs failed).
Untuk leader persisten: bisa `idle` antar pesan.

### 8.3 Endpoint

- `GET /agents/monitor` — halaman fleet (HTML, read-only).
- `GET /agents/monitor/snapshot` — JSON `pool.ActiveSnapshot()` digabung
  `agent_delegations` aktif (untuk initial render + polling fallback).
- Live: subscribe SSE `/stream` existing (global feed). **Tidak menambah hub
  baru.**
- ACL: operator melihat agent dalam scope tag-nya; admin lihat semua (pola
  `IsVisibleTo`). `agent_delegations.triggered_by` dipakai untuk filter
  non-admin.

### 8.4 Read-only = observe, bukan interaksi

v1: monitor hanya **lihat** (status, task sekarang, transcript read-only,
riwayat). **Tidak** ada tombol kirim pesan dari monitor. (Aksi `Kill` manual
dari monitor = open question §14 — berguna untuk operator, tapi perlu ACL hati-
hati.)

---

## 9. UI states

Detail visual: [`mockup.html`](mockup.html).

| State | Where | Note |
|---|---|---|
| ① Profil list | `/manager/agents/profiles` | Card per role: icon, provider/model badge, # tools (tag), enabled toggle, "+ New profile" |
| ② Profil editor | `/manager/agents/profiles/new` & `/{key}/edit` | Form: Meta + provider dropdown + model + system_prompt textarea + tag picker (akses tools) + default_max_turns + can_delegate toggle (auto-off kalau provider tak dukung leader) |
| ③ Pohon delegasi (in session) | `agents/session/{id}` | Di transcript leader: kartu `wick_delegate` (spinner → hasil), expand → transcript sub-agent read-only. Nested indent per depth + chip turns_used/budget |
| ④ Fleet monitor | `/agents/monitor` | Grid kartu agent: status chip (running/idle/dead), profil, task sekarang (truncate), depth, parent, turns_used, elapsed. Group by root atau by profil. Live via SSE |
| ⑤ Monitor detail | `/agents/monitor/{child_session_id}` | Transcript read-only sub-agent + meta delegasi + riwayat task profil itu |
| ⑥ Settings governor | `/manager/agents/settings` | max_depth, budget per-root, max_parallel, global cap max_turns |

### 9.1 Design-system rules (dipakai di mockup)

Patuh skill `design-system`:

- Font: Inter via `font-sans`. Weights 400/500/600 only.
- Primary accent: `green-500` (`#27B199`) — tombol utama, status running.
- Page bg: `white-200` / `dark:navy-800`; cards `white-100` / `dark:navy-700`.
- Borders: `white-300` / `dark:navy-600`. Text: `black-900` / `dark:white-100`.
- Status chip (pakai ramp status, BUKAN green untuk "success"):
  - Running → `pos-400` text + `pos-100` bg (atau `prog-400` untuk "aktif").
  - Idle → `cau-400` text + `cau-100` bg.
  - Spawning → `prog-400` text + `prog-100` bg.
  - Dead/failed → `neg-400` text + `neg-100` bg.
  - Done → `pos-400` text + `pos-100` bg.
- Depth indicator: indent 16px/level + garis `white-400`/`dark:navy-600`.
- Spacing 8-grid; cards `rounded-xl`; chips `rounded-full`; inputs `rounded-lg`.
- Icons 16/18/24px, stroke 2px.
- Mobile-first: monitor grid 1-col ≤375px → `sm:2` → `lg:3`.

---

## 10. Tags / ACL

Pola **sama persis** dengan connector (`internal/connectors/service.go`
`ListVisibleTo`/`IsVisibleTo`, `repo.go` filter tag):

- **Create/edit/delete profil** = admin-only (`IsAdmin`).
- **Siapa boleh delegasi ke profil X** = profil punya `allowed_tag_ids` untuk
  AKSES TOOLS sub-agent; visibilitas profil ke leader pakai tag filter terpisah
  (auto-create `agent:<profile_key>` filter tag saat save, sama pola
  custom-connector §9). Default admin-only sampai admin assign tag ke user/group.
- **Sub-agent tools** = sub-agent hanya lihat tools/connectors yang lolos
  `allowed_tag_ids` profil — jadi role "researcher" bisa dibatasi cuma ke web/loki,
  "reviewer" cuma ke git/repo, dst. Ini sekaligus **least-privilege** per role.
- **Monitor** = non-admin lihat delegasi yang `triggered_by` = dirinya / dalam
  scope tag; admin lihat semua.

---

## 11. Backward compat

- Tidak mengubah single-agent flow existing. Session tanpa delegasi = persis
  seperti sekarang.
- `pool.Send` / `AgentEntry` / event model **tidak berubah** — delegation
  package adalah consumer di atasnya.
- Migration: tambah 2 tabel (`agent_profiles`, `agent_delegations`). No
  drop/alter tabel existing.
- MCP: tambah 2 meta-tool (`wick_delegate`, `wick_agents`). Tool lain tak
  tersentuh. Leader tanpa profil/tanpa tool = tak terdampak.

---

## 12. Refactor surface — impact zones

### 12.1 Core (new)

| Zona | File / pkg | Catatan |
|---|---|---|
| Pkg baru | `internal/agents/delegation/` | `delegator.go` (Run + governor), `profile.go` (CRUD profil), `monitor.go` (snapshot+history join) |
| Schema | `internal/entity/agent_profile.go`, `agent_delegation.go` | structs |
| Migration | `internal/entity/migrations/NNNN_sub_agent_delegation.go` | 2 tabel + index |

### 12.2 MCP

| Zona | File / pkg | Catatan |
|---|---|---|
| Handlers | `internal/mcp/handlers/delegation.go` | `WickDelegate`, `WickAgents` descriptors + execute |
| List/dispatch | `internal/mcp/handler.go` | append descriptors di `handleToolsList`; branch di `handleToolsCall` (+ SSE `sse.go` kalau perlu jalur SSE — lihat pola `wick_execute`) |

### 12.3 Pool (consume, minim ubah)

| Zona | File / pkg | Catatan |
|---|---|---|
| `pool` | `internal/agents/pool/pool.go` | Reuse `Send`, `Kill`, `ActiveSnapshot`. Mungkin tambah helper spawn-with-system-prompt kalau belum ada jalur inject system prompt per child |
| event | `internal/agents/event` | Reuse `Done` untuk counter. **No change** |

### 12.4 Manager / Tools UI

| Zona | File / pkg | Catatan |
|---|---|---|
| Routes | `internal/manager/agents.go` (baru) atau extend agents handler | `/manager/agents/profiles*`, `/manager/agents/settings` |
| Monitor | `internal/tools/agents/` | `/agents/monitor*` — reuse SSE `Broadcaster` + `ActiveSnapshot` |
| Views | `internal/manager/view/agent_profiles*.templ`, monitor templ | design-system compliant |
| Session view | `internal/tools/agents/...session templ` | render kartu delegasi + pohon nested di transcript |
| JS | delegasi tree expander, monitor live (SSE consumer) | reuse `/stream` |

### 12.5 Tags

| Zona | File / pkg | Catatan |
|---|---|---|
| `internal/tags` | auto-create `agent:<key>` filter tag saat save profil (pola custom-connector §9.6) |

### 12.6 Tests

| Zona | Catatan |
|---|---|
| Unit | governor (depth/budget/cycle/max_parallel) table-driven; turn-counter (Done count → Kill); profil CRUD + ACL |
| Integration | delegate end-to-end: leader spawn sub-agent, hasil balik; nested 2-level; budget-exceeded → stopped; parallel 3 sub-agent |
| Provider-agnostik | turn-enforcement test dengan fake provider tanpa `--max-turns` → Done-count+Kill berhenti tepat |
| Monitor | snapshot join `agent_delegations`; status derivation running/idle/dead; SSE live update |
| Security | sub-agent allowlist sesuai tag profil (tak bisa akses tool di luar tag); monitor ACL non-admin |

---

## 13. Acceptance checklist (implementation gate)

- [ ] `internal/agents/delegation/` package: `Delegator.Run` sinkron, governor
      lengkap (max_turns/max_depth/budget/cycle/max_parallel)
- [ ] Migration `agent_profiles` + `agent_delegations` + index
- [ ] **max_turns provider-agnostik** — counter `event.Done` + `pool.Kill`;
      verified berhenti tepat pada provider tanpa flag native; flag native
      dipasang sebagai optimisasi bila ada
- [ ] **Parallel** — multiple `wick_delegate` dalam satu turn jalan konkuren,
      dibatasi `max_parallel`; tiap hasil balik benar
- [ ] MCP `wick_delegate` + `wick_agents` muncul di `tools/list`, dispatch di
      JSON + SSE path, ACL tag server-side
- [ ] Profil editor: provider dropdown, model, system_prompt, tag picker,
      default_max_turns, can_delegate (auto-off untuk provider non-leader)
- [ ] Sub-agent least-privilege: tools terlihat = sesuai `allowed_tag_ids`
- [ ] **Fleet monitor** — `/agents/monitor` live (SSE) menampilkan
      running/idle/dead, task sekarang, depth/parent, turns_used; detail =
      transcript read-only + riwayat task
- [ ] Pohon delegasi di session view: kartu delegate (spinner→hasil), nested
      indent, expand transcript sub-agent
- [ ] Settings governor page (max_depth, budget, max_parallel, cap max_turns)
- [ ] Tags: auto-create `agent:<key>` filter tag; default admin-only; assign via
      `/admin/tags`
- [ ] `agent_delegations` row ditulis tiap delegasi (running→terminal) dengan
      status akurat
- [ ] Tests pass (unit governor, integration nested+parallel+budget,
      provider-agnostik turn-stop, monitor ACL, sub-agent tool-scope)
- [ ] Docs: user-facing `docs/guide/sub-agents.md` + sidebar; design.md +
      mockup.html sinkron dengan kode

---

## 14. Open questions (need user input before scoping)

1. **Gemini sebagai leader** — apakah gemini CLI versi wick mendukung MCP
   tool-use (syarat panggil `wick_delegate`)? Kalau belum: gemini = sub-agent
   only di v1. Perlu verifikasi.
2. **Budget habis di tengah** — saat budget root habis sementara ada sub-agent
   masih jalan: (a) biarkan yang jalan selesai, tolak yang baru *(default
   usulan)*, atau (b) kill semua? 
3. **Kill manual dari monitor** — operator boleh klik "Kill" sub-agent dari
   fleet monitor? Berguna untuk runaway, tapi perlu ACL ketat (admin-only?).
4. **Squad eksplisit fase berapa** — v1 "leader boleh panggil profil mana pun
   yang lolos tag" cukup, atau langsung butuh grup squad bernama (leader+member
   tetap)? Usulan: fase-2.
5. **Sugar `wick_delegate_many`** — perlu tool batch eksplisit, atau cukup
   andalkan paralel alami dari multiple tool_use? Usulan: cukup alami.
6. **System prompt injection per child** — apakah pool sudah punya jalur inject
   system prompt per session, atau perlu helper baru di pool? (impl detail,
   verifikasi saat coding).
7. **Default angka governor** — max_depth=3, budget_per_root=40 turn,
   max_parallel=4, default_max_turns=12 — angka awal masuk akal? 

---

## 15. Rejected alternatives

- **Task-board asinkron (multica penuh)** — enqueue/claim/start/complete +
  board UI + autopilot. Beda paradigma dari "leader nunggu hasil"; jauh lebih
  besar (lifecycle, assignment, profil yang post comment/issue). v1 sinkron dulu;
  board bisa jadi fase-3 di atas `agent_delegations`.
- **Chatroom multi-agent (stoa)** — semua agent + human di satu room,
  mention-routed, agent saling lihat transcript. Butuh injeksi konteks antar-agent
  + turn-governor room. Use-case beda (diskusi, bukan delegasi tugas). Dipisah
  ke proposal sendiri kalau diinginkan.
- **Token-budget di v1** — event ter-normalisasi tak bawa usage (`types.go:76-86`).
  Implement butuh parse raw CLI usage. Turn-count sudah cukup sebagai rem v1.
- **Streaming hasil parsial sub-agent ke leader** — leader terima hasil akhir
  saja (blocking). Progress terlihat di monitor. Streaming ke konteks leader =
  kompleksitas + token bleed; ditolak v1.
- **Sub-agent provider baru / runtime plugin** — sub-agent = provider existing
  yang di-spawn pool. Tak ada loader/sandbox baru.
- **Session id sub-agent = thread/predictable** — child session pakai id baru
  unik (bukan thread_ts) supaya tak bisa ditebak/di-spoof; sejalan dengan
  pelajaran dari isu identitas Slack send-proxy.
- **File / shared-file sebagai jalur komunikasi** — ditolak. Leader↔sub-agent
  lewat pipe (stdin/stdout) + MCP yang dipegang pool in-memory (§5.1); file JSON
  hanya state/audit. Membuat "file kanal" khusus = IPC redundan + race + cleanup
  overhead, tanpa manfaat untuk model single-daemon sinkron.

### Boundary — kapan butuh bus nyata

v1 mengasumsikan **satu daemon wick**: leader + semua sub-agent jalan di proses
yang sama, jadi pipe lokal + MCP loopback cukup. **Kalau** suatu hari sub-agent
di-distribusi ke **mesin/host lain** (model daemon-terhubung ala stoa/multica),
pipe lokal tak lagi memadai → butuh **message bus** (WebSocket/queue) +
identitas/auth antar-node. Itu **di luar scope v1** dan akan jadi proposal
terpisah; `agent_delegations` sudah menyediakan record durable yang bisa jadi
fondasi bus terdistribusi nanti.
