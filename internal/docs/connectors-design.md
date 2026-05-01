# Connectors — Desain & State

Status: implemented (modul + persistence + MCP JSON-RPC meta-tool pattern
+ auth dual-mode PAT & OAuth 2.1 + per-user grant management + admin UI:
list/detail row CRUD, dedicated test page, dedicated history page dgn
filter URL-driven, admin overview pages utk connector instance, access
token, connected app cross-user, admin dashboard split Modules vs Access
stats + nav grouping ke Access/Setup dropdown).
Update terakhir: 2026-05-01.

Dokumen ini mencatat desain **Connectors** — kelas modul ketiga di wick,
sejajar dengan Tools dan Jobs, dirancang khusus dikonsumsi LLM lewat MCP
(Model Context Protocol). State dibawah refleksi dari kode di
`pkg/connector/`, `internal/connectors/`, `internal/mcp/`,
`internal/accesstoken/`, `internal/oauth/`, dan
`internal/entity/connector.go` + `internal/entity/oauth.go` +
`internal/entity/personal_access_token.go`.

---

## 1. Latar belakang

Wick punya dua jalur eksposur:

- **Tools** — buat manusia, lewat web UI.
- **Jobs** — buat scheduler, jalan di background.

Yang belum ada: cara rapi expose kapabilitas wick ke LLM client (Claude
Desktop, Cursor, custom agent). Tujuannya supaya LLM bisa manggil hal
seperti "ambil error log terbaru dari Loki", "lookup issue Jira", "post
ke Slack" lewat protokol standar, dengan auth per user, dan response
yang bentuknya sepenuhnya dikontrol developer (JSON ramping, bukan
payload mentah upstream).

Protokol yg dipilih: **MCP**. Mayoritas Tools yg ada terlalu UI-heavy
atau response-nya terlalu gemuk buat langsung diekspos. Connectors
dibikin sbg modul jenis baru, bukan retrofit Tools.

---

## 1.1 Prinsip desain

**Wick side boleh ribet, user side wajib simple.** Kompleksitas
(OAuth dance, JWT validation, tag resolution, transform response,
encryption, MCP dispatch) ditelan di sisi wick. Yg user lihat:
form isi configs, klik Save, copy 1 token, paste ke Claude Desktop —
selesai. Setiap pilihan UX ditimbang dgn pertanyaan: "user mesti
ngerti apa supaya ini jalan?" — jawabannya harus minimal.

---

## 2. Konsep

**Connector** = modul Go yg ditulis developer, bungkus satu API
eksternal khusus dikonsumsi LLM.

- HTTP call, header, body, error handling — semua hardcoded di Go.
  Connectors **bukan** HTTP builder generik user-defined.
- Developer **mengontrol bentuk response**. Response upstream di-parse
  dan ditransformasi jadi JSON ramping sebelum balik ke LLM.
- Satu definisi connector carry **N Operations** — aksi-aksi kecil yg
  bisa dipanggil LLM (`query`, `list_repos`, `create_issue`, ...).
  Setiap operation punya Input schema + ExecuteFunc sendiri, plus
  flag `Destructive` buat tandain aksi yg susah di-undo.
- Satu definisi bisa **diduplikat lewat web UI** jadi beberapa row,
  masing-masing carry credential sendiri. Satu Loki connector bisa
  punya row `prod`, `staging`, `dev` bersamaan — tiap row × tiap op
  yg enabled = satu MCP tool.
- Akses row pakai **tag filter** yg sama dgn Tools (sec. 5.1) —
  tiap row punya tag (mis. `user:yoga@abc.com`, `team:platform`),
  endpoint MCP cuma expose row yg tagnya match dgn user pemanggil.
  Tidak ada konsep "public" — semua row authenticated.

> Mental model: Connectors itu untuk LLM seperti Tools untuk manusia.
> Pola yg sama ("bungkus sesuatu di modul wick"), tapi audience dan
> kontrak output-nya beda — plus N operations per definisi.

---

## 3. Perbandingan dgn Tools dan Jobs

| Aspek           | Tool                          | Job                     | Connector                            |
|-----------------|-------------------------------|-------------------------|--------------------------------------|
| Audience        | Manusia via web UI            | Scheduler               | LLM via MCP                          |
| Lokasi logika   | Go (dev-authored)             | Go (dev-authored)       | Go (dev-authored)                    |
| Output          | HTML / templ                  | side effect, log        | nilai Go terstruktur → JSON          |
| Granularitas    | 1 modul = 1 surface UI        | 1 modul = 1 worker      | **1 modul = N operations**           |
| Row di DB       | duplikasi via Key             | 1 per job               | **N per Key** (row di `connectors`)  |
| Scope configs   | global per tool               | global per job          | **per row**                          |
| UI              | workflow custom penuh         | tidak ada               | panel test generik (Postman-style)   |
| Akses           | Private + tag filter          | n/a                     | selalu private + tag filter          |
| Auth            | session wick                  | n/a                     | bearer OAuth/SSO atau PAT            |
| Run history     | tidak ada                     | `JobRun`                | `ConnectorRun` (per call, full audit)|

---

## 4. Bentuk modul

Mengikuti pola `pkg/tool/` (`Module`, `Configs`, `RegisterFunc`) yg
sudah ada, supaya mental model dan helper refleksi
(`entity.StructToConfigs`) tetap konsisten.

```go
// pkg/connector/connector.go
type Meta struct {
    Key         string // "loki", "github"
    Name        string // "Loki"
    Description string // ditampilkan ke admin & LLM
    Icon        string
}

type Operation struct {
    Key         string          // "query", "list_repos"
    Name        string          // "Query Logs"
    Description string          // load-bearing — dibaca LLM di tools/list
    Input       []entity.Config // argumen LLM, jadi JSON Schema
    Execute     ExecuteFunc
    Destructive bool            // default: per-row toggle off
}

type ExecuteFunc func(c *Ctx) (any, error)

type Module struct {
    Meta       Meta
    Configs    []entity.Config // shared per row, semua op pakai sama
    Operations []Operation
}
```

`Ctx` menyediakan:

- `c.Cfg("token")` / `c.CfgInt(...)` / `c.CfgBool(...)` — nilai configs
  dari row yg dipilih (per-call resolved, bukan lookup global).
- `c.Input("query")` / `c.InputInt(...)` / `c.InputBool(...)` —
  argumen yg dikirim LLM untuk operation ini.
- `c.HTTP` — `*http.Client` dgn timeout default 30s. Bisa di-replace
  per call kalau butuh transport beda.
- `c.Context()` — `context.Context` untuk cancellation propagation.
- `c.InstanceID()` — id row, buat structured logging.

Configs dan Input keduanya pakai refleksi struct-tag `wick:"..."` yg
sudah ada, supaya form admin dan JSON Schema MCP bisa di-generate
otomatis:

```go
type LokiConfigs struct {
    URL   string `wick:"url,required,placeholder=https://loki.example.com"`
    Token string `wick:"token,secret,required"`
}

type QueryInput struct {
    Query string `wick:"query,required,description=LogQL query"`
    Start string `wick:"start,description=RFC3339 timestamp, optional"`
}
```

Konstruktor singkat untuk operation:

```go
connector.Op("query", "Query Logs",
    "Search Loki using LogQL.",
    QueryInput{}, queryExec)

// Destructive variant — default off di setiap row baru.
connector.OpDestructive("delete_repo", "Delete Repo",
    "Permanently delete a GitHub repository.",
    DeleteRepoInput{}, deleteRepoExec)
```

Registrasi di `main.go` downstream:

```go
app.RegisterConnector(
    loki.Meta(),
    loki.Configs{},        // typed configs struct, di-reflect ke form
    loki.Operations(),     // []connector.Operation
)
```

---

## 5. Persistence

Tiga tabel inti connector: `connectors`, `connector_operations`,
`connector_runs`. Tag association **reuse `ToolTag`** dgn
`ToolPath = "/connectors/{id}"` — tidak ada tabel join baru.

Auth pakai 4 tabel terpisah (di-cover §8): `personal_access_tokens`,
`oauth_clients`, `oauth_authorization_codes`, `oauth_tokens`.

### 5.1 `connectors`

```
connectors
  id         varchar(36)  PK   -- uuid, di-stamp BeforeCreate
  key        varchar(100) idx  -- FK logis ke Meta.Key code-side, NOT unique
  label      varchar(255)      -- "Loki Prod"
  configs    text              -- JSON map[string]string, secret di-mask di UI
  disabled   bool              -- row-level off-switch (orthogonal ke tag)
  created_by varchar(36)
  created_at timestamp
  updated_at timestamp
```

Catatan:

- **Bukan** `connector_instances` — namanya `connectors` (entity:
  `entity.Connector`).
- `key` not unique: banyak row bisa share key yg sama (Loki Prod, Loki
  Staging, Loki Dev semuanya `key="loki"`).
- `configs` JSON map keyed by field name di Configs struct. Secret
  field disimpan plaintext (matching konvensi tabel `configs` lama)
  dan dimask di UI render layer. Kalau encryption-at-rest jadi
  requirement, applied bareng tabel `configs` lama.
- **Tag access control** lewat `ToolTag` existing dgn
  `ToolPath = "/connectors/{id}"` — sama persis kayak Jobs yg pakai
  `"/jobs/{path}"`. Tidak ada tabel `connector_instance_tags` baru.
  Future rename `ToolTag` → generic entity-tag tracked terpisah.
- `Disabled` orthogonal ke tag: tag-filter gating siapa yg lihat,
  `Disabled=true` hide dari MCP `tools/list` & UI list view.

### 5.2 `connector_operations`

Per-(connector_row, op) toggle. Default dihitung dari `Operation.Destructive`
ketika row toggle belum ada — tidak perlu seed eager.

```
connector_operations
  connector_id   varchar(36) PK
  operation_key  varchar(100) PK
  enabled        bool         -- default true di kolom; aturan resolve di service
  updated_at     timestamp
```

Aturan default (di `Service.OperationStates`, fold-on-read):

- `Operation.Destructive == false` → enabled = true
- `Operation.Destructive == true`  → enabled = false (admin opt-in)

Row baru dimasukkan saat admin pertama kali toggle. Op yg belum pernah
disentuh = pakai default rule di atas.

### 5.3 `connector_runs` (audit trail + retry)

Menggantikan `connector_test_history` di draft awal — ini lebih luas:
satu tabel buat MCP call, panel-test, dan retry.

```
connector_runs
  id             varchar(36) PK
  connector_id   varchar(36)  -- FK logis ke connectors.id
  operation_key  varchar(100)
  user_id        varchar(36)
  source         varchar(20)  -- "mcp" | "test" | "retry"
  request_json   text         -- input args, NO credentials (those live on connector)
  response_json  text         -- marshaled ExecuteFunc return value (truncatable)
  status         varchar(20)  -- "running" | "success" | "error"
  error_msg      text
  latency_ms     int
  http_status    int
  ip_address     varchar(45)  -- v4 atau v6
  user_agent     varchar(512)
  parent_run_id  varchar(36)  -- non-nil hanya untuk source=retry
  started_at     timestamp
  ended_at       timestamp
  created_at     timestamp
```

Index strategy (composite, tiap query yg dilayani):

- `(connector_id, started_at DESC)` → "recent runs for this connector"
- `(user_id, started_at DESC)` → user activity timeline
- `(status, started_at DESC)` → "recent errors" filter
- `(ip_address, started_at DESC)` → future allow/block UX
- `started_at` standalone → retention purge cheap range delete
- `parent_run_id` → retry lineage trace

Retention: `Service.PurgeOldRuns(retentionDays)`. Default 7 hari, di-jalanin
cleanup job di phase berikutnya.

### 5.4 Bootstrap (auto-seed 1 row)

Tidak ada `DefaultSeed` di Module — terlalu mekanisme buat sedikit
gain. Sebagai gantinya, `Service.Bootstrap`:

- Daftarkan setiap module ke dispatch table (`s.modules[Key]=module`).
- Untuk tiap key: kalau `CountByKey(key) == 0`, auto-create satu row
  kosong: `Label = Meta.Name`, `Configs = "{}"`. Admin tinggal buka
  UI dan isi credential.
- Row existing **tidak pernah** diutak-atik — admin yg sudah isi
  cred gak akan kehilangan data saat restart.
- Duplicate Keys = error fatal di boot. Row yg key-nya gak punya
  module registered (mis. dropped di deploy berikutnya) ditoleransi:
  muncul "deactivated" di UI, `Execute` returns error.

### 5.5 Duplicate → reset configs

Di `Service.Duplicate`:

- Row baru: `Key` di-copy, `Label = "<src> (copy)"`, `Configs = "{}"`.
- Tag dari source **tidak** diwarisin (anti-bocor: row team-shared
  diduplikat user pribadi tetap pribadi). Caller yg assign tag user
  lewat `ToolTag` setelah duplicate.

UI flow setelah duplicate:

```
[Duplicate] → redirect ke form edit row baru
            → semua field configs kosong, ditandai "required"
            → user isi → save → ready dipakai
```

### 5.6 Model akses (tag filter)

Connector row **selalu private** di level transport — endpoint `/mcp`
selalu butuh bearer token. Tidak ada konsep "public"; LLM client wajib
authenticated.

Di dalam authenticated user, gating dilakukan dgn tag filter (sama persis
dgn Tools Private + Jobs):

- Row dgn 0 filter-tag → visible ke semua approved user
- Row dgn ≥1 filter-tag → visible kalau user carry minimal 1 dari tag itu
- Admin → bypass, lihat semua

Tag itu sendiri **arbitrary string** — admin-defined. Gak ada konvensi
prefix wajib di code (`user:`, `team:`, `role:` dll cuma contoh
naming, bukan rule). Yg load-bearing: flag `IsFilter=true` di tabel
`tags`, dan link many-to-many lewat `ToolTag` (row ↔ tag) +
`UserTag` (user ↔ tag).

Konsekuensi: 1 tag bisa di-link ke N user + N connector row,
sharing-nya granular. Helper resolve tag user pakai middleware existing
(`login.GetUserTagIDs`).

Implementasi: `connectors.Repo.ListAccessibleTo(ctx, userTagIDs)` +
`IsAccessibleTo` di `internal/connectors/repo.go`.

---

## 6. Web UI

Empat surface admin-facing buat manage connector (semua implemented),
plus profile area user-facing buat manage auth.

### 6.1 List + detail row *(implemented)*

```
/manager/connectors/loki                       — list semua row utk Meta.Key=loki
└── Loki Prod        [user:yoga]              [⋮ menu]
└── Loki Staging     [user:yoga, env:staging] [⋮ menu]
└── Loki Platform    [team:platform]          [⋮ menu]
   ↳ klik card → /manager/connectors/loki/{id}  (detail page)

/manager/connectors/loki/{id}                  — detail page (settings-only)
├── Identity         label, status badge, ID
├── Top actions      History · Duplicate · Disable/Enable · Delete
├── Label form
├── Credentials      auto-render dari Module.Configs (typed struct + secret mask)
└── Operations       table:
       Operation │ Description │ Actions          │ Enabled
       query     │ ...         │ [Test] [History] │ [Disable]
       delete    │ ⚠ destruct. │ [Test] [History] │ [Enable]
```

- **List page** ([connector_list.templ]) — n8n-style stacked cards, kebab
  menu kanan untuk Disable/Duplicate/Delete tanpa pindah halaman.
- **New row**: tombol `+ New row` mint row kosong (`Configs="{}"`,
  Label = `Meta.Name + " (new)"`) lalu redirect ke detail. Form per-field
  pakai `ConfigsTable` shared dgn Tools/Jobs.
- **Per-op toggle**: tiap operation row punya kolom `Enabled` dgn tombol
  Enable/Disable. Default mengikuti `Operation.Destructive` (off untuk
  destructive, on untuk sisanya).
- **Per-op actions**: kolom `Actions` punya 2 link — `[Test]` ke
  `/test?op=<key>` dan `[History]` ke `/history?op=<key>`. Detail page
  fokus settings; runtime dilempar ke page khusus.
- **Duplicate**: copy row → configs **direset**. Tag dari source tidak
  diwarisin. Lihat 5.5.
- **Disable**: row-level off-switch; hide dari MCP `tools/list` & list
  view tanpa harus delete.

### 6.2 Test page (gaya Postman) *(implemented)*

`GET /manager/connectors/{key}/{id}/test?op=<op_key>`
([connector_test.templ] + [connector_test.js]).

```
/manager/connectors/loki/{id}/test?op=query
├── Breadcrumb: Home / Loki / Loki Prod / Test
├── Header: "Test runner" + [View history] (preserve op filter)
├── Operation dropdown        — URL-synced: ganti = history.replaceState ke ?op=...
├── Input form                — di-render dari op.Input via testInputField
├── [Run]                     — POST /test (JSON {operation, input})
└── Result panel              — status pill + latency + response/error pre
```

- **URL sync**: ganti dropdown ngubah `?op=` lewat `history.replaceState`
  — back/refresh preserve pilihan, link dari detail page bisa preselect.
- **No back to detail**: ganti operation = ganti form aja, tetap di
  page yg sama. Tombol Run + result panel tetap visible.
- Backend handler `connectorTestPage` + endpoint POST `/test` reuse
  `Service.Execute` dgn `Source=ConnectorRunSourceTest`. Path code yg
  sama dgn MCP `tools/call` — verifikasi behavior end-to-end.

### 6.3 History page *(implemented)*

`GET /manager/connectors/{key}/{id}/history?op=...&source=...&status=...&user=...&page=N`
([connector_history.templ] + [connector_history.js]).

```
/manager/connectors/loki/{id}/history?op=query&status=error&page=2
├── Breadcrumb: Home / Loki / Loki Prod / History
├── Filter bar (4 select, URL-driven)
│     Operation │ Source │ Status │ User
│     [query ▾] │ [all ▾]│ [error▾]│ [all ▾]
│     [Clear all filters] (muncul kalau ada filter aktif)
├── Table (max 10 rows / page)
│     ▸ When │ Operation │ Source │ User │ Status │ Latency
│     ▸ 2m ago│ query    │ mcp    │ Yoga │ error  │ 312 ms
│       (klik row → expand inline)
│       └── Request JSON · Response JSON · Run ID · IP · UA · HTTP
└── Pager: "Showing 11–20 of 137 run(s)"   [← Prev] Page 2 of 14 [Next →]
```

- **Filter chips URL-driven**: tiap `<select>` change → navigate ke
  baseUrl + `?key=value` baru (tanpa `page=`, jd reset ke page 1).
  Link bisa di-share, refresh preserve.
- **Pagination**: 10 row/page, server-side (`LIMIT 10 OFFSET (page-1)*10`).
  Prev/Next preserve filter via `historyPageURL` helper. `?page=` di-clamp
  ke `[1, totalPages]`; out-of-range → ditarik balik ke page terakhir,
  bukan empty list.
- **User column**: resolve `UserID` → display name via
  `login.Service.GetUserByID`. Map dibangun sekali per page render
  (`resolveRunUsers`) supaya N+1 batched ke distinct user ID. Empty
  UserID → "system". Unknown → short ID.
- **Expand row**: klik row toggle detail row di bawahnya — dua kolom
  Request/Response (pretty-printed JSON), plus run ID + IP + UA + HTTP
  status di footer. Zero round trip (data sudah di DOM).
- Backend handler `connectorHistoryPage` panggil
  `Service.CountRunsFiltered(...)` + `Service.ListRunsFiltered(ctx,
  connectorID, RunFilter{...}, pageSize, offset)` — count untuk
  totalPages, list untuk page yg aktif. Same composite-index query.
- **Audit trail granularitas**: yg ke-track baru `user_id` + IP + UA.
  Token-id (PAT vs OAuth client mana) belum di-track — semua PAT/grant
  milik 1 user terlihat seragam. Trade-off awal; nanti tambah
  `auth_token_id` + `auth_token_kind` kalau "siapa pakai token mana"
  jadi load-bearing buat triage abuse.

### 6.4 Profile area *(implemented)*

Di-render via `ProfileLayout` (admin-style header, max-w-container)
dgn 4 tab: Account · Access Tokens · Connected Apps · MCP.

```
/profile               — password change, display preferences
/profile/tokens        — generate/revoke Personal Access Tokens
/profile/connections   — list & disconnect OAuth-authorized apps
/profile/mcp           — endpoint URL + install snippets (OAuth + bearer)
```

- **Access Tokens** ([internal/accesstoken/view/tokens.templ]):
  table `Name | Token (masked) | Created | Last used | Revoke`. "Create
  token" → inline form → submit → render-once banner dgn plaintext
  `wick_pat_xxx`. Hash-only persisted; plaintext gak pernah re-readable.

- **Connected Apps** ([internal/oauth/view/connections.templ]):
  satu row per (user × OAuth client) yg punya active token. Disconnect
  → revoke semua access + refresh token client itu, app tinggal re-OAuth
  kalau mau akses lagi.

- **MCP** ([internal/accesstoken/view/mcp.templ]): dokumentasi 2 jalur
  — section "Claude.ai (OAuth-aware)" (cuma paste URL) + section
  "Claude Desktop / Cursor / VSCode (Bearer)" (4 install snippet siap-paste).

### 6.5 Admin overview pages *(implemented)*

Tab strip di `AdminLayout` punya 3 surface cross-user paralel ke /profile
area (sec. 6.4) — admin-only, bypass tag filter, lihat semua user.

```
/admin/connectors      — semua Connector row (cross-Key) — toggle Disabled, set tags, link ke /manager
/admin/access-tokens   — semua active PAT — owner, masked token, last used, admin revoke
/admin/connections     — semua active OAuth grant — owner, app, granted, last used, admin disconnect
```

- **Connectors** ([internal/admin/view/connectors.templ] +
  [internal/admin/connectors.go]): row label, module name, status pill.
  Disable toggle nulis ke `Connector.Disabled` langsung (entity field,
  bukan ToolPermission — yg disabled ngumpet dari MCP `tools/list` dan
  test panel). Tag picker reuse `ToolTag` dgn path `/connectors/{id}`
  — sama persis dgn manager UI. Row yg `Key`-nya gak ada module
  registered ditandai "Module not registered" jadi admin bisa cleanup.

- **Access Tokens** ([internal/admin/view/access_tokens.templ] +
  [internal/admin/access_tokens.go]): cross-user view dari /profile/tokens.
  Stat card (active tokens · users with token · never used) + table
  (owner · masked token · created · last used · revoke). Admin revoke
  pakai `accesstoken.Service.RevokeAny` — bypass owner check yg ada di
  user-facing /profile/tokens.

- **Connected Apps** ([internal/admin/view/connections.templ] +
  [internal/admin/connections.go]): cross-user view dari
  /profile/connections. Satu row per (user × OAuth client) yg punya
  active token. Disconnect → `oauth.Service.RevokeGrant(userID,
  clientID)` revoke semua access + refresh token user buat client itu;
  app musti re-OAuth. Backed by `oauth.Repo.ListAllGrants` (versi
  `ListGrantsByUser` tanpa filter user — same SQLite/Postgres timestamp
  parsing dance).

Surface ini admin-override; gak ada konfirmasi dari token/grant owner.
Audit trail per call masih di `connector_runs` (sec. 5.3) — tab ini
cuma manage-state, bukan log.

### 6.6 Admin dashboard + nav grouping *(implemented)*

`/admin` dashboard ([internal/admin/view/dashboard.templ] +
[internal/admin/handler.go]) split jadi 2 stat group biar Modules
(execution health) ga ke-mix sama Access (auth surface):

```
Modules : Tools · Jobs · Enabled · Running · Configs · Missing
Access  : Connectors · Access Tokens · Connected Apps   (clickable cards)
```

Stats dihitung per render dari `connectors.List`,
`tokens.ListAllActive`, `oauth.ListAllGrants`. Connector card carry
hint kecil (`X disabled` / `all enabled`) — quick health glance tanpa
buka /admin/connectors.

Nav strip ([internal/admin/view/layout.templ]) di-grouping jadi 7
slot biar muat di header tanpa wrap:

```
Dashboard · Users · Tools · Jobs · Connectors · Access ▾ · Setup ▾
                                                  │         │
                                                  │         └─ Configs · Tags
                                                  └─ Access Tokens · Connected Apps
```

Dropdown pakai `<details>/<summary>` (pattern sama dgn `UserMenu` di
`internal/pkg/ui/nav.templ`) — zero JS, summary highlight kalau child
aktif. Connectors sengaja dipertahankan top-level krn frequently
visited; auth surface (token + grant) dilipat ke Access dropdown krn
admin jarang nyentuh setelah onboarding.

---

## 7. Eksposur MCP

### 7.1 Transport

**Streamable HTTP** (endpoint tunggal). Alasan:

- Wick = server remote (bukan child process stdio lokal).
- Streamable HTTP menggantikan transport SSE legacy di spec MCP
  versi sekarang.
- Mayoritas request-response — JSON cukup buat 90% connector.
- Streamable HTTP (spec 2025-03-26) bisa dipake buat tools/call: client
  kirim `Accept: text/event-stream`, server balas SSE body. Connector
  emit progress lewat `Ctx.ReportProgress` → di-frame jadi
  `notifications/progress`. Heartbeat `:keepalive` tiap 15s biar reverse
  proxy ga reap koneksi mid-call.
- GET-based SSE (server-initiated) belum dipakai — wick ga punya msg
  yg di-push tanpa client request dulu.

### 7.2 Surface endpoint

```
POST /mcp                                       -- JSON-RPC 2.0 (implemented)
                                                   - Accept: application/json     → JSON response (default)
                                                   - Accept: text/event-stream    → Streamable HTTP for tools/call
GET  /mcp                                       -- server→client SSE (belum, opsional)

-- Auth metadata (implemented)
GET  /.well-known/oauth-protected-resource      -- RFC 9728
GET  /.well-known/oauth-authorization-server    -- RFC 8414

-- OAuth 2.1 server (implemented)
POST /oauth/register                            -- DCR (RFC 7591)
GET  /oauth/authorize                           -- PKCE consent
POST /oauth/authorize                           -- consent submit
POST /oauth/token                               -- code exchange + refresh
```

### 7.3 Meta-tool pattern

MCP surface **bukan** N×M static tool (1 entry per connector×op). Sebagai
gantinya, server expose **4 tool tetap** yg LLM pake buat discovery dan
dispatch:

| Tool | Annotation | Fungsi |
|---|---|---|
| `wick_list` | `readOnlyHint: true` | List semua tool visible ke caller — tanpa `input_schema` |
| `wick_search` | `readOnlyHint: true` | Cari tool by keyword (substring match: label + name + desc) |
| `wick_get` | `readOnlyHint: true` | Ambil detail 1 tool by `tool_id`, termasuk `input_schema` |
| `wick_execute` | `destructiveHint: true` | Eksekusi tool by `tool_id` + `params` |

**Kenapa meta-tool, bukan static list:**

- Tambah/hapus connector instance di admin UI → MCP surface **tidak
  berubah** → client (Claude.ai) tidak perlu refresh tool list manual.
- Token `wick_list` / `wick_search` lebih kecil karena tidak bawa
  `input_schema` — LLM hanya bayar token schema buat tool yg akan dipakai.
- Scale ke ratusan connector tanpa balon `tools/list` response.

**Tool ID format:**

```
conn:{connector_id}/{op_key}
```

UUID-based, tidak berubah saat admin rename label instance. `conn:` prefix
disisakan buat future extension (mis. `mcp:` buat proxied remote MCP tools,
`prompt:` buat prompt templates).

**Flow LLM (tipikal):**

```
wick_list                           → dapet daftar tool + tool_id
wick_get({tool_id: "conn:abc/get"}) → dapet input_schema
wick_execute({tool_id, params})     → hasil
```

Atau shortcut kalau LLM sudah tahu schema dari deskripsi:

```
wick_search({query: "loki query"}) → match + tool_id
wick_execute({tool_id, params})    → hasil
```

**Isi payload `wick_list` / `wick_search`:**

```json
{
  "tools": [
    {
      "tool_id": "conn:7f3a9c2e-4b1d-11ee-be56/query",
      "connector": "Loki Prod",
      "name": "Query Logs",
      "description": "Search Loki using LogQL.",
      "destructive": false
    }
  ],
  "total": 1
}
```

**Isi payload `wick_get`:**

Sama seperti entry di atas, ditambah `input_schema` (JSON Schema object).

**Auth check per call:**

Setiap `wick_execute` dan `wick_get` re-check `IsVisibleTo(connectorID,
tagIDs, isAdmin)` — tidak trust list cache. Tag user bisa berubah antara
list dan call. `connector_operations` enable state juga di-validasi oleh
`Service.Execute`.

`ListVisibleTo` query: `SELECT connectors JOIN tool_tags JOIN tags WHERE
tool_tags.tool_path = '/connectors/'||id AND (tags.is_filter = false OR
tag_id IN (tagIDs))`. Logika identik dgn cara Tools Private resolve akses.

### 7.4 Session

- `Mcp-Session-Id` di-generate saat call `initialize` pertama.
- Disimpan **in-memory** (struct kecil: client capabilities, user_id,
  created_at). Tidak persist ke DB.
- Saat server restart, session hilang; client `initialize` ulang dan
  dapat session id baru. Transparent buat user.
- Auth (sec. 8) yg load-bearing identitas — session cuma marker
  handshake protokol.

### 7.5 Streaming, kapan dipakai

Default: `Content-Type: application/json`, single response.

Pindah ke `Content-Type: text/event-stream` cuma kalau:

- Run operation diperkirakan > 5 detik dan butuh event progress.
- Server perlu push `notifications/tools/list_changed` — **saat ini
  tidak dibutuhkan** karena meta-tool pattern membuat tool list statis
  (selalu 4 entry `wick_*`). Kalau kelak ada tipe tool baru yg perlu
  advertise secara eksplisit, `GET /mcp` SSE channel baru relevan.

---

## 8. Auth

Dual-mode bearer di endpoint `/mcp`: **PAT** (static) atau **OAuth 2.1**
(dynamic). Middleware unified detect prefix → route ke validator yg
sesuai. Dua mode coexist tanpa endpoint terpisah.

### 8.1 Flow OAuth (Claude.ai dst)

```
1. Claude.ai → POST /mcp  (tanpa token)
2. Wick → 401 + WWW-Authenticate: Bearer resource_metadata="..."
3. Client GET /.well-known/oauth-protected-resource
4. Client GET /.well-known/oauth-authorization-server
5. Client POST /oauth/register  (DCR, gak ada pre-registration)
   → {client_id}
6. Client redirect browser → GET /oauth/authorize?
       client_id=...&code_challenge=...&code_challenge_method=S256
7. Wick check session cookie → kalau gak login, set after-login cookie
   + redirect /auth/login. Habis login (password atau Google SSO),
   bounce balik ke /authorize.
8. Wick render consent page → user click Approve
9. POST /oauth/authorize → mint code → redirect ke client redirect_uri
10. Client POST /oauth/token (grant_type=authorization_code, PKCE verifier)
    → {access_token: wick_oat_xxx, refresh_token: wick_ort_xxx, expires_in}
11. Client retry POST /mcp  Authorization: Bearer wick_oat_xxx
12. Wick validate → resolve user_id → ListVisibleTo(user_tag_ids) → tools/list
```

### 8.2 Flow PAT (Claude Desktop / Cursor / cURL / dll)

```
1. User generate token di /profile/tokens → render-once `wick_pat_xxx`
2. User paste ke client config (Claude Desktop config.json dst)
3. Client POST /mcp Authorization: Bearer wick_pat_xxx
4. Wick validate (SHA-256 hash lookup) → user_id → tag-filtered list
```

PAT gak butuh OAuth dance — single round trip. Useful buat client yg
gak speak OAuth flow (Claude Desktop, Cursor, custom CLI).

### 8.3 Lokasi auth server

**Self-hosted**: wick implement sendiri `/oauth/{authorize,token,register}`
+ `.well-known/*`. Federasi sosial via login wick existing (password
atau Google SSO yg udah ada).

Original draft pertimbangin opsi delegasi (Auth0/Clerk/Keycloak),
tapi self-hosted dipilih krn:
- Wick udah punya user table + session cookie + Google SSO
- Delegasi nambah dependency eksternal + secret rotation overhead
- Token storage opaque (bukan JWT) → no key management

Implementasi di `internal/oauth/`:
- `service.go` — DCR, IssueAuthCode, ExchangeAuthCode, ExchangeRefreshToken,
  Authenticate
- `repo.go` — gorm CRUD + chain revocation buat replay detection
- `handler.go` — 5 routes + per-user grant management

### 8.4 Mode token

Endpoint `/mcp` terima dua format, dibedakan prefix:

| Mode              | Wire format                  | Validator                       | Storage              |
|-------------------|------------------------------|---------------------------------|----------------------|
| **PAT**           | `wick_pat_<32hex>`           | SHA-256 hash lookup             | `personal_access_tokens` |
| **OAuth access**  | `wick_oat_<32hex>`           | SHA-256 hash lookup + expiry    | `oauth_tokens` (kind=access) |
| **OAuth refresh** | `wick_ort_<64hex>`           | SHA-256 hash lookup + chain     | `oauth_tokens` (kind=refresh) |

Semua opaque (bukan JWT). Stored hashed. Plaintext cuma cross the wire
saat issue (PAT: render-once banner di /profile/tokens; OAuth: response
body dari /oauth/token). DB leak ≠ token leak.

OAuth feature lengkap:
- PKCE S256 mandatory (OAuth 2.1 spec, gak terima `plain`)
- Dynamic Client Registration (RFC 7591) tanpa pre-shared secret
- Refresh rotation tiap exchange + replay detection (token-reuse =
  revoke chain via `parent_token_id`)
- Authorization Server Metadata (RFC 8414) + Protected Resource
  (RFC 9728)
- TTL: access 1h, refresh 30d, auth code 5min

### 8.5 Middleware

```go
// internal/mcp/auth.go
func (m *AuthMiddleware) resolveToken(ctx, plain string) (userID, error) {
    if strings.HasPrefix(plain, accesstoken.Prefix) {  // "wick_pat_"
        return m.tokens.Authenticate(ctx, plain)
    }
    if m.oauth != nil {
        return m.oauth.Authenticate(ctx, plain)        // wick_oat_*
    }
    return "", ErrInvalid
}
```

Middleware juga set `login.WithUser(ctx, user, tagIDs)` — same context
shape sebagai cookie session, jadi downstream code (`login.GetUser`,
`login.GetUserTagIDs`) jalan identik.

### 8.6 Isolasi & sharing per user

| Resource                      | Scope                                                  |
|-------------------------------|--------------------------------------------------------|
| Definisi connector (Module)   | global (kode Go, semua user lihat template sama)       |
| Connector row                 | gating via tag filter (`UserTag` ↔ `ToolTag` row)      |
| Operation enable state        | per row (`connector_operations`)                       |
| Personal access token         | per user; user manage di /profile/tokens               |
| OAuth grant (refresh + access)| per (user, client); user manage di /profile/connections |
| `connector_runs`              | per user pemanggil; admin bisa lihat semua             |
| Eksekusi MCP `tools/call`     | dicek ulang tag user + op enable state setiap call     |
| IP/UA per call                | dicatat di `connector_runs.ip_address`/`user_agent`    |

User bisa disconnect single OAuth grant tanpa affect PAT-nya, dan
revoke single PAT tanpa affect OAuth grants.

---

## 9. Rencana fase

1. **Skeleton** ✅
   - `pkg/connector/` — `Meta`, `Module`, `Operation`, `Op`, `OpDestructive`,
     `ExecuteFunc`, `Ctx`, `NewHTTPClient`.
   - `app.RegisterConnector(meta, configs, ops)`.
   - Registry in-memory di `internal/connectors/registry.go`.

2. **Persistence + Service** ✅
   - Tabel `connectors`, `connector_operations`, `connector_runs` +
     index composite.
   - `Service` CRUD + `Bootstrap` (auto-seed 1 row per key) +
     `Duplicate` (reset configs) + `Execute` + `Retry` +
     `OperationStates` + `PurgeOldRuns`.
   - Reuse `ToolTag` dgn path `/connectors/{id}` — tidak ada tabel
     join baru.

3. **Connector pertama** ✅
   - `internal/connectors/crudcrud/` jadi pilot — CRUD generik thd
     crudcrud.com sandbox. 5 operasi (create/list/get/update/delete),
     1 destructive op (delete).

4. **MCP server** ✅
   - `internal/mcp/` — JSON-RPC handler, bearer auth middleware,
     schema converter.
   - Endpoint `POST /mcp` dispatch `initialize`, `tools/list`,
     `tools/call`, `ping`.
   - **Meta-tool pattern**: `tools/list` selalu return 4 tool tetap
     (`wick_list`, `wick_search`, `wick_get`, `wick_execute`).
     Discovery dan dispatch connector dilakukan via tool_id
     `conn:{connector_id}/{op_key}` — bukan nama tool statis.
   - `wick_execute` bind ke `connectors.Service.Execute` dgn
     `Source=ConnectorRunSourceMCP`. Tag-filtered + op-state check
     per call.

5. **Auth** ✅
   - **PAT** di `internal/accesstoken/` — generate/revoke di
     /profile/tokens. Format `wick_pat_<32hex>`, hash-only stored.
   - **OAuth 2.1** di `internal/oauth/` — DCR + PKCE + refresh
     rotation + chain replay detection. Self-hosted (bukan delegasi).
   - Format `wick_oat_<32hex>` (access) + `wick_ort_<64hex>` (refresh),
     opaque (bukan JWT).
   - Per-user grant management di /profile/connections.
   - .well-known/* metadata + /oauth/{register,authorize,token}.

6. **Web UI admin** ✅
   - Page `/manager/connectors/{key}` list row dgn n8n-style cards +
     kebab menu (Disable/Duplicate/Delete).
   - Page `/manager/connectors/{key}/{id}` detail row: identity, label,
     credentials (typed struct → ConfigsTable), operations table dgn
     Test/History/Enable per row.
   - Tombol Disable/Duplicate/Delete di header detail.
   - Bootstrap auto-bikin row kosong per Key biar admin tinggal isi
     credential dari UI.

7. **Test page + history page** ✅
   - `/manager/connectors/{key}/{id}/test?op=...` standalone Postman-style
     runner. Operation dropdown URL-synced (history.replaceState) — ganti
     op tanpa back, refresh preserve. Run pakai `Service.Execute` dgn
     `Source=ConnectorRunSourceTest`.
   - `/manager/connectors/{key}/{id}/history?op=...&source=...&status=...&user=...`
     audit log dedicated. Filter chips URL-driven (shareable links),
     user column resolve nama via `login.Service.GetUserByID`, expand
     row reveal request/response JSON + IP/UA/HTTP. Backed by
     `Service.ListRunsFiltered` (single composite-index query).
   - Retry button via `Service.Retry` masih outstanding — sekarang user
     copy request JSON manual + Run di test page.

8. **Streaming + notification** *(opsional, low priority)*
   - Stream SSE `GET /mcp` — buat long-running ops (> 5s).
   - `notifications/tools/list_changed` — **tidak dibutuhkan** selama
     meta-tool pattern dipakai; tool list selalu statis 4 entry.

9. **Admin overview pages** ✅
   - `/admin/connectors` — list semua Connector row cross-Key, toggle
     Disabled (entity field, bukan ToolPermission), set tags via
     `ToolTag` path `/connectors/{id}`, link ke /manager utk edit
     credential + ops.
   - `/admin/access-tokens` — cross-user view PAT, admin revoke via
     `accesstoken.Service.RevokeAny`. Bukan `/admin/mcp` — PAT itu
     bearer general-purpose, MCP cuma satu caller.
   - `/admin/connections` — cross-user view OAuth grant, admin
     disconnect via `oauth.Service.RevokeGrant`. Backed by
     `oauth.Repo.ListAllGrants` (varian `ListGrantsByUser` tanpa
     filter user). Lihat sec. 6.5 buat detail.

10. **Convenience** *(belakangan)*
    - Cleanup job harian → `Service.PurgeOldRuns(retentionDays)` +
      purge expired `oauth_authorization_codes` + `oauth_tokens`.
    - Admin view `/admin/oauth-clients` — list registered DCR clients
      + revoke (sekarang SQL only). Beda dgn /admin/connections yg
      grant-centric: ini client-centric (1 row per `oauth_clients`).
    - OAuth Token Revocation endpoint (RFC 7009) — `POST /oauth/revoke`
      buat client revoke own token.
    - Import OpenAPI / Postman collection buat scaffold stub Go
      connector.

---

## 10. Pertanyaan terbuka

- **Gaya transformasi response.** Operation return struct Go bertipe
  (lalu wick `json.Marshal`), atau selalu return `map[string]any`?
  Sekarang crudcrud pilot pakai `any` (mostly map) krn sandbox shape
  user-defined. Connector domain-specific (Loki dst) bakal lebih
  cocok struct bertipe. Konvensi belum final.
- **Penyimpanan secret.** Encrypt field configs at rest di kolom
  `connectors.configs`? Pakai envelope encryption, atau cukup
  encryption di level DB? Konsisten dgn tabel `configs` lama. Sama
  jg untuk `personal_access_tokens.token_hash` — sekarang cuma SHA-256
  (irreversible), tapi `oauth_tokens` punya plaintext claim flow di
  /token response yg secara teori bocor di log proxy.
- **Visibility definisi.** Apakah ada definisi connector yg admin-only
  (gak muncul di picker "+ New row" milik user)? Bisa di-gate lewat
  Module-level tag mirip `DefaultTags` Tool. Belum implement krn UI
  manage row sendiri belum ada.
- **Rate limit.** Per user, per row, atau per connector? Client MCP
  bisa cukup chatty. Belum implement — tergantung observe pattern
  abuse di production.
- ~~**Penamaan di MCP.**~~ *Resolved.* Diganti meta-tool pattern —
  LLM tidak melihat nama per-connector di `tools/list`. Tool ID
  `conn:{uuid}/{op_key}` tidak pernah bentrok dan tidak berubah saat
  admin rename label.
- **Reset configs saat duplicate — full vs partial.** Sekarang full
  reset (`Configs = "{}"`). Field non-secret (URL endpoint) sering
  reusable; cuma yg `secret` yg harus re-isi. Partial-reset lebih
  ergonomis tapi butuh metadata `secret` konsisten di tag struct.
- **Generic entity-tag.** `ToolTag` sekarang dipakai Tools, Jobs,
  Connectors via path-prefix convention (`/tools/{key}`, `/jobs/{key}`,
  `/connectors/{id}`). Layak di-rename jadi `EntityTag` dgn dedicated
  `entity_path` / `entity_type`?
- **OAuth audit trail.** `oauth_tokens.last_used_at` di-stamp tiap
  validate, tapi gak ada per-call audit log (mirip `connector_runs`).
  Cukup, atau perlu tabel `oauth_token_uses`?

---

## 11. Glosarium

- **Definisi connector / Module** — modul Go yg didaftarkan saat boot.
  Satu per API eksternal (Loki, Jira, Slack, ...). Carry shared
  `Configs` + N `Operations`.
- **Connector row** — row di tabel `connectors`. Memasangkan definisi
  (lewat `Key`) dgn nilai configs, label, dan tag.
- **Operation** — aksi terdeklarasi di Module (`query`, `list_repos`).
  Per-row punya enable toggle. `Destructive=true` default off.
- **Tag filter** — `Tag` dgn `IsFilter=true`. Dicocokkan antara row
  (via `ToolTag` path `/connectors/{id}`) dan user (via `UserTag`)
  untuk gating akses. Tag string bebas — admin-defined, gak ada
  konvensi prefix wajib di code.
- **MCP tool** — yg dilihat client LLM di `tools/list`. Selalu 4
  entry tetap: `wick_list`, `wick_search`, `wick_get`,
  `wick_execute`. Connector row × op direpresentasikan secara internal
  via **tool_id** `conn:{connector_id}/{op_key}`, bukan nama tool
  eksplisit. LLM discover via `wick_list`/`wick_search` dan execute
  via `wick_execute`.
- **ConnectorRun** — satu eksekusi (MCP, panel-test, atau retry).
  Catat input, response, latency, status, IP, UA, parent (kalau
  retry). Diretensi (default 7 hari).
- **Streamable HTTP** — transport MCP terkini. Endpoint tunggal,
  default JSON, bisa upgrade ke SSE per response kalau perlu.
- **Static Bearer (PAT)** — token yg user generate manual di
  /profile/tokens, formatnya `wick_pat_<32hex>`. Alternatif OAuth
  buat client yg gak speak OAuth flow (Claude Desktop, Cursor, cURL).
  Hash-only stored di `personal_access_tokens`.
- **OAuth grant** — pasangan (access + refresh token) yg di-mint saat
  user approve consent di /oauth/authorize. Access `wick_oat_<32hex>`
  TTL 1h, refresh `wick_ort_<64hex>` TTL 30d dgn rotation tiap exchange.
  Disconnect lewat /profile/connections revoke semua token milik
  (user, client) sekaligus.
- **Dynamic Client Registration (DCR)** — RFC 7591. MCP client
  (Claude.ai dst) panggil `POST /oauth/register` tanpa pre-registration
  → wick mint `client_id` + simpan redirect URIs. Public clients only
  (no client_secret) — PKCE menggantikan secret per OAuth 2.1.
