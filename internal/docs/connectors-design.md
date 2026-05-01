# Connectors — Desain & State

Status: implemented (modul + persistence + MCP JSON-RPC + auth dual-mode
PAT & OAuth 2.1 + per-user grant management). Admin UI buat CRUD
connector row + panel test belum dibikin — sekarang admin pakai SQL
langsung atau bootstrap auto-row kosong.
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

Tiga surface — dua admin-facing buat manage connector (belum dibikin),
satu user-facing buat manage auth (sudah ada).

### 6.1 Manajemen row *(belum dibikin)*

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

### 6.2 Panel test (gaya Postman) *(belum dibikin)*

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

### 6.3 Profile area *(implemented)*

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
POST /mcp                                       -- JSON-RPC 2.0 (implemented)
GET  /mcp                                       -- stream SSE (belum, opsional)

-- Auth metadata (implemented)
GET  /.well-known/oauth-protected-resource      -- RFC 9728
GET  /.well-known/oauth-authorization-server    -- RFC 8414

-- OAuth 2.1 server (implemented)
POST /oauth/register                            -- DCR (RFC 7591)
GET  /oauth/authorize                           -- PKCE consent
POST /oauth/authorize                           -- consent submit
POST /oauth/token                               -- code exchange + refresh
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
     schema converter, slugify.
   - Endpoint `POST /mcp` dispatch `initialize`, `tools/list`,
     `tools/call`, `ping`.
   - `tools/list` & `tools/call` bind ke `connectors.Service.Execute`
     dgn `Source=ConnectorRunSourceMCP`. Tag-filtered per user.
   - Tool name format: `{key}__{op}__{label_slug}`.

5. **Auth** ✅
   - **PAT** di `internal/accesstoken/` — generate/revoke di
     /profile/tokens. Format `wick_pat_<32hex>`, hash-only stored.
   - **OAuth 2.1** di `internal/oauth/` — DCR + PKCE + refresh
     rotation + chain replay detection. Self-hosted (bukan delegasi).
   - Format `wick_oat_<32hex>` (access) + `wick_ort_<64hex>` (refresh),
     opaque (bukan JWT).
   - Per-user grant management di /profile/connections.
   - .well-known/* metadata + /oauth/{register,authorize,token}.

6. **Web UI admin** *(belum dibikin — gap UX terbesar)*
   - Page `/manager/connectors` buat CRUD row + form configs.
   - Per-row per-op toggle panel.
   - Duplicate / edit / delete / disable.
   - Sekarang admin pakai SQL langsung; bootstrap auto-bikin row
     kosong per Key biar gak harus INSERT manual.

7. **Panel test** *(belum dibikin)*
   - Handler `/connectors/{id}/test`.
   - Viewer request/response gaya Postman, source=`test`.
   - History + retry button (panggil `Service.Retry`).

8. **Streaming + notification** *(opsional, kalau dibutuhkan)*
   - Stream SSE `GET /mcp`.
   - `notifications/tools/list_changed` saat row/op berubah.

9. **Convenience** *(belakangan)*
   - Cleanup job harian → `Service.PurgeOldRuns(retentionDays)` +
     purge expired `oauth_authorization_codes` + `oauth_tokens`.
   - Admin view `/admin/oauth-clients` — list registered DCR clients
     + revoke (sekarang SQL only).
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
- **Penamaan di MCP.** Sekarang `loki__query__prod` (underscore-only,
  paling aman lintas client). Bentrok label antar tag-mate (mis. dua
  "Loki Default" beda team) — sekarang gak dicegah; bakal generate 2
  tool dgn nama sama → MCP client behavior undefined. Solusi: suffix
  `__<short-id>` di slug atau cegah duplicate label di UI.
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
- **MCP tool** — yg dilihat client LLM. Di-generate dari (row × op)
  yg tagnya match dgn user pemanggil + op enabled, di
  `tools/list`. Format nama: `{key}__{op}__{label_slug}`.
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
