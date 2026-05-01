# Connectors — Desain & State

Status: implemented (skeleton + service + persistence + run history). UI &
MCP server menyusul.
Update terakhir: 2026-05-01.

Dokumen ini mencatat desain **Connectors** — kelas modul ketiga di wick,
sejajar dengan Tools dan Jobs, dirancang khusus dikonsumsi LLM lewat MCP
(Model Context Protocol). State dibawah refleksi dari kode di
`pkg/connector/`, `internal/connectors/`, dan `internal/entity/connector.go`.

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

Tiga tabel: `connectors`, `connector_operations`, `connector_runs`.
Tag association **reuse `ToolTag`** dgn `ToolPath = "/connectors/{id}"` —
tidak ada tabel join baru.

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

| Skenario                                          | Cara setup                              |
|---------------------------------------------------|-----------------------------------------|
| Row pribadi user                                  | tag `user:<email>` (filter)             |
| Row dipakai bareng tim                            | tag `team:<slug>` (filter)              |
| Row template/admin-only                           | tag `role:admin` (filter)               |

Helper resolve tag user pakai middleware existing
(`login.GetUserTagIDs`). Konvensi prefix tag string: `user:<email>`,
`team:<slug>`, `role:<name>`, `env:<name>`.

---

## 6. Web UI

Dua surface utama.

### 6.1 Manajemen row

```
Connectors
└── Loki                                (1 modul Go, key="loki")
    ├── [+ New row]
    ├── Loki Prod     [user:yoga]              [test] [edit] [duplicate] [delete]
    ├── Loki Staging  [user:yoga, env:staging] [test] [edit] [duplicate] [delete]
    └── Loki Platform [team:platform]          [test] [edit] [duplicate] [delete]
        Operations:
          ✓ query              (enabled)
          ✓ list_apps          (enabled)
          ✗ delete_log [⚠]     (destructive — admin opt-in)
```

- **New row**: form di-render otomatis dari `Module.Configs`. User isi
  configs, label, dan pilih tag (`user:<self>` di-set otomatis). Cuma
  tag yg user sendiri carry yg bisa dipilih — supaya gak bikin row yg
  user sendiri gak bisa lihat.
- **Per-op toggle**: tiap row punya panel toggle on/off untuk setiap
  op yg di-declare di Module. Destructive ops default off + chip
  warning di UI.
- **Duplicate**: copy row → configs **direset** (form muncul, user
  re-isi). Tag dari source tidak diwarisin; cuma `user:<self>` yg
  di-set caller. Lihat 5.5.
- **Edit / Delete / Duplicate**: muncul ke semua user yg lihat row
  (siapa pun yg tag-nya match). Tidak ada konsep owner eksklusif —
  siapa yg punya akses, punya hak penuh. Audit trail via
  `connector_runs.user_id` + `ip_address`/`user_agent`.
- **Disable**: row-level off-switch (`Connector.Disabled`); hide dari
  MCP `tools/list` & list view tanpa harus delete.

Tag list ditampilkan sbg chip di sebelah label — sekaligus jadi filter
di list view (klik tag → list mengkerucut ke row yg carry tag itu).

### 6.2 Panel test (gaya Postman)

```
Loki Prod   [Test]
├── Operation: [query ▾]    (dropdown dari module.Operations yg enabled)
├── Form input              (auto dari op.Input)
│   query: [_______]
│   start: [_______]
├── [Run]
├── Preview request         method, URL, header, body
├── Preview response        status, latency, JSON tree
└── History                 last N runs (klik → reload form / [Retry])
```

- Memanggil `Service.Execute` dgn `Source=ConnectorRunSourceTest`.
  Path code yg sama dgn MCP `tools/call` — verifikasi behavior end-to-end.
- Riwayat berasal dari `connector_runs`, di-filter ke source=test +
  user_id pemanggil.
- `[Retry]` di history row → panggil `Service.Retry(originalRunID, ...)`,
  rebuild call dari `RequestJSON` orig + configs row sekarang
  (cred edit yg admin lakukan di antara replay = honored).

---

## 7. Eksposur MCP

### 7.1 Transport

**Streamable HTTP** (endpoint tunggal). Alasan:

- Wick = server remote (bukan child process stdio lokal).
- Streamable HTTP menggantikan transport SSE legacy di spec MCP
  versi sekarang.
- Mayoritas request-response — JSON cukup buat 90% connector.
  Upgrade SSE disisakan buat call long-running.

### 7.2 Surface endpoint

```
POST /mcp                                  -- request/response JSON-RPC 2.0
GET  /mcp                                  -- stream notifikasi SSE (opsional)
GET  /.well-known/oauth-protected-resource -- metadata auth
```

### 7.3 Mapping row × operation → MCP tool

```go
// 1 row × N enabled ops = N MCP tools.
// Disabled rows + disabled ops + tag-mismatched rows di-skip.
func (r *MCPRegistry) List(ctx context.Context) []MCPTool {
    tagIDs := login.GetUserTagIDs(ctx) // user pemanggil
    rows := r.repo.ListVisibleTo(ctx, tagIDs) // tag-filtered, !Disabled

    var out []MCPTool
    for _, row := range rows {
        mod, ok := r.modules[row.Key]
        if !ok { continue } // row deactivated (no module registered)
        states, _ := r.svc.OperationStates(ctx, row.ID, row.Key)
        for _, op := range mod.Operations {
            if !states[op.Key] { continue }
            out = append(out, MCPTool{
                Name:        toolName(row, op),
                Description: opDescription(mod, op, row),
                InputSchema: configsToJSONSchema(op.Input),
                Handler:     dispatch(row.ID, op.Key),
            })
        }
    }
    return out
}

func toolName(row Connector, op Operation) string {
    // 3-part: {connector_key}__{op_key}__{label_slug}
    return row.Key + "__" + op.Key + "__" + slug(row.Label)
}
```

`ListVisibleTo` query: `SELECT connectors JOIN tool_tags JOIN tags
WHERE tool_tags.tool_path = '/connectors/'||id AND (tags.is_filter = false
OR tag_id IN (tagIDs))`. Logika identik dgn cara Tools Private resolve
akses, cuma path-prefix-nya beda.

Contoh: connector `loki` punya 2 ops (`query`, `list_apps`) × 3 row yg
user "yoga" punya akses (`Prod`, `Staging`, `Dev`) = **6 MCP tool**:

```
loki__query__prod        loki__list_apps__prod
loki__query__staging     loki__list_apps__staging
loki__query__dev         loki__list_apps__dev
```

Kalau ada bentrok label (jarang, tapi mungkin krn tag bareng tim),
append suffix `__<short-id>`.

Saat `tools/call` masuk, server resolve nama tool balik ke
`(connector_id, op_key)`, cek ulang tag user vs tag row +
`connector_operations` enable state (double-check, jangan trust list
cache), terus panggil `Service.Execute` dgn
`Source=ConnectorRunSourceMCP` — IP & UA dari request masuk ke
`connector_runs` row.

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
- Server perlu push `notifications/tools/list_changed` setelah admin
  add/remove row atau toggle operation via web UI (butuh stream
  long-lived `GET /mcp`).

---

## 8. Auth

OAuth 2.1 / SSO, sesuai spec MCP buat server remote.

### 8.1 Flow

```
1. Claude Desktop → POST /mcp  (tanpa token)
2. Wick → 401 + WWW-Authenticate: Bearer resource_metadata="..."
3. Client fetch /.well-known/oauth-protected-resource
4. Client buka browser → login SSO (Google / Microsoft / dll)
5. Callback → access_token (Bearer)
6. Client retry POST /mcp  Authorization: Bearer <token>
7. Wick validasi → resolve user_id → scope row connector lewat tag
```

### 8.2 Lokasi auth server

Dua opsi:

- **Self-hosted**: wick implement `/oauth/authorize`,
  `/oauth/callback`, `/oauth/token`, federasi ke Google/MS via OIDC.
- **Delegasi**: provider eksternal (Auth0, Clerk, Keycloak) yg expose
  endpoint OAuth; wick cuma validate bearer token.

Rekomendasi: **delegasi**. Implementasi OAuth yg spec-compliant
(PKCE, refresh, revocation, dynamic client registration) itu sub-
proyek tersendiri yg berat; serahkan ke provider.

### 8.3 Mode token

Endpoint `/mcp` terima dua format token, dibedakan oleh format string:

| Mode             | Contoh token              | Validator                    | Audience                              |
|------------------|---------------------------|------------------------------|---------------------------------------|
| **OAuth 2.1**    | `eyJhbGc...` (JWT 3-part) | verify signature + claims    | Claude.ai, Claude Desktop, Cursor     |
| **Static Bearer** | `wick_pat_xxx`           | hash lookup di DB            | dev, CLI, automation user, internal   |

OAuth 2.1 wajib lengkap PKCE + Dynamic Client Registration (RFC 7591)
+ metadata Authorization Server (RFC 8414) + Protected Resource
(RFC 9728). Refresh token rotasi otomatis.

Static bearer = mirip GitHub PAT — user generate di UI wick, disimpan
hash-nya, kirim via `Authorization: Bearer wick_pat_xxx`. Useful buat
client custom yg gak butuh login interaktif.

Middleware unified pakai prefix / format detection buat route ke
validator yg sesuai. Dua mode coexist tanpa endpoint terpisah.

### 8.4 Isolasi & sharing per user

| Resource                      | Scope                                                  |
|-------------------------------|--------------------------------------------------------|
| Definisi connector (Module)   | global (kode Go, semua user lihat template sama)       |
| Connector row                 | gating via tag filter (`UserTag` ↔ `ToolTag` row)      |
| Operation enable state        | per row (`connector_operations`)                       |
| Session MCP                   | per user (terikat token)                               |
| `connector_runs`              | per user pemanggil; admin bisa lihat semua             |
| Eksekusi MCP `tools/call`     | dicek ulang tag user + op enable state setiap call     |
| IP/UA per call                | dicatat di `connector_runs.ip_address`/`user_agent`    |

Konsekuensi: tidak semua user lihat semua row. Tidak ada konsep
"public" — semua row authenticated. Sharing antar user/tim dilakukan
dgn assign tag yg sama (mis. admin link tag `team:platform` ke row +
ke user-user yg perlu akses).

### 8.5 Sketsa middleware

```go
func MCPAuth(v TokenValidator) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            tok := extractBearer(r)
            if tok == "" {
                w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="..."`)
                http.Error(w, "unauthorized", 401)
                return
            }
            claims, err := v.Validate(r.Context(), tok)
            if err != nil {
                http.Error(w, "invalid token", 401)
                return
            }
            ctx := context.WithValue(r.Context(), userIDKey, claims.Sub)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

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

3. **Web UI** *(in progress / next)*
   - Handler CRUD + form auto-render dari `Module.Configs`.
   - Per-row per-op toggle panel.
   - Duplicate / edit / delete / disable.

4. **Panel test**
   - Handler `/connectors/{id}/test`.
   - Viewer request/response gaya Postman, source=`test`.
   - History + retry button (panggil `Service.Retry`).

5. **Connector pertama**
   - `internal/connectors/loki/` jadi pilot, validate ergonomi
     response-shape di kasus nyata.

6. **MCP server**
   - Endpoint `POST /mcp`, dispatch JSON-RPC.
   - `tools/list` + `tools/call` di-bind ke `Service.Execute`,
     source=`mcp`.
   - Auth bearer (token statik dulu buat dev).

7. **SSO**
   - Ganti bearer statik jadi OAuth 2.1 + provider delegasi.
   - Resolve row per user via tag.

8. **Streaming + notification** *(opsional, kalau dibutuhkan)*
   - Stream SSE `GET /mcp`.
   - `notifications/tools/list_changed` saat row/op berubah.

9. **Convenience** *(belakangan)*
   - Import OpenAPI / Postman collection buat scaffold stub Go
     connector.
   - Cleanup job harian → `Service.PurgeOldRuns(retentionDays)`.

---

## 10. Pertanyaan terbuka

- **Gaya transformasi response.** Operation return struct Go bertipe
  (lalu wick `json.Marshal`), atau selalu return `map[string]any`?
  Bertipe lebih clean; map lebih fleksibel kalau bentuk upstream
  berubah-ubah.
- **Penyimpanan secret.** Encrypt field configs at rest di kolom
  `connectors.configs`? Pakai envelope encryption, atau cukup
  encryption di level DB? Konsisten dgn tabel `configs` lama.
- **Visibility definisi.** Apakah ada definisi connector yg admin-only
  (gak muncul di picker "+ New row" milik user)? Bisa di-gate lewat
  Module-level tag mirip `DefaultTags` Tool.
- **Rate limit.** Per user, per row, atau per connector? Client MCP
  bisa cukup chatty.
- **Penamaan di MCP.** `loki__query__prod` vs `loki.query.prod` —
  underscore-only paling aman lintas client tapi kurang cantik.
  Bentrok label antar tag-mate (mis. dua "Loki Default" beda team)
  → suffix `__<short-id>` atau cegah di UI?
- **Reset configs saat duplicate — full vs partial.** Sekarang full
  reset (`Configs = "{}"`). Field non-secret (URL endpoint) sering
  reusable; cuma yg `secret` yg harus re-isi. Partial-reset lebih
  ergonomis tapi butuh metadata `secret` konsisten di tag struct.
- **Tag tipe terstruktur.** Sekarang konvensi prefix string (`user:`,
  `team:`, `env:`). Cukup, atau perlu kolom `Type` di tabel `tags`?
- **Auto-create tag `user:<email>`.** Lazy saat user pertama bikin
  row, atau eager saat user di-approve admin? Lazy lebih bersih
  (gak ada tag yatim).
- **Generic entity-tag.** `ToolTag` sekarang dipakai Tools, Jobs,
  Connectors via path-prefix convention. Layak di-rename jadi
  `EntityTag` dgn dedicated `entity_path` / `entity_type`?

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
  untuk gating akses. Konvensi prefix: `user:<email>`, `team:<slug>`,
  `role:<name>`, `env:<name>`.
- **MCP tool** — yg dilihat client LLM. Di-generate dari (row × op)
  yg tagnya match dgn user pemanggil + op enabled, di
  `tools/list`. Format nama: `{key}__{op}__{label_slug}`.
- **ConnectorRun** — satu eksekusi (MCP, panel-test, atau retry).
  Catat input, response, latency, status, IP, UA, parent (kalau
  retry). Diretensi (default 7 hari).
- **Streamable HTTP** — transport MCP terkini. Endpoint tunggal,
  default JSON, bisa upgrade ke SSE per response kalau perlu.
- **Static Bearer (PAT)** — token yg user generate manual di UI
  wick, formatnya `wick_pat_xxx`. Alternatif OAuth buat client
  non-interaktif.
