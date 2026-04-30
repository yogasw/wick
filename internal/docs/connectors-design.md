# Connectors — Diskusi Desain

Status: draft / diskusi. Belum diimplementasi.
Update terakhir: 2026-04-30.

Dokumen ini mencatat desain yang sedang dibahas untuk **Connectors** —
konsep baru di wick yang sejajar dengan Tools dan Jobs, dirancang
khusus untuk dikonsumsi LLM lewat MCP (Model Context Protocol).

---

## 1. Latar belakang

Wick sudah punya dua jalur eksposur:

- **Tools** — untuk manusia, lewat web UI.
- **Jobs** — untuk scheduler, jalan di background.

Yang belum ada: cara rapi buat expose kapabilitas wick ke LLM client
(Claude Desktop, Cursor, custom agent). Tujuannya supaya LLM bisa
manggil hal seperti "ambil error log terbaru dari Loki", "lookup
issue Jira", "post ke Slack" lewat protokol standar, dengan auth
per user, dan bentuk response yang sepenuhnya dikontrol developer
(JSON ramping, bukan payload mentah dari upstream).

Protokol yang dipilih: **MCP**. Tapi mayoritas Tools yang ada
sekarang terlalu UI-heavy atau response-nya terlalu gemuk untuk
langsung diekspos. Jadi Connectors dibikin sebagai modul jenis baru,
bukan retrofit Tools.

---

## 2. Konsep

**Connector** = modul Go yang ditulis developer, bungkus satu API
eksternal khusus untuk dikonsumsi LLM.

- HTTP call, header, body, error handling — semua hardcoded di Go.
  Connectors **bukan** HTTP builder generik yang user-defined.
- Developer **mengontrol bentuk response**. Response mentah dari
  upstream di-parse dan ditransformasi jadi JSON ramping sebelum
  dibalikin ke LLM.
- Satu definisi connector bisa **diduplikat lewat web UI** jadi
  beberapa instance, masing-masing bawa credential sendiri. Satu
  Loki Connector bisa melayani instance `prod`, `staging`, `dev`
  bersamaan, masing-masing muncul sebagai tool berbeda di MCP
  `tools/list`.
- Akses instance pakai **tag filter** yang sama dgn Tools (sec. 8) —
  tiap instance punya tag (mis. `user:yoga@abc.com`, `team:platform`),
  dan endpoint MCP cuma expose instance yang tag-nya match dgn tag
  user pemanggil (lookup via `UserTag` yg udah ada). Tidak ada konsep
  "public" — semua instance authenticated.

> Mental model: Connectors itu untuk LLM seperti Tools untuk manusia.
> Pola yang sama ("bungkus sesuatu di modul wick"), tapi audience dan
> kontrak output-nya beda.

---

## 3. Perbandingan dengan Tools dan Jobs

| Aspek           | Tool                          | Job                     | Connector                            |
|-----------------|-------------------------------|-------------------------|--------------------------------------|
| Audience        | Manusia via web UI            | Scheduler               | LLM via MCP                          |
| Lokasi logika   | Go (dev-authored)             | Go (dev-authored)       | Go (dev-authored)                    |
| Output          | HTML / templ                  | side effect, log        | nilai Go terstruktur → JSON          |
| Instance        | duplikasi via Key             | 1 per job               | **N per connector** (row di DB)      |
| Scope config    | global per tool               | global per job          | **per instance**                     |
| UI              | workflow custom penuh         | tidak ada               | panel test generik (gaya Postman)    |
| Akses           | Private + tag filter          | n/a                     | selalu private + tag filter          |
| Auth            | session wick                  | n/a                     | bearer OAuth/SSO atau PAT            |

---

## 4. Bentuk modul

Mengikuti pola `pkg/tool/` (`Module`, `Configs`, `RegisterFunc`) yang
sudah ada, supaya mental model dan helper refleksi
(`entity.StructToConfigs`) tetap konsisten.

```go
// pkg/connector/connector.go
type Connector struct {
    Key         string // "loki-query"
    Name        string // "Loki Query"
    Description string // ditampilkan ke LLM di MCP tools/list
    Icon        string
}

type Module struct {
    Meta    Connector
    Configs []entity.Config // field credential, per-instance
    Input   []entity.Config // argumen dari LLM, jadi JSON Schema
    Execute ExecuteFunc     // HTTP + transform, dikontrol dev
}

type ExecuteFunc func(c *Ctx) (any, error)
```

`Ctx` menyediakan:

- `c.Cfg("token")` — nilai credential dari instance yang dipilih.
- `c.Input("query")` — argumen yang dikirim LLM.
- `c.HTTP` — http client yang sudah dikonfigurasi (timeout, retry).

Configs dan Input keduanya pakai refleksi struct-tag `wick:"..."`
yang sudah ada, supaya form dan JSON Schema bisa di-generate
otomatis:

```go
type LokiCreds struct {
    URL   string `wick:"url,required,placeholder=https://loki.example.com"`
    Token string `wick:"token,secret,required"`
}

type LokiInput struct {
    Query string `wick:"query,required,description=Query LogQL"`
    Start string `wick:"start,description=Timestamp RFC3339, opsional"`
}
```

---

## 5. Persistence

```
connector_instances
  id             string (uuid)
  connector_key  string  -- FK ke Meta.Key dari modul Go
  label          string  -- "Loki Prod"
  configs        jsonb   -- nilai credential, secret di-encrypt at rest
  enabled        bool
  created_at     timestamp
  updated_at     timestamp
```

Akses kontrol pakai sistem `Tag` + `UserTag` yang udah ada di wick
(dipakai Tools untuk Private + tag-filter). Tabel join baru meniru
`ToolTag`:

```
connector_instance_tags
  instance_id  string  -- FK ke connector_instances.id
  tag_id       string  -- FK ke tags.id (Tag table existing)
  PRIMARY KEY (instance_id, tag_id)
```

Konvensi nama tag pakai prefix `<type>:<value>`:

- `user:<email>` — instance milik user tertentu (auto-tag saat
  bikin / duplicate).
- `team:<slug>` — group tim, dishared lewat `UserTag` ke anggota.
- `env:<name>` — opsional, buat filtering UI/MCP (`prod`, `staging`).

Tag dengan `IsFilter=true` (atribut existing di tabel `tags`) yang
gating akses. Logikanya identik dgn Tools Private: kalau instance
punya tag filter, user pemanggil harus punya tag yang match (via
`UserTag`) supaya bisa lihat & pakai. Tag tanpa `IsFilter` cuma
kosmetik buat browsing.

Tabel opsional buat panel test:

```
connector_test_history
  id             string
  instance_id    string
  user_id        string  -- siapa yang trigger test
  request_json   jsonb
  response_json  jsonb
  status         int
  latency_ms     int
  created_at     timestamp
```

Definisi connector **tidak** disimpan di DB — itu kode Go, didaftarkan
saat boot lewat `app.RegisterConnector(...)`.

### 5.1 Model akses

Connector instance **selalu private** di level transport — endpoint
`/mcp` selalu butuh bearer token. Tidak ada konsep "public" /
anonymous; LLM client wajib authenticated.

Di dalam authenticated user, gating dilakukan dgn tag filter (sama
persis dgn Tools Private):

| Skenario                                          | Cara setup                              |
|---------------------------------------------------|-----------------------------------------|
| Instance pribadi user                             | tag `user:<email>` (filter, auto)       |
| Instance dipakai bareng tim                       | tag `team:<slug>` (filter)              |
| Instance template/admin-only                      | tag `role:admin` (filter)               |

User lihat instance kalau dia carry minimal satu tag filter yang
match. Helper resolve tag user pakai middleware existing
(`login.GetUserTagIDs`).

### 5.2 Seeding default instance

Saat connector definition di-register, modul boleh expose
`DefaultSeed` opsional (mirip pola `DefaultTags` di Tool):

```go
app.RegisterConnector(connector.Module{
    Meta:    lokiMeta,
    Configs: lokiCreds,
    Input:   lokiInput,
    Execute: lokiExec,
    DefaultSeed: &connector.Seed{
        Label:    "Loki Default",
        Tags:     []string{"role:admin"}, // filter tag — admin only
        Configs:  map[string]string{ /* dummy / placeholder */ },
    },
})
```

Aturan:

- Seed jalan **sekali**, saat boot pertama kalau belum ada instance
  apapun untuk connector itu. Kalau admin sudah pernah edit /
  delete, seed gak balik (sama prinsipnya kayak `DefaultTags`).
- Tag yang di-set di `Tags` di-link ke instance via
  `connector_instance_tags`. Tag baru di-bikin otomatis kalau
  belum ada (reuse `tags.Service.EnsureTag` style).
- Cred kosong / placeholder — admin re-isi setelah seed.

Kalau definition tidak punya `DefaultSeed`, gak ada instance otomatis
— admin bikin manual lewat UI.

### 5.3 Duplicate → reset configs

Duplicate selalu **reset credential**. Alasan: cred = sensitive
material, lebih aman selalu minta user re-isi.

Aturan duplicate:

- Row baru: instance kosong, `label` = "<source label> (copy)".
- `configs` = **kosong** (user re-isi via form). Field non-secret
  yang harmless (mis. URL endpoint) bisa di-copy → lihat open
  question.
- Tag otomatis: `user:<email-pemanggil>` di-link via
  `connector_instance_tags`. Tag dari source **tidak** di-warisin
  (anti-bocor: instance team-shared diduplikat user pribadi tetap
  jadi pribadi). Tag `user:<email>` ini di-create otomatis dgn
  `IsFilter=true` kalau belum ada, dan auto-link ke `UserTag` user
  pemanggil supaya dia bisa lihat instance-nya sendiri.

UI flow setelah duplicate:

```
[Duplicate] → redirect ke form edit instance baru
            → semua field cred kosong, ditandai "required"
            → user isi → save → ready dipakai
```

---

## 6. Web UI

Dua surface utama:

### 6.1 Manajemen instance

```
Connectors
└── Loki Query                       (1 modul Go)
    ├── [+ Instance baru]
    ├── Loki Prod     [user:yoga]              [test] [edit] [duplicate] [delete]
    ├── Loki Staging  [user:yoga, env:staging] [test] [edit] [duplicate] [delete]
    └── Loki Platform [team:platform]          [test] [edit] [duplicate] [delete]
```

- **Instance baru**: form di-render otomatis dari `Configs`. User
  isi credential, label, dan pilih tag (`user:<self>` di-set
  otomatis). Cuma tag yang user sendiri carry yang bisa dipilih,
  supaya user gak bikin instance yang dia sendiri gak bisa lihat.
- **Duplicate**: copy row → configs **direset** (form muncul, user
  re-isi cred). Tag dari source tidak diwarisi; cuma `user:<self>`
  yang di-set. Lihat section 5.3.
- **Edit / Delete / Duplicate**: muncul ke semua user yang lihat
  instance (siapa pun yg tag-nya match). Tidak ada konsep owner
  eksklusif — siapa yg punya akses, punya hak penuh. Audit trail
  via `connector_test_history.user_id`.
- **Test**: tombol jalanin handler manual; riwayat direkam atas
  nama user yg trigger.

Tag list ditampilkan sebagai chip di sebelah label — sekaligus jadi
filter di list view (klik tag → list mengkerucut ke instance yg
carry tag itu).

### 6.2 Panel test (gaya Postman)

```
Loki Prod   [Test]
├── Form input         (auto dari mod.Input)
│   query: [_______]
│   start: [_______]
├── [Run]
├── Preview request    method, URL, header, body
└── Preview response   status, latency, JSON tree
```

Func `Execute` Go yang sama dipanggil, cuma lewat handler manual,
bukan lewat MCP. Ini view "Postman buat connector" — buat dev dan
user verifikasi bentuk response sebelum LLM dilepas memanggilnya.

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

### 7.3 Mapping instance → MCP tool

```go
func (r *MCPRegistry) List(ctx context.Context) []MCPTool {
    // tagIDs = tag yang user pemanggil carry, hasil resolve auth
    // middleware (mirror login.GetUserTagIDs).
    tagIDs := login.GetUserTagIDs(ctx)

    var out []MCPTool
    for _, inst := range r.repo.ListVisibleTo(ctx, tagIDs) {
        mod := r.modules[inst.ConnectorKey]
        out = append(out, MCPTool{
            Name:        toolName(inst),
            Description: toolDescription(mod, inst),
            InputSchema: configsToJSONSchema(mod.Input),
            Handler:     bindHandler(mod.Execute, inst.Configs),
        })
    }
    return out
}

func toolName(inst Instance) string {
    return inst.ConnectorKey + "__" + slug(inst.Label)
}
```

`ListVisibleTo` query: `SELECT instances JOIN connector_instance_tags
JOIN tags WHERE tags.is_filter = false OR tag_id IN (tagIDs)`. Logika
identik dgn cara Tools Private resolve akses via `ToolTag` + `UserTag`.

Modul Go `loki-query` + 3 instance yg user "yoga" punya akses
= 3 MCP tool: `loki_query__prod`, `loki_query__staging`,
`loki_query__dev`. Kalau ada bentrok label (jarang, tapi mungkin
karena tag bareng tim), append suffix `__<short-id>`.

Saat `tools/call` masuk, server resolve nama tool balik ke
`instance_id`, cek ulang tag user vs tag instance (double-check,
jangan trust list cache), terus eksekusi.

### 7.4 Session

- `Mcp-Session-Id` di-generate saat call `initialize` pertama.
- Disimpan **in-memory** (struct kecil: client capabilities, user_id,
  created_at). Tidak persist ke DB.
- Saat server restart, session hilang; client `initialize` ulang dan
  dapat session id baru. Transparent buat user.
- Auth (section berikutnya) yang load-bearing identitas — session
  cuma marker handshake protokol.

### 7.5 Streaming, kapan dipakai

Default: `Content-Type: application/json`, single response.

Pindah ke `Content-Type: text/event-stream` cuma kalau:

- Run connector diperkirakan > 5 detik dan butuh event progress.
- Server perlu push `notifications/tools/list_changed` setelah user
  add/remove instance via web UI (butuh stream long-lived
  `GET /mcp`).

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
7. Wick validasi → resolve user_id → scope instance connector
```

### 8.2 Lokasi auth server

Dua opsi:

- **Self-hosted**: wick implement `/oauth/authorize`,
  `/oauth/callback`, `/oauth/token`, federasi ke Google/MS via OIDC.
- **Delegasi**: provider eksternal (Auth0, Clerk, Keycloak) yang
  expose endpoint OAuth; wick cuma validate bearer token.

Rekomendasi: **delegasi**. Implementasi OAuth yang spec-compliant
(PKCE, refresh, revocation, dynamic client registration) itu sub-
proyek tersendiri yang berat; serahkan ke provider.

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
client custom yang gak butuh login interaktif.

Middleware unified pakai prefix / format detection buat route ke
validator yang sesuai. Dua mode coexist tanpa endpoint terpisah.

### 8.4 Isolasi & sharing per user

| Resource                | Scope                                                |
|-------------------------|------------------------------------------------------|
| Definisi connector      | global (kode Go, semua user lihat template sama)     |
| Instance connector      | gating via tag filter (`UserTag` ↔ instance tag)     |
| Session MCP             | per user (terikat token)                             |
| Riwayat test            | per user yang trigger (siapa pun yang punya akses)   |
| Eksekusi MCP `tools/call` | dicek ulang tag user vs tag instance setiap call   |

Konsekuensi: tidak semua user lihat semua connector instance. Tidak
ada konsep "public" — semua instance authenticated. Sharing antar
user/tim dilakukan dgn assign tag yang sama (mis. admin link tag
`team:platform` ke instance + user-user yg perlu akses).

### 8.4 Sketsa middleware

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

1. **Skeleton**
   - `pkg/connector/` — `Connector`, `Module`, `ExecuteFunc`, `Ctx`.
   - `app.RegisterConnector(meta, Cred{}, Input{}, Execute)`.
   - Registry in-memory buat definisi connector.

2. **Persistence + UI**
   - Migration buat `connector_instances`.
   - Handler CRUD + form auto-render dari `Configs`.
   - Duplicate / edit / delete.

3. **Panel test**
   - Handler `/connectors/{key}/instances/{id}/test`.
   - Viewer request/response gaya Postman.
   - `connector_test_history` opsional.

4. **Connector pertama**
   - `internal/connectors/loki/` jadi pilot.
   - Validasi ergonomi response-shape di kasus nyata.

5. **MCP server**
   - Endpoint `POST /mcp`, dispatch JSON-RPC.
   - `tools/list` + `tools/call` di-bind ke registry.
   - Auth bearer (token statik dulu buat dev).

6. **SSO**
   - Ganti bearer statik jadi OAuth 2.1 + provider delegasi.
   - Resolve instance per user.

7. **Streaming + notification** *(opsional, kalau dibutuhkan)*
   - Stream SSE `GET /mcp`.
   - `notifications/tools/list_changed` saat instance add/remove.

8. **Convenience** *(belakangan)*
   - Import OpenAPI / Postman collection buat scaffold stub Go
     connector.

---

## 10. Pertanyaan terbuka

- **Gaya transformasi response.** Connector return struct Go
  bertipe (lalu wick `json.Marshal`), atau selalu return
  `map[string]any`? Bertipe lebih clean; map lebih fleksibel kalau
  bentuk upstream berubah-ubah.
- **Penyimpanan secret.** Encrypt field credential at rest di
  jsonb `configs`? Pakai envelope encryption, atau cukup
  encryption di level DB?
- **Visibility definisi.** Apakah ada definisi connector yang
  admin-only (tidak muncul di picker "+ Instance baru" milik user)?
  Bisa di-gate lewat tag di Module-level mirip `DefaultTags` Tool.
- **Rate limit.** Per user, per instance, atau per connector?
  Client MCP bisa cukup chatty.
- **Penamaan di MCP.** `loki_query__prod` vs `loki-query.prod` —
  underscore-only paling aman lintas client tapi kurang cantik.
  Bentrok label antar tag-mate (mis. dua "Loki Default" beda team)
  → suffix `__<short-id>` atau cegah di UI?
- **Route definisi vs instance.** `/connectors/{key}/instances/...`
  meniru routing tool; alternatif: `/connectors/instances/{id}` yang
  flat kalau ada beberapa definisi yang share UI.
- **Auto-create tag `user:<email>`.** Kapan tag user di-create —
  lazy saat user pertama bikin instance, atau eager saat
  user di-approve admin? Lazy lebih bersih (gak ada tag yatim);
  eager mempermudah admin assign instance team langsung ke user.
- **Tag tipe terstruktur.** Sekarang konvensi prefix string
  (`user:`, `team:`, `env:`). Cukup, atau perlu kolom `Type` di
  tabel `tags`? Prefix string fleksibel & gak bikin migrasi schema;
  kolom struktur memungkinkan filter UI yg cleaner per-type.
- **Reset configs saat duplicate — full vs partial.** Reset semua
  field, atau cuma field bertanda `secret`? Field non-secret kayak
  URL endpoint sering reusable; cred yang sensitif aja yang harus
  re-isi. Partial-reset lebih ergonomis tapi butuh metadata `secret`
  konsisten di tag struct.
- **Default seed lifecycle.** Sama kayak `DefaultTags` Tool — seed
  sekali di boot pertama, tidak balik kalau admin delete? Atau
  re-seed setiap deploy dengan flag "preserve admin edits"?

---

## 11. Glosarium

- **Definisi connector** — modul Go yang didaftarkan saat boot. Satu
  per API eksternal (Loki, Jira, Slack, ...).
- **Instance connector** — row di `connector_instances`. Memasangkan
  definisi dengan credential, label, dan tag.
- **Tag filter** — `Tag` dgn `IsFilter=true`. Dicocokkan antara
  instance (via `connector_instance_tags`) dan user (via `UserTag`)
  untuk gating akses. Konvensi prefix: `user:<email>`,
  `team:<slug>`, `role:<name>`, `env:<name>`.
- **MCP tool** — yang dilihat client LLM. Di-generate dari instance
  connector yang tag-nya match dgn tag user pemanggil saat call
  `tools/list`.
- **Streamable HTTP** — transport MCP terkini. Endpoint tunggal,
  default JSON, bisa upgrade ke SSE per response kalau perlu.
- **Static Bearer (PAT)** — token yang user generate manual di UI
  wick, formatnya `wick_pat_xxx`. Alternatif OAuth buat client non-
  interaktif.
