# Custom Connector — Add From cURL / MCP / Form (design)

Status: **in progress — backend + manager UI lengkap 2026-06-12**
(branch `feat/custom-connectors`; semua flow A/B/C, OAuth per-instance,
ownership contract, management connector `custom-connector` shipped —
lihat checklist §13 dan update notes di bawah). Update terakhir:
2026-06-12.

**Paradigm:** built-in connectors (Go code under `internal/connectors/*`)
tetap canonical. Di atasnya, tambah jalur **custom connector** yang dibuat
admin via UI — runtime "executor generic" yang baca definisi connector
dari DB (bukan dari `RegisterBuiltins`). Tiga jalur input definisi:

1. **From paste (cURL parser + AI parser)** — satu paste box, dua
   parser di belakangnya. **cURL parser** deterministic (regex grammar
   — fast, no LLM call, no token spend) buat real cURL strings.
   **AI parser** fallback buat paste apa pun selain cURL (raw API docs,
   `fetch()` snippet, axios call, Postman blob) — LLM ekstrak ke
   bentuk yang sama. Hasil keduanya masuk ke review form yang sama
   (`Configs` + per-op `Input`).
2. **From MCP server** — admin daftarin satu MCP server (URL streamable
   HTTP + headers). Wick = **forwarder/proxy**: simpan URL + auth
   headers, no process spawn. **1 server = 1 connector** — save form
   langsung melahirkan connector; **semua** tool hasil `tools/list`
   jadi Operation otomatis (ops tidak pernah dipersist — tiap module
   build re-probe live, jadi tool baru di server muncul sendiri setelah
   re-sync/reload). Kontrolnya **exclude list** (opt-out) di form
   server, bukan pilih-import (opt-in). Input schema dipetakan dari MCP
   `inputSchema`. Stdio (npx/python spawn) **bukan v1** — wick gak jadi
   process supervisor.
3. **Manual form builder** — admin bangun connector + op dari form
   kosong (rare path, tapi penting buat APIs tanpa cURL/MCP spec).

Semua jalur ujungnya **rapat** dengan kontrak `connector-module` skill:
Meta, Configs, Operations, per-op Input. Bedanya cuma sumber: built-in
dari Go reflect, custom dari satu row `custom_connectors` (ops embedded
di kolom JSON `ops`). Dari sudut MCP (`wick_list` / `wick_execute`)
two paths look identical — same `tool_id` shape, same audit trail, same
encrypted-fields layer.

Paired mockup: [`mockup.html`](mockup.html). Update keduanya barengan.

> **Update 2026-06-11 — keputusan final storage.** Implementasi pakai
> **satu tabel** `custom_connectors`: ops embedded sebagai kolom JSON
> `ops` (array of `custom.DefOp`), configs sebagai JSON `configs`
> (array of `custom.DefField`), provenance di JSON `source_meta`
> (`{category, server_id}`). **Tidak ada** tabel `connector_def_ops`,
> dan **tidak ada** `tools_cache` — setiap module build re-hit
> `tools/list` live. Entities: `entity.CustomConnector` +
> `entity.CustomConnectorMCPServer` (tabel
> `custom_connector_mcp_servers`). Package `internal/connectors/custom`;
> registry `Meta.Key` = def key langsung (bukan `custom:<id>`); module
> `Meta.Fixed=true` (1 def = 1 instance di v1). Migration via gorm
> `AutoMigrate` di `internal/pkg/postgres/migrate.go` — no SQL migration
> files. Sisa mention `connector_defs` / `connector_def_ops` /
> `custom_mcp_servers` di bagian bawah dokumen dibaca sebagai: row
> `custom_connectors` / elemen array `ops`-nya /
> `custom_connector_mcp_servers`. Detail final di §3.

> **Update 2026-06-12 — MCP flow disederhanakan: live ops + exclude
> list.** Import picker dan list page MCP server **dihapus**. Model
> baru: **1 server row = 1 connector**. Register form (label, URL,
> auth, test) → save → connector langsung jadi (def `ops` kolom tetap
> `"[]"` selamanya untuk source=mcp). `BuildModule` re-probe
> `tools/list` live tiap build (boot / Reload / save server) dan map
> **semua** tool minus `excluded_tools` (kolom baru di
> `custom_connector_mcp_servers`, JSON array nama tool, dikelola via
> exclude-checkbox di form server). Konsekuensi: tool baru di server
> otomatis ke-expose setelah re-sync, tanpa edit wick. Edit definition
> untuk def MCP redirect ke form server (nama connector sync dari
> label); delete definition cascade hapus server row. Probe sso-scheme
> saat boot pakai identity sintetis `system:wick` (per-call tetap
> identity user asli). Sisa mention "import picker" / "tools snapshot"
> di bawah dibaca sebagai model lama yang sudah diganti.

> **Update 2026-06-12 (2) — instance model + status.**
> - **Multi-instance by default** (`Meta.Fixed=false`): custom connector
>   diperlakukan persis built-in — `+ New row`, Duplicate, tiap row
>   credentials sendiri. Opt-out via checkbox "Single instance only" di
>   review form (kolom `single_instance`).
> - **No auto-seed — rule global, bukan flag**: `seedModuleRows` hanya
>   auto-create row pertama untuk module `Fixed` (UI sembunyikan
>   `+ New row` buat Fixed, jadi row tunggalnya wajib ada duluan).
>   Semua connector non-fixed — built-in maupun custom — mulai 0 row;
>   row dibuat eksplisit via `+ New row` dan setelah row terakhir
>   dihapus tidak muncul lagi sendiri pas restart. Redirect save →
>   halaman connector (list), bukan instance. Def custom
>   "single instance only" = Fixed → row tunggalnya di-seed saat save.
> - **Status MCP** di manager (list + instance header): chip
>   ● Connected / ● Disconnected dari probe terakhir (`last_test_ok`,
>   di-refresh tiap module build/re-sync), ● Never tested, ● Disabled.
>   Def cURL/manual tidak punya chip koneksi.
> - **Disable/Enable definition** (kolom `disabled` akhirnya di-wire):
>   def disabled tetap teregister tapi module-nya 0 ops — card, page,
>   instance rows tetap hidup, tidak ada yang listable/callable.
>   Re-enable rebuild penuh (re-probe untuk MCP). Danger zone di list
>   page memuat Disable/Enable + Delete.
> - ~~Deferred: ownership~~ → **shipped 2026-06-12, lihat note (5).**
>   Masih deferred: setting global `/admin/variables` "user boleh buat
>   custom connector" (default true), per-def setting "user lain boleh
>   nambah instance".

> **Update 2026-06-12 (5) — ownership contract (generik, siap dicopy
> ke jobs/tools).** Mekanisme: kolom `created_by` + tag — zero mesin
> baru.
> - **Level 1 — Definition** (server MCP row ikut, 1:1): create =
>   semua approved user (builder routes `RequireAuth`, bukan admin);
>   mutate (edit/save/reload/disable/delete) = **admin ∨ creator**
>   (`custom.CanMutate`, guard `requireDefMutable` di manager +
>   `mutableDef` di ops). Not-found dan not-yours sengaja sama (404).
>   Kontrol def di UI (badge/banner/danger/actions) cuma render buat
>   yang boleh mutate (`customDefInfo` gated per user).
> - **Level 2 — Instance**: creator non-admin auto-tag
>   `owner:<uuid>` di SEMUA jalur create (+ New row, instance pertama
>   save OAuth, op `instance_create`); configure = mekanisme existing
>   (owner/admin/"allow others"); Connect account dijaga
>   `canConfigureRow`.
> - **Level 3 — per-op**: enabled / destructive-default-off /
>   admin-only per (row × op) — framework existing.
> - **Level 4 — identitas downstream**: oauth = per instance, sso =
>   per pemanggil (JWT per call), bearer/header = shared per def.
> - **Ops `custom-connector` scoped, bukan admin-gated**: admin
>   kelola semua; caller lain hanya def `created_by` miliknya
>   (`def_list` terfilter; lookup def orang = not-found). Akses ke
>   connector management-nya sendiri tetap cerita tag (System default).

> **Update 2026-06-12 (3) — auth scheme `oauth` (MCP authorization
> spec, per-instance accounts).** Buat server MCP yang gate pakai
> `Authorization: Bearer` standar (401 `invalid_token`) — skema `sso`
> bukan jawabannya (itu khusus server in-house yang validasi pubkey
> wick).
> - **Discovery**: 401 → `WWW-Authenticate resource_metadata`
>   (RFC 9728) → AS metadata (RFC 8414 / OIDC fallback). **DCR**
>   (RFC 7591) otomatis saat form tidak diisi client_id; override
>   manual tersedia (client_id/secret/scopes).
> - **Login** = PKCE authorization-code via browser. Register form:
>   popup (Test now → login → probe pakai token → save). Session
>   in-flight in-memory TTL 10 menit, tidak dipersist.
> - **Token per-instance**: access/refresh/expiry disimpan sebagai
>   config rows owner `connector:<id>` (secret, hidden di Settings).
>   Refresh transparan saat expire (-30s skew). Client material
>   (client_id, secret encrypted, endpoints) di `AuthExtra` server row.
> - **Save (register)** = connector + instance pertama dengan akun
>   yang barusan login. Akun tambahan: tombol **Connect account →** di
>   instance page (full-page redirect, callback nempel token ke row +
>   auto re-sync def).
> - **Probe boot/re-sync** minjem token instance pertama yang punya
>   akun (row disabled di-skip); belum ada akun → 0 ops sampai
>   connect + re-sync.
> - **Lazy refresh di `wick_get`**: katalog def MCP re-sync otomatis
>   saat `wick_get` kalau module berumur > 30s (throttled per def,
>   claim-before-probe anti stampede). Probe gagal → katalog lama
>   tetap serve, ngak pernah ke-wipe. `wick_list` tetap snapshot
>   (cepat, zero network). Hook: `connectors.SetCatalogRefresh` →
>   `custom.RefreshIfStale`.
> - Routes: `POST …/mcp-servers/oauth/start`,
>   `GET …/mcp-servers/oauth/callback`, `POST …/mcp-servers/connect`.

> **Update 2026-06-12 (4) — management connector `custom-connector`.**
> Built-in connector (`internal/connectors/customconnector/`, Fixed,
> tag System+Connector, semua op gate ADMIN runtime) yang expose
> lifecycle UI sebagai MCP ops — LLM bisa bikin/kelola custom
> connector tanpa dashboard:
> `def_list/get/create/update/set_disabled/delete/resync`,
> `mcp_register` (test-gate sama; skema oauth ditolak → butuh browser,
> arahkan ke UI), `mcp_set_excluded`,
> `instance_list/create/delete/set_disabled` (guard: cuma row milik
> def custom — bukan side-door ke row built-in). **Tanpa op cURL** —
> LLM konversi cURL/doc API ke draft manual sendiri lalu `def_create`.

---

## Naming note (pilih nama folder sebelum implement)

Folder ini ditulis `custom-connector/` ngikutin wording user di chat.
Tiga kandidat nama yang bisa dipakai konsisten di code + docs + UI:

| Nama | Pro | Con |
|---|---|---|
| **`custom-connector`** *(default)* | Match wording user, jelas vs built-in | "Custom" agak generik di kosakata Go |
| `connector-builder` | Cocok kalau fokusnya tindakan "build" via UI | Kurang tegas di MCP-import path |
| `byoc` (Bring-Your-Own-Connector) | Catchy, branded | Singkatan asing buat new contributor |

Rekomendasi: pakai `custom-connector` di docs + UI label, tapi di package
Go pakai `internal/connectors/custom/` (singular, snake-friendly).
Disebut "custom connector" di copy UI dan "Custom" sebagai badge di
list.

---

## TODO

**Deferred (out of v1 scope):**

- ⏸ **OAuth provider non-Slack** — `internal/manager/oauth.go::oauthCallback`
  masih hardcode `slackgo.GetOAuthV2ResponseContext`. Custom connector
  v1 dukung **bearer / header / query** auth only. Standard OAuth 2.0
  authorize-code flow di-defer sampai callback di-generalize (lihat
  `connector-module` skill § OAuth caveats).
- ⏸ **Stdio MCP transport (npx/python spawn)** — bikin wick jadi
  process supervisor: lifecycle, idle timeout, respawn, npm/python
  dependency, arbitrary command exec surface. Beratnya gak sebanding
  dengan benefit-nya kalau user bisa wrap stdio server pake sidecar
  (`mcp-proxy` atau setara) dan expose lewat HTTP. V1 forwarder-only.
- ⏸ **OpenAPI / Swagger import** — paste OpenAPI URL → auto-generate
  N Operations dari paths. Dipertimbangkan, di-defer: parser besar,
  cURL + MCP udah cover 80% kasus.
- ⏸ **Edit connector live di-running** — v1: edit definisi → row
  jadi dirty, butuh "Reload" button untuk apply ke executor. Hot
  reload di-defer.
- ⏸ **Per-row override Configs** — built-in udah punya: tiap row punya
  Configs sendiri. Untuk custom, v1: 1 definisi = 1 row instance.
  Multi-row datang setelah v1 stabil.
- ⏸ **Response shaper / JSONPath transform** — biar LLM lihat shape
  bersih, bukan raw upstream. v1: passthrough JSON. Shaper di-defer.

**v1 locked decisions:**

- ✓ **Built-in tetap source of truth** — custom hanya tambahan, tidak
  replace `RegisterBuiltins`. Built-in `httprest` tetap ada (covers
  one-shot adhoc HTTP call); custom connector covers "saya pengen
  ngunci endpoint + op spesifik biar LLM ngga salah call".
- ✓ **Persisted di DB** — bukan file JSON di disk. Tabel baru
  `connector_defs` + `connector_def_ops`. Ikut migration framework.
- ✓ **Eksekusi via generic executor** — satu `Module` di-register di
  registry dengan `Key="custom:<def_id>"`. Operations dynamic dari
  rows. Bukan codegen.
- ✓ **MCP path = forwarder/proxy** — Flow B v1 hanya
  streamable-HTTP. Wick simpan `url + headers + auth_scheme` → tiap
  MCP-backed op jadi outbound HTTP JSON-RPC call. **Zero process
  supervision** di sisi wick. Sama codepath dengan HTTP connector
  (`http.NewRequestWithContext`).
- ✓ **MCP Save gated by Test connection** — minimal 1× berhasil
  `initialize` + `tools/list` sebelum row tersimpan. Mencegah half-
  broken row.
- ✓ **MCP auth schemes** — `none`, `bearer`, `custom_header`, `sso`
  (forward caller identity via signed JWT). SSO untuk per-user RBAC
  di downstream MCP tanpa shared secret.
- ✓ **Paste = dua parser** — `cURL` (regex, default, $0) + `AI`
  (LLM, fallback untuk format lain). Sama review form. AI tab
  auto-hidden kalau gak ada provider configured. Raw paste tidak
  pernah disimpan.
- ✓ **Encrypted-fields layer dipakai** — header value bertanda secret,
  body field bertanda secret, semua otomatis decrypt-then-mask via
  `secret` tag (sama path dengan built-in).
- ✓ **Audit trail seragam** — `connector_runs` tabel udah ada,
  custom executor write rows yang sama. History page reuse persis.
- ✓ **Admin-only create/edit** — gating sama dengan
  `RequireToolAccess` / `IsAdmin`. Non-admin lihat instance kalau
  punya tag access (sama dengan built-in).
- ✓ **Destructive flag tetap opt-in** — admin centang per Operation
  pas building; default off di tiap row (sama dengan
  `OpDestructive`).

---

## 1. Tujuan & non-goal

**Tujuan:**

- Admin bisa nambah connector baru tanpa nulis Go code + recompile +
  redeploy.
- Tetap pakai infrastruktur existing: encrypted-fields, connector_runs
  audit, tags ACL, MCP `wick_execute` dispatch, `wick_get` schema.
- Tiga path import (cURL, MCP, manual form) supaya user nggak ngetik
  schema dari nol kecuali kepaksa.
- UI/UX masuk ke `/manager/connectors` index — satu tombol "+ New
  custom" + dropdown source (cURL / MCP / blank).

**Non-goal:**

- Bukan **runtime plugin loader** (no `.so`, no WASM). Eksekusi via
  generic Go function yang baca JSON definisi — sandbox masih process
  utama, gak boost beyond apa yang built-in udah bisa.
- Bukan **multi-environment per row** lebih dulu — v1 satu Configs
  per definisi (sama seperti pasangan Meta+Configs di built-in
  module). Multi-row datang setelah pattern stabil.
- Bukan **scripting** — gak ada eval JS/Lua untuk transform body /
  response. Body templating fix (Go `text/template` dgn whitelist
  funcs) saja.
- Bukan **replacement** untuk built-in modules. Built-in lebih cepat
  +  bisa do hal yg parser cURL gak bisa cover (paging, retry, custom
  health-check). Custom adalah "Quick Win" path.

---

## 2. Konsep & terminologi

```
ConnectorDef (custom)
├─ Meta            — Key, Name, Description, Icon (admin-set)
├─ Configs[]       — list of named fields (URL, secrets, etc.)
├─ Operations[]    — list of custom ops
│   ├─ Meta        — Key, Name, Description, Destructive flag
│   ├─ Input[]     — list of named fields (path, query, body…)
│   ├─ Request     — method, URL template, headers map, body template
│   └─ Response    — passthrough (v1) / typed sketch (v2)
└─ Source          — "curl" | "mcp" | "manual"
```

| Term | Arti | Catatan |
|---|---|---|
| **Definition** | Definisi connector custom (1 row `connector_defs`) | Bukan instance — definisi di-instantiate jadi row di `connectors` table seperti built-in |
| **Source** | Asal definisi (cURL / MCP / manual) | Display-only; behavior eksekutor sama |
| **Generic executor** | Satu `ExecuteFunc` yang baca op definition + Configs/Input dan jalanin HTTP | Live di `internal/connectors/custom/repo.go` |
| **MCP-backed op** | Operation yang ekekusinya proxy ke MCP server external | v1 forward JSON-RPC; tetap pakai `connector_runs` |

**Hubungan ke built-in:**

```
RegisterBuiltins() (existing)
  └─ github, slack, loki, httprest, …       (Go-defined modules)

bootstrapCustomDefs() (NEW, called dari registry.Register dengan keys "custom:<id>")
  └─ for each row in connector_defs:
       build connector.Module{
         Meta:       def.Meta,
         Configs:    def.ConfigsAsStruct(),
         Operations: def.OperationsAsArray(),
       }
       extra = append(extra, ...)
```

---

## 3. Storage layout

**Final (as implemented 2026-06-11):** satu definisi = **satu row**.
Ops tidak punya tabel sendiri — embedded sebagai JSON array di kolom
`ops`. Dua gorm entities, ditambahkan ke `db.AutoMigrate(...)` di
`internal/pkg/postgres/migrate.go` (no SQL migration files).

### 3.1 Tables

**`custom_connectors` ← `entity.CustomConnector`**
(`internal/entity/custom_connector.go`)

| Kolom | Go field | Catatan |
|---|---|---|
| `id` | `ID` | uuid (BeforeCreate) |
| `key` | `Key` | unique slug, share namespace dengan built-in `Meta.Key`; **immutable** setelah create |
| `name` | `Name` | display name |
| `description` | `Description` | shown in index card |
| `icon` | `Icon` | default `🔌` |
| `source` | `Source` | `entity.CustomConnectorSource`: `curl` \| `mcp` \| `manual` — display-only |
| `source_meta` | `SourceMeta` | JSON `custom.SourceMeta` `{category, server_id}` — provenance only, **raw paste tidak pernah disimpan** |
| `configs` | `Configs` | JSON array of `custom.DefField` |
| `ops` | `Ops` | JSON array of `custom.DefOp`; array order = display order |
| `created_by` | `CreatedBy` | admin user.id |
| `disabled` | `Disabled` | skip registrasi at boot |
| `created_at` / `updated_at` | — | `UpdatedAt` vs in-memory `loadedAt` drives the dirty/Reload banner |

**`custom_connector_mcp_servers` ← `entity.CustomConnectorMCPServer`**

| Kolom | Go field | Catatan |
|---|---|---|
| `id` | `ID` | uuid |
| `label` | `Label` | display name |
| `transport` | `Transport` | `'http'` only di v1; kolom reserved buat future stdio |
| `url` | `URL` | streamable-HTTP endpoint |
| `auth_scheme` | `AuthScheme` | `none` \| `bearer` \| `custom_header` \| `sso` |
| `auth_secret` | `AuthSecret` | bearer token, encrypted (`wick_enc_`) under master key via `configs.EncryptSecret` |
| `auth_headers` | `AuthHeaders` | JSON `[]custom.HeaderRow` `{key, value, secret}` — scheme `custom_header`; secret values encrypted |
| `auth_extra` | `AuthExtra` | JSON `custom.SSOExtra` `{audience, ttl_seconds}` — scheme `sso` |
| `headers` | `Headers` | extra non-auth headers, sama shape `[]HeaderRow`, appended on top of scheme headers |
| `last_test_at` / `last_test_ok` | `LastTestAt`/`LastTestOK` | save gate: row hanya tersimpan setelah ≥1 sukses `initialize` + `tools/list` |

> ~~`tools_cache`~~ — **dihapus.** Tool catalog tidak pernah di-cache;
> import picker re-hit `tools/list` live tiap load supaya admin selalu
> lihat surface terkini.

**Reuse existing tables (unchanged):**

- `connectors` (instance rows) — satu instance per def di v1
  (`Meta.Fixed=true`), auto-seeded via `connectors.Service.Bootstrap` /
  `UpsertModule`.
- `connector_operations` (per-op enabled state, admin-only) — sama.
- `connector_runs` (audit) — sama; eksekusi flow lewat
  `connectors.Service.Execute` persis seperti built-in.
- `configs` (per-instance config values) — sama,
  `owner="connector:<instance_id>"`. Plus satu row khusus:
  `owner="custom_connector"`, `key="sso_signing_key"` — ED25519 seed
  buat SSO signer, encrypted under master key.

### 3.2 JSON shapes

Semua shape didefinisikan di `internal/connectors/custom/schema.go` dan
di-decode tanpa Go reflection (`ParseFields` / `ParseOps` /
`ParseSourceMeta`).

**`custom_connectors.configs`** — array of `custom.DefField`
(`{key, label, widget, options, secret, required, default, desc}`,
mirror subset `entity.Config` yang `entity.StructToConfigs` hasilkan):

```json
[
  {
    "key": "base_url",
    "label": "Base URL",
    "widget": "url",
    "required": true,
    "default": "https://api.example.com",
    "desc": "API base URL. Example: https://api.example.com"
  },
  {
    "key": "auth_value",
    "label": "Authorization",
    "widget": "secret",
    "secret": true,
    "required": true,
    "desc": "Value for the `Authorization` header. Stored encrypted."
  }
]
```

Allowed widgets: `text`, `textarea`, `dropdown` (+`options` `"a|b|c"`),
`number`, `checkbox`/`bool`, `secret`, `email`, `url`, `date`,
`datetime`. Keys snake_case, no duplicates (`ValidateFields`).

**`custom_connectors.ops`** — array of `custom.DefOp`. Per op, exactly
one of `request` (HTTP path) / `mcp_source` (proxy path) is set;
`inputs` sama shape dengan `configs`:

```json
[
  {
    "key": "post_charges",
    "name": "Post Charges",
    "description": "POST /v1/charges on api.example.com. Returns the upstream JSON response as-is.",
    "destructive": false,
    "inputs": [
      { "key": "amount", "widget": "number", "required": true, "desc": "Body field `amount`. Example: 2000" },
      { "key": "currency", "widget": "text", "required": true, "default": "usd", "desc": "Body field `currency`." }
    ],
    "request": {
      "method": "POST",
      "url_template": "{{.cfg.base_url}}/v1/charges",
      "headers": { "Authorization": "Bearer {{.cfg.auth_value}}" },
      "body_template": "amount={{urlquery .in.amount}}&currency={{urlquery .in.currency}}",
      "content_type": "application/x-www-form-urlencoded"
    }
  },
  {
    "key": "search_docs",
    "name": "Search Docs",
    "description": "Proxy of MCP tool search_docs on Internal Tools MCP.",
    "destructive": false,
    "inputs": [
      { "key": "query", "widget": "text", "required": true }
    ],
    "mcp_source": { "server_id": "<custom_connector_mcp_servers.id>", "tool_name": "search_docs" }
  }
]
```

**`custom_connectors.source_meta`** — `custom.SourceMeta`:

```json
{ "category": "Development", "server_id": "<uuid, source=mcp only>" }
```

**Templating rules (final — `template.go`):**

- Go `text/template` dengan `.cfg.<key>` dan `.in.<key>` namespaces;
  berlaku untuk `url_template`, semua header values, dan
  `body_template`.
- Custom funcs whitelist: `default`, `lower`, `upper`, `b64` (Basic
  auth recipes). Plus safe builtins: `urlquery`, `js`, `printf`.
  **No `exec`, no shell, no file read** — text/template memang tidak
  punya builtin berbahaya.
- `missingkey=error` — typo `{{.cfg.api_keyy}}` jadi error jelas di
  `connector_runs.error_msg`, bukan `<no value>` nyasar ke upstream.
- Rendered output capped **1 MB** per template; rendered URL wajib
  `http(s)://`.
- Template errors → returned via `connector_runs.error_msg`, not
  panic.

---

## 4. Operations — Flow A: from paste (cURL + AI parser)

Most common path. Two parsers behind one paste box. Default = cURL
(deterministic, no LLM call). Fallback = AI parser for everything
else.

### 4.0 Tab toggle UX

`/manager/connectors/custom/new/paste` opens with two tabs:

| Tab | When to pick | Cost |
|---|---|---|
| **cURL parser** *(default)* | You have a real cURL command (DevTools "Copy as cURL", `man curl` literal) | $0 — regex grammar, sync |
| **AI parser** | You have anything else — raw API docs, `fetch()` snippet, axios call, Postman export, prose like "POST /users with name + email body" | 1 LLM call per parse, async |

Both tabs feed the **same review step** (§4.2). Switching tabs
preserves the textarea content so admin can fall back if cURL parser
fails. AI parser is hidden behind a feature flag if no LLM provider
is configured on the wick instance.

### 4.1 cURL parser scope

Support common cURL flags:

| Flag | Mapping |
|---|---|
| `-X METHOD` / `--request` | request.method |
| `-H 'K: V'` / `--header` | request.headers[K] = V; if V looks like a token → suggest secret |
| `-d` / `--data` / `--data-raw` | request.body_template |
| `--data-urlencode K=V` | request.body_template (form-encoded) |
| `-u USER:PASS` / `--user` | header `Authorization: Basic <…>`, suggest secret |
| URL (positional) | request.url_template |

**Token detection heuristic** (auto-suggest `secret`):

- Header value matches `Bearer\s+\S+` or `Basic\s+\S+`.
- Header key matches `Authorization | X-(Api|Auth|Token)-Key | …`.
- Query param contains `token | apikey | password | secret`.
- Body contains keys matching the same regex.

Admin can override toggle per field in the review step.

### 4.1b AI parser scope

Single LLM call with a structured-output prompt. Input = raw paste
(textarea contents, up to 8 KB; longer → error with "trim down"
hint). Output = same JSON shape as cURL parser would produce:

```json
{
  "method": "POST",
  "url": "https://api.example.com/users",
  "headers": [{"key":"Authorization","value":"Bearer …","secret":true}],
  "body": {"raw":"…","content_type":"application/json"},
  "suggested_op_name": "create_user",
  "suggested_inputs": [{"key":"name","widget":"text","required":true}, …]
}
```

**Implementation notes:**

- Provider = wick's default LLM provider (configured in
  `/admin/settings/providers`). Falls back gracefully (tab hidden) if
  none configured.
- Prompt template lives at `internal/connectors/custom/ai_parser.tmpl`
  — versioned, testable in isolation.
- Output validated against a strict JSON schema before handing to the
  review step. Parse failures surface as "AI couldn't extract a clean
  HTTP call from your paste — try cURL parser or paste more context."
- **No retention** of the raw paste. Only the extracted definition is
  persisted to `connector_defs.source_meta`. LLM call is one-shot, no
  streaming, no chain.
- Audit row in `connector_runs` is NOT written (this is admin tooling,
  not an LLM-callable op).

### 4.2 Fields extraction

Setelah parse, wick split jadi dua bucket:

- **Configs** — nilai yang stabil antar request (base URL, auth header
  value). Default: hostname → `base_url`; auth header value → `<header>_value`.
- **Inputs** — nilai yang berubah per request (path segments after
  base URL, query params, body fields). Wick tokenize JSON body /
  query string jadi `{{.in.<key>}}` placeholders.

Contoh:

```bash
curl -X POST 'https://api.stripe.com/v1/charges' \
  -H 'Authorization: Bearer sk_test_xxx' \
  -d 'amount=2000&currency=usd&customer=cus_123'
```

→

```
Configs:
  base_url   = "https://api.stripe.com/v1"        (auto)
  auth_value = "sk_test_xxx"                       (secret, auto-detected)

Operation "post_charges":
  Inputs:
    amount    (number)
    currency  (string, default "usd")
    customer  (string, required)
  Request:
    POST {{.cfg.base_url}}/charges
    Authorization: Bearer {{.cfg.auth_value}}
    body: amount={{.in.amount}}&currency={{.in.currency}}&customer={{.in.customer}}
```

Review step UI: 2-column form, left side = extracted fields with
suggested widget/secret toggle, right side = live preview of
`Configs` + `Inputs` JSON. **Admin can rename keys, change widget,
toggle secret, add `desc` and `default`.**

### 4.3 Save flow

1. Admin clicks **Save as new connector** → wick prompts for
   connector `Name`, `Key` (slug-validated, unique across built-in +
   custom), `Description`, `Icon`.
2. Wick writes one row to `connector_defs` + one row to
   `connector_def_ops`.
3. `registry.Bootstrap` re-runs → new custom def appears in
   `RegisterBuiltins`-equivalent registry, gets auto-seeded one
   instance row di `connectors`.
4. Redirect to `/manager/connectors/<key>/<instance_id>` (existing
   detail page) where admin can fill Configs values.

### 4.4 Edit flow

`GET /manager/connectors/custom/<def_id>/edit` → same review form,
prefilled. Save → bump `updated_at`, **don't** auto-restart executor —
flag instance(s) as "needs reload" via UI banner. Admin clicks
**Reload** → registry rebuild for this def only.

Rationale: live edit of an active connector while another user is
calling it via MCP could mid-flight swap the schema. Safer to require
explicit reload (one click, no downtime — old in-memory module stays
serving until atomic swap).

---

## 5. Operations — Flow B: from MCP server

For teams that already host internal MCP servers and want to expose
selected tools to wick as governed connectors (tagged, audited,
encrypted).

### 5.1 Server registration

`GET /manager/connectors/custom/mcp-servers/new` → form:

| Field | Note |
|---|---|
| Label | Display only |
| URL | Streamable-HTTP endpoint, e.g. `https://mcp.internal.example.com/v1` |
| Auth scheme | `none` / `bearer` / `custom_header` / `sso` (forward caller identity) |
| Headers | KV list — `Authorization`, `X-Tenant-Id`, etc. Values stored encrypted (`secret` widget) |

**Auth scheme details:**

#### `none` — no auth

No fields. Wick sends JSON-RPC with only `Content-Type: application/json`
and `Accept: application/json`. Acceptable inside private network or
when MCP server is gated by service mesh / VPN.

Outbound headers:
```
POST /v1
Content-Type: application/json
Accept: application/json
```

#### `bearer` — single secret token

One field: `auth_secret` (Bearer token). Stored as `wick_enc_…`,
decrypted server-side per request.

Outbound headers:
```
POST /v1
Authorization: Bearer <decrypted auth_secret>
Content-Type: application/json
```

Most common shared-secret case (OAuth access token, API key in
Bearer form). Save flow marks the field as `secret` automatically —
admin can never see plaintext after first save.

#### `custom_header` — KV pairs

Multiple header rows, each markable as secret. Stored in `headers`
JSONB (one row per key). Schemes that don't fit Bearer (Azure AAD
`Ocp-Apim-Subscription-Key`, paired ID + secret headers, etc.) live
here.

Outbound headers example:
```
POST /v1
X-API-Key: <decrypted>
X-Tenant-Id: prod
Content-Type: application/json
```

#### `sso` — forward caller identity

Zero shared secret. Wick mints a short-lived (5-min default,
configurable 1/5/15) ED25519-signed JWT representing the **user who
triggered the MCP call** and forwards as `X-Wick-User`. MCP server
validates against wick's pubkey at `/.well-known/wick-pubkey.pem`.

JWT claim mapping:

| Claim | Source |
|---|---|
| `sub` | `user.id` (UUID) |
| `email` | `user.email` |
| `name` | `user.display_name` |
| `groups` | `user.tag_ids[]` (for downstream RBAC) |
| `aud` | configurable (defaults to MCP URL host) |
| `iss` | wick base URL |
| `iat` / `exp` | now / now + TTL |

UI fields:

| Field | Default | Note |
|---|---|---|
| Audience (`aud`) | MCP URL host | MCP server should validate this — prevents token re-use across MCPs |
| TTL | 5 min | 1/5/15-min selector. Re-minted per request so short TTL is safe |

**Why SSO:** no shared secret stored, per-user RBAC + audit at the
MCP side, revoking a wick user revokes downstream access instantly
(no rotation needed).

**Server requirement:** MCP server must implement wick JWT
validation against the published pubkey. **Not supported by stock
open-source MCP servers** — typically only in-house ones. UI surfaces
a yellow note clarifying this.

#### Extra headers (any scheme)

In addition to the scheme-driven header, admin can define arbitrary
extra headers (routing, tenancy, `X-Request-Source`, etc.) under
"Extra headers". Each row independently markable as secret. Appended
on top of the scheme's headers — never replace them.

On **Test connection** → wick fires one outbound `initialize` +
`tools/list` request to the URL with the configured headers / auth →
shows result inline:

- **Success** — green panel: "✓ Connected · N tools discovered · NNms"
  + first N tool names. **Save is enabled.**
- **Failure** — red panel with HTTP status + first 200 chars of
  upstream body. **Save remains blocked.**

Save is **gated by at least one successful test** in the current form
session. Without that, the form submit returns a validation error.
This prevents half-broken MCP rows from polluting the table — and
guarantees the module build right after save (which re-probes) succeeds.

On save → **connector langsung dibuat** (create) atau di-rebuild
(edit). No tools cache, no import step. Reconnect / refresh kapan pun:
buka form server (detail MCP) → Test now → Save, atau tombol
**↻ Re-sync tools** di halaman connector.

**Why HTTP-only:** wick stays a forwarder. No spawn lifecycle, no
idle timeout, no respawn watcher, no node/python runtime in the wick
container, no arbitrary command exec. Existing stdio MCP servers can
be exposed via a small sidecar (`mcp-proxy`, `supergateway`, or
similar) — that complexity sits with the server owner, not wick.

### 5.2 Live ops + exclude list (replaces tool import)

Tidak ada import step. `BuildModule` untuk def `source=mcp`:

- probe `tools/list` live (timeout 15s; gagal → module 0 ops +
  warn log, boot tetap jalan; Reload re-sync setelah server balik)
- map **semua** tool minus `excluded_tools` → `DefOp` in-memory:
  - `mcp_source = {server_id, tool_name}`
  - `inputs` derived dari MCP `inputSchema` (JSON Schema → wick
    widget grammar; see § 5.4)
  - destructive guess dari nama (`delete_/remove_/drop_/...`)
- kolom `ops` di row def **selalu `"[]"`** untuk source=mcp —
  nothing per-tool persists.

**1 server = 1 connector**, named after the server label (key =
slug(label), immutable). Exclude list = opt-out checkboxes di form
server; ganti exclude → save → module rebuild langsung.

### 5.3 Execution path

When LLM calls a custom MCP-backed op:

```
wick_execute("conn:<custom_def_instance_id>/<op_key>", input)
  → connectors.Service.Execute
  → custom executor sees op.mcp_source != null
  → POST custom_mcp_servers[server_id].url
     headers: decrypted custom_mcp_servers[server_id].headers
     body:    JSON-RPC {"method":"tools/call",
                        "params":{"name": op.mcp_source.tool_name,
                                  "arguments": input}}
     via      http.NewRequestWithContext(c.Context(), ...)
  → unwrap JSON-RPC envelope → audit row written → returned to LLM
```

**Server connection management:** none. Per-call HTTP client using
`c.HTTP` (wick's shared 30s-timeout client). Same goroutine-leak
discipline as built-in `httprest`. No connection pool, no warmup, no
process to babysit.

### 5.4 inputSchema mapping

MCP `inputSchema` adalah JSON Schema. Mapper minimal v1:

| JSON Schema | wick widget |
|---|---|
| `type=string` | text |
| `type=string, format=uri` | url |
| `type=string, format=password` | secret |
| `type=number / integer` | number |
| `type=boolean` | checkbox |
| `enum=[a,b,c]` | dropdown |
| `type=string, description matches /password|token|secret/` | secret (auto-suggest) |
| nested object | flatten satu level dengan key `parent.child` (rare; flag warning to admin) |

Mapper output editable di import review — admin bisa override widget,
secret flag, default value, desc.

---

## 6. Operations — Flow C: manual form builder

Bare form. Tiga tahap:

1. **Meta** — Key, Name, Description, Icon.
2. **Configs** — table editor: + Add row → key / label / widget /
   secret / required / default / desc.
3. **Operations** — list, each expandable:
   - Op Meta (Key, Name, Description, Destructive toggle)
   - Inputs (same table editor as Configs)
   - Request (method dropdown + URL template + headers KV + body
     textarea + content-type)
   - **Test** button — live request against current Configs values,
     shows formatted response, **doesn't** persist to `connector_runs`
     until saved.

Used when user has API docs but no cURL handy, or wants to assemble
something multi-step ad-hoc.

---

## 7. UI states

Detail visual: [`mockup.html`](mockup.html). High-level mapping:

| State | Where | Note |
|---|---|---|
| ⓪ Connectors index | `/manager/connectors` | Tambah **+ New connector** button kanan-atas dgn dropdown (Paste / MCP / Blank) |
| ① Paste · cURL tab | `/manager/connectors/custom/new/paste` | Default tab. Big textarea + "Parse" button. Regex grammar, sync |
| ① Paste · AI tab | same URL, `?parser=ai` | Same textarea, LLM extract on submit. Tab hidden if no provider configured |
| ② Review | `/manager/connectors/custom/new/paste/review` | Split view: extracted fields ← → JSON preview. Same form for both parsers |
| ③ MCP server form | `/manager/connectors/custom/mcp-servers` (alias `/new`) | URL + auth scheme (none/bearer/header/SSO) + extra headers + inline Test connection (save gated by ≥1 success) + **exclude-list tools** (muncul setelah test). Save → connector langsung jadi. No list page, no import picker |
| ④ MCP server edit | `/manager/connectors/custom/mcp-servers/edit?id=` | Form sama, prefilled; tools di-probe live server-side buat exclude list. "Edit definition" def MCP redirect ke sini. Reconnect = Test now → Save |
| ⑥ Manual builder | `/manager/connectors/custom/new/manual` | Meta → Configs → Operations stepper |
| ⑦ Custom detail | `/manager/connectors/{key}` | Same chrome as built-in; tambah "Edit definition" + badge "Custom · <source>" + reload banner kalau dirty + **↻ Re-sync tools** (MCP only) |

Entry button di index ada di kanan-atas card list, persis di kanan
search box, supaya sealiran sama existing chrome (lihat
`connectors_index.templ:41-54`). Dropdown menu pakai existing
disclosure pattern.

### 7.1 Design system rules (yang dipakai di mockup)

- Font: Inter via `font-sans`.
- Primary accent: `green-500` (`#27B199`).
- Page bg: `white-200` / `dark:navy-800`; cards `white-100` / `dark:navy-700`.
- Borders: `white-300` / `dark:navy-600`.
- Text: `black-900` / `dark:white-100` for primary; `black-800` /
  `dark:black-600` for secondary; `black-700` for placeholder/disabled.
- Status chips:
  - "Custom" badge → `green-200` bg + `green-700` text.
  - "Built-in" badge → `white-300` bg + `black-800` text.
  - "Dirty / needs reload" banner → `cau-400` text + `cau-100` bg.
  - "Destructive" op chip → `neg-400` text + `neg-100` bg.
- Spacing: 8-grid (`gap-2 / gap-3 / gap-4`, `p-4 / p-5 / p-6`).
- Radius: `rounded-xl` (12px) for cards; `rounded-lg` (8px) for
  inputs / buttons; `rounded-full` for chips.
- Icons: 16/18/24px containers, 2px stroke (Heroicons / inline SVG).

---

## 8. Encrypted-fields integration

Custom connector tetap rapat dengan `encrypted-fields` skill — sama
sekali tidak bikin path baru:

1. **Configs field bertanda `secret`** (admin toggle di Flow A/B/C
   review) → field tersebut di-Mark `secret` di `connector_defs.configs`
   array → saat `entity.ConfigsToStruct` melahirkan schema, framework
   layer di `connectors.Service.Execute` udah auto-decrypt
   `wick_enc_` token dan auto-mask plaintext di response.
2. **Input field bertanda `secret`** — sama, untuk round-trip token
   (refresh token, session cookie).
3. **MCP-backed op** — header `Authorization` value yang dipake buat
   reach MCP server, di-store di `custom_mcp_servers.headers` JSONB
   sebagai `wick_enc_<token>`. Saat eksekusi, executor decrypt sebelum
   spawn / HTTP call. Plaintext **never** masuk ke `connector_runs.response_json`
   karena response sudah di-mask oleh layer.

**Yang harus diaudit pas implement:**

- Body template berisi `{{.cfg.api_key}}` di mana `api_key` secret →
  generic executor harus pakai pre-decrypt'd value (yg framework udah
  resolve) lewat `c.Cfg("api_key")`. Nggak ada plaintext storage di
  template engine.
- Live "Test" button di Flow C — request keluar dgn plaintext config,
  response dimask sebelum render ke admin (sama dengan
  `Connector Test` page existing). Jangan log plaintext ke
  `connector_runs.request_json` — gunakan `wick_enc_` placeholder.

---

## 9. Tags / ACL

**Tags adalah surface utama** access control buat custom connector —
sama persis pattern dengan built-in tools, jobs, dan connectors
(lihat `internal/tags/defaults.go` & `connector-module` skill).
Custom def tidak punya jalur ACL khusus, tidak ada "shared with
users" picker terpisah. Semua granting akses lewat tag.

### 9.1 Tiga flag tag (recap dari `internal/tags/defaults.go`)

| Flag | Arti |
|---|---|
| `IsGroup` | Visual grouping — tagged item muncul di group ini di home / connector index |
| `IsFilter` | Participates di access-filter rule — non-admin yg ngga carry tag ini, ngga lihat item-nya |
| `IsSystem` | Admin UI nolak assign tag ini ke user → nobody carries → item invisible ke semua non-admin |

### 9.2 Auto-tag at create — per-def filter tag

Saat admin save custom connector (dari Flow A/B/C), wick **auto-create
satu tag baru** dengan shape:

```
Name:        "custom:<def_key>"
Description: "Access tag for custom connector '<Name>'. Assign to user groups to grant access."
IsGroup:     false
IsFilter:    true   ← penting: aktifin filter rule
IsSystem:    false  ← admin BISA assign ke user (beda dengan System tag)
SortOrder:   2000+  ← di bawah default catalog
```

Instance row dari def itu (`connectors` table) di-tag dengan:

1. `custom:<def_key>` — filter tag yang baru dibuat (per-def)
2. `Connector` group tag (visual: muncul di Connector group di home)
3. Category tag pilihan admin di review step (`Communication` /
   `Observability` / `Internal APIs` / `Development` / dst — sama
   pilihan yang sudah ada di `defaults.go:51-99`)

### 9.3 Default behavior = admin-only

Begitu di-save:

- **Tidak ada user** yang carry `custom:<def_key>` tag → filter rule
  hide connector dari semua non-admin `/manager/connectors` index +
  detail + `wick_list`.
- Admin (via flag `IsAdmin`) bypass filter → admin lihat normal.

Setara dengan System tag default — admin-only sampai admin opt in.

Tapi **beda dari System tag** dalam dua hal:
- `IsSystem=false` → admin UI tetap **boleh** assign tag ini ke user
  / user group lewat `/admin/tags`. Itu yang nge-buka akses.
- Tag belongs ke specific def (per-def, bukan global). Admin bisa
  open `Stripe` ke group A doang, `Notion` ke group B doang.

### 9.4 Membuka akses ke user / group

Admin punya dua jalur:

| Jalur | Lokasi | Efek |
|---|---|---|
| Assign per-user | `/admin/users/<id>` → Tags section, add `custom:<def_key>` | User itu carry tag → lihat connector di /manager + bisa call MCP |
| Assign per-group | `/admin/tags/<tag_id>` → Members section, add users | Bulk grant — semua user di list dapet akses |

Atau, kalau admin **mau full open** ke semua approved user:

| Pilihan B | Lokasi | Efek |
|---|---|---|
| Hapus filter tag dari def | `/manager/connectors/<key>/<id>` → Access → remove `custom:<def_key>` tag | Connector kehilangan filter → terlihat oleh semua approved user. Tag rowtetap exist tapi tidak load-bearing |
| Toggle "Open to all" | UI shortcut yang setara: remove `custom:<def_key>`, keep category + Connector group | Sama efek dengan pilihan B, satu klik |

Pilihan B berguna untuk connector yang admin yakin OK buat semua
orang (misal "Internal Knowledge Base"). Tag tetap di-keep untuk
backward-restore (admin bisa re-attach kalau berubah pikiran).

### 9.5 Per-operation ACL (existing, dipakai apa adanya)

Selain row-level tag, per-op masih punya:

- `ConnectorOperation.Enabled` (admin per-op on/off)
- `ConnectorOperation.AdminOnly` (admin-only op meskipun row terbuka
  untuk user lain — pas buat op destruktif yang user biasa shouldn't
  call meskipun admin sudah grant connector access)
- `OpDestructive` default-off (sama dengan built-in)

Three independent off-switches resolve as: **call passes only when
row tags allow + op enabled + op not admin-only-for-non-admin**.

### 9.6 Bootstrap flow

```
admin save custom def (Flow A/B/C)
  → connector_defs row INSERT
  → wick.Tag.EnsureCustomDefTag(def_key, def_name) — idempotent
       → INSERT tags row dengan IsFilter=true, IsSystem=false (sekali aja)
  → registry rebuild → instance row INSERT di connectors table
  → tag-link rows: connectors.id ↔ tag_ids =
       [custom:<def_key>, Connector, <category pilihan admin>]
  → admin redirect ke /manager/connectors/<key>/<id>
  → Access section sudah pre-populated; admin lihat hint:
       "Visible to admins only. Assign tag custom:<def_key> to user
       groups at /admin/tags to grant access."
```

### 9.7 Delete cleanup

Saat admin delete custom def:

- `connector_defs` row hard-deleted (cascade ke `connector_def_ops`)
- Instance `connectors` row hard-deleted
- `custom:<def_key>` tag — **default keep** (admin mungkin re-create
  dengan key sama nanti; tag-user links survive). Optional: cleanup
  via "Also delete tag" checkbox di delete confirm modal.
- Tag-link rows ke connector instance ke-cascade ke delete

---

## 10. MCP surface

Custom connector **tidak** menambah meta-tool baru di MCP. Semua
tetap di `wick_list` / `wick_search` / `wick_get` / `wick_execute`.

LLM melihat custom connector identik dengan built-in:

```
wick_list →
  [
    { "tool_id": "conn:<id>/list_repos", "name": "List Repositories", ... },     // built-in github
    { "tool_id": "conn:<id>/create_charge", "name": "Create Charge", ... }       // custom stripe
  ]
```

Tidak ada flag di output yang bilang "ini custom" — by design, LLM
nggak peduli source. Audit log + admin UI yang membedakan.

---

## 11. Backward compat

- Built-in connectors (`internal/connectors/*` registered di
  `RegisterBuiltins`) **tidak berubah**.
- `httprest` built-in tetap exist sebagai "quick adhoc HTTP" — admin
  pakai itu kalau gak mau setup definition full.
- Existing instances + `connector_runs` rows tidak ke-touch.
- Migration: tambah 3 tables baru, no drop / alter ke tabel existing.

---

## 12. Refactor surface — impact zones (actual, 2026-06-11)

### 12.1 Core (landed)

| Zona | File / pkg | Catatan |
|---|---|---|
| Schemas + validation | `internal/connectors/custom/schema.go` | `DefField`, `DefOp`, `OpRequest`, `MCPSource`, `HeaderRow`, `SSOExtra`, `SourceMeta`, `Draft` + `ValidateDraft`/`ValidateFields` |
| Orchestration | `internal/connectors/custom/service.go` | `Service`: `RegisterAllAtBoot`, `SaveNew`/`Update`/`Reload`/`Delete`, `IsDirty`, per-def tags (`defaultTagsFor`, `EnsureInstanceTags`), `ParsePaste`, `TestServer`/`SaveServer`/`ProbeStored`, `ImportTools` + inputSchema→widget mapper |
| Generic executor | `internal/connectors/custom/executor.go` | `BuildModule` (def row → `connector.Module`, `Meta.Fixed=true`), `executeHTTP` (template render + shared client), `executeMCP`, `coerceArgs` |
| Template engine | `internal/connectors/custom/template.go` | text/template whitelist (`default`/`lower`/`upper`/`b64`), `missingkey=error`, 1 MB output cap |
| cURL parser | `internal/connectors/custom/curl_parser.go` | tokenizer + `ParseCurl` + `Extract` (Configs/Inputs split) + secret heuristics |
| AI parser | `internal/connectors/custom/ai_parser.go` + `ai_parser.tmpl` | `NewProviderAIParser` — wraps workflow-provider `StructuredCall`; nil (tab hidden) kalau provider tanpa structured output |
| MCP forwarder + SSO | `internal/connectors/custom/mcp_client.go` | per-call JSON-RPC (initialize/tools-list/tools-call, SSE-tolerant, `Mcp-Session-Id`), 4 auth schemes, `ssoSigner` (ED25519 JWT, seed di `configs` owner=`custom_connector`) |
| Persistence | `internal/connectors/custom/store.go` | gorm CRUD defs + servers; `ErrKeyTaken` |
| Entities | `internal/entity/custom_connector.go` | `entity.CustomConnector`, `entity.CustomConnectorMCPServer` |
| Migration | `internal/pkg/postgres/migrate.go` | dua entity ditambah ke `db.AutoMigrate(...)` — no SQL files |
| Wiring | `internal/pkg/api/server.go` | `customConnSvc := customconn.New(...)` → `SetAIParser` (first structured-output CLI provider) → `RegisterAllAtBoot` **sebelum** `connectorsSvc.Bootstrap`; `SetTags` + `EnsureInstanceTags` setelah `tagsSvc`; `managerHandler.SetCustomConnectors` |

### 12.2 Manager UI

| Zona | File / pkg | Status |
|---|---|---|
| Service hook | `internal/manager/custom_connectors.go` + `handler.go` | ✅ `SetCustomConnectors` late-binds service ke handler |
| Routes | `/manager/connectors/custom/*` (paste, review, mcp-servers, manual, edit) | ⏳ belum — backend endpoints ready di `custom.Service` |
| Views / JS | `internal/manager/view/custom_*.templ`, `internal/manager/js/custom_*.js` | ⏳ belum |
| Index tweak | "+ New connector" dropdown, "Custom" badge | ⏳ belum |
| Pubkey route | `GET /.well-known/wick-pubkey.pem` → `customConnSvc.SSO().PublicKeyPEM()` | ⏳ belum di-mount (signer + accessor sudah ada) |

### 12.3 Connector framework

| Zona | File / pkg | Catatan |
|---|---|---|
| `pkg/connector` | (no public API change) | Custom modules pakai `ExecuteFunc` + `Ctx` pattern apa adanya |
| `connectors.Service` | `internal/connectors/service.go` | satu tambahan: `UpsertModule(ctx, m)` — register-or-replace di dispatch map + seed instance/config rows (dipakai save/Reload tanpa restart) |
| `connector_runs` | (no schema change) | Reused as-is |

### 12.4 MCP server

No changes to `internal/mcp/*`. `wick_list` / `wick_execute` lihat
custom connector identik dengan built-in.

### 12.5 Tests

| Zona | Catatan |
|---|---|
| Unit | cURL parser tests dengan fixtures (10–15 real cURL strings), template engine tests (whitelist enforcement), MCP proxy tests dengan in-process server |
| Integration | Bootstrap custom def → instance auto-seed → wick_execute via MCP smoke |
| Security | Secret leak: secret config never appears in `connector_runs.response_json` plaintext; live Test path masks before render |

### 12.6 Docs

- `internal/planning/archive/connectors-design.md` — section baru "Custom
  connectors" yang point ke file ini.
- `internal/planning/todo/custom-connector/design.md` (file ini) — keep
  authoritative.
- `internal/planning/todo/custom-connector/mockup.html` — paired mockup.
- `docs/guide/custom-connectors.md` (user-facing) — how-to: paste
  cURL, register MCP, manual form. Add to vitepress sidebar.

---

## 13. Acceptance checklist (implementation gate)

- [x] `internal/connectors/custom/` package with: `DefField`/`DefOp` +
  `Draft` via `schema.go`, CRUD via `store.go` + `service.go`; generic
  `ExecuteFunc` via `executor.go`; template engine via `template.go`
  (whitelist enforced, `missingkey=error`, 1 MB cap)
- [x] Migration lands — **single table** `custom_connectors` (ops
  embedded JSON) + `custom_connector_mcp_servers`, via gorm
  `AutoMigrate` di `internal/pkg/postgres/migrate.go`
- [x] `Service.RegisterAllAtBoot` runs at boot (sebelum
  `connectorsSvc.Bootstrap`), replays DB → registers one
  `connector.Module` per def (key = def key, `Meta.Fixed=true`),
  auto-seeds instance row (sama dengan Bootstrap built-in)
- [x] Encrypted-fields integration: secret-tagged Configs/Input on
  custom def auto-decrypt + auto-mask via existing layer (no new
  Mask call); MCP server creds encrypted under master key
  (`configs.EncryptSecret`)
- [x] **Flow A · cURL parser** — backend + manager page
  `/manager/connectors/custom/new/paste` (paste + review UI) ✅
- [x] **Flow A · AI parser** — backend + AI tab UI ✅ (tab hidden
  tanpa provider structured-output; raw paste not persisted)
- [x] **Flow B** — backend + pages `/manager/connectors/custom/
  mcp-servers*` ✅ (save gate, auth none/bearer/custom_header/sso/
  **oauth**, live ops + exclude list — import picker dihapus, lihat
  update notes); pubkey route `/.well-known/wick-pubkey.pem` mounted
- [x] **Flow C** — `/manager/connectors/custom/new/manual` stepper ✅
- [x] Custom def di index dengan badge "Custom · <source>" + status
  chip; "+ New connector" dropdown (semua approved user) ✅
- [x] **Tags / ACL** — at save + at boot (idempotent), wick
  auto-creates `custom:<def_key>` filter tag (`IsFilter=true,
  IsSystem=false`, SortOrder 2000) + tags instance row with [filter,
  Connector, category] via `EnsureToolDefaultTags`. Default =
  admin-only. Per-op `enabled` + `admin_only` reused as-is.
  (⏳ UI "Open to all" shortcut menyusul bareng manager pages)
- [x] Edit flow — banner + Reload + per-instance Re-sync ✅ (atomic
  swap via `UpsertModule`, no restart)
- [x] `connector_runs` writes the same shape for custom ops as for
  built-in (eksekusi lewat `connectors.Service.Execute` yang sama)
- [x] Tests pass: cURL parser table-driven, template whitelist, MCP
  proxy in-process roundtrip (incl. JWT verify), OAuth discovery/DCR/
  PKCE/refresh, schema, executor mapper
- [x] Docs: user-facing `docs/guide/custom-connectors.md` + sidebar
  entry; design.md + mockup.html kept in sync with code
  (2026-06-11)

---

## 14. Open questions — **semua resolved per 2026-06-11**

1. ~~**Naming**~~ — **RESOLVED 2026-06-11:** "custom connector" di
   docs + UI copy; Go package `internal/connectors/custom/`; entities
   `entity.CustomConnector` + `entity.CustomConnectorMCPServer`.
2. ~~**MCP transport priority**~~ — **RESOLVED 2026-06-05:**
   streamable-HTTP only di v1. Wick = forwarder murni. Stdio
   di-defer (alasan: process supervision overhead, gak match dengan
   "wick stays light" prinsip). User yang punya stdio MCP existing
   bisa pakai sidecar (mcp-proxy/supergateway) buat expose lewat
   HTTP.
3. ~~**Body template engine**~~ — **RESOLVED 2026-06-11:** Go
   `text/template` dengan whitelist (`default`/`lower`/`upper`/`b64`
   + safe builtins `urlquery`/`js`/`printf`), `missingkey=error`,
   1 MB output cap. Mustache ditolak — Go template + missingkey
   errors udah cukup aman dan ekspresif (`template.go`).
4. ~~**Edit-while-running**~~ — **RESOLVED 2026-06-11:** explicit
   Reload. `Update` cuma rewrite row (module lama tetap serving);
   `IsDirty` drives banner; `Reload` rebuild + atomic swap via
   `connectors.Service.UpsertModule`. No auto hot-swap.
5. ~~**Per-row Configs**~~ — **RESOLVED 2026-06-11:** 1 def = 1
   instance, enforced via `Meta.Fixed=true` di `BuildModule`.
   Multi-row tetap deferred (lihat TODO).
6. ~~**Live Test button**~~ — **RESOLVED 2026-06-11:** one-shot,
   mirror existing Connector Test panel — instance row dari def
   dipakai apa adanya, no sandbox/mock.

---

## 15. Rejected alternatives

- **Stdio MCP server in v1** — wick jadi process supervisor (spawn,
  lifecycle, idle timeout, respawn, npm/python dependency, command
  exec surface). Beratnya gak proporsional sama benefit-nya: existing
  stdio MCPs bisa di-wrap dengan `mcp-proxy` sidecar dan di-expose via
  HTTP. Wick stays as a forwarder.
- **WASM / dynamic .so loader** — overkill, security nightmare,
  binary distribution problem. Generic Go executor + JSON schema
  cukup untuk 95% kasus.
- **Connector definition as YAML on disk** — admin friction
  (filesystem access, version control workflow not aligned with UI
  edit). DB-backed lebih konsisten dengan rest of wick admin surface.
- **MCP server proxy as separate top-level surface** — pernah dilihat
  sebagai "/manager/mcp-servers" bersaudara dengan connectors. Tolak
  karena duplicates audit / tags / ACL infrastructure. Lebih masuk
  akal jadi import path dari custom-connector.
- **OpenAPI/Swagger import in v1** — postponed (lihat TODO). cURL
  cover 80% of one-off needs without writing a spec parser.
- **Codegen** — admin saves def → wick generates Go code →
  hot-recompile + restart. Way too much complexity vs benefit.
- **Per-user custom connectors** — connectors are team infra; admin
  scope is correct. Per-user customization belongs to the LLM client
  side, not the connector layer.
