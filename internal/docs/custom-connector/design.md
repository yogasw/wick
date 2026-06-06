# Custom Connector — Add From cURL / MCP / Form (design)

Status: **proposal — not implemented**. Awaiting human sign-off on scope,
storage shape, and security model before any code lands.
Update terakhir: 2026-06-05.

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
   headers, no process spawn. Discover tools via JSON-RPC `tools/list`
   → pilih tool mana yang mau di-import → tiap tool jadi satu Operation
   dengan input schema dipetakan dari MCP `inputSchema`. Stdio
   (npx/python spawn) **bukan v1** — wick gak jadi process supervisor.
3. **Manual form builder** — admin bangun connector + op dari form
   kosong (rare path, tapi penting buat APIs tanpa cURL/MCP spec).

Semua jalur ujungnya **rapat** dengan kontrak `connector-module` skill:
Meta, Configs, Operations, per-op Input. Bedanya cuma sumber: built-in
dari Go reflect, custom dari row di tabel `connector_defs` + JSON
schema rows di `connector_def_ops`. Dari sudut MCP (`wick_list` / `wick_execute`)
two paths look identical — same `tool_id` shape, same audit trail, same
encrypted-fields layer.

Paired mockup: [`mockup.html`](mockup.html). Update keduanya barengan.

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

### 3.1 Tables

```sql
-- one row per custom connector definition
connector_defs (
  id           uuid primary key,
  key          text unique not null,          -- e.g. "stripe", "internal-billing"
  name         text not null,                 -- "Stripe (custom)"
  description  text,                          -- shown in index card
  icon         text default '🔌',
  source       text not null,                 -- "curl" | "mcp" | "manual"
  source_meta  jsonb,                         -- raw parser input (cURL string / MCP server config)
  configs      jsonb not null,                -- [{key, label, widget, secret, required, default, desc}]
  created_by   uuid not null,                 -- user.id (admin)
  created_at   timestamptz,
  updated_at   timestamptz,
  disabled     boolean default false
)

-- one row per operation within a custom def
connector_def_ops (
  id            uuid primary key,
  def_id        uuid references connector_defs(id) on delete cascade,
  key           text not null,                -- snake_case
  name          text not null,
  description   text not null,
  destructive   boolean default false,
  inputs        jsonb not null,               -- per-input schema (same shape as configs)
  request       jsonb not null,               -- {method, url_template, headers, body_template, content_type}
  response      jsonb,                        -- {mode: "passthrough" | "typed", shape: ...} — v1: always passthrough
  mcp_source    jsonb,                        -- {server_id, tool_name} if source=mcp
  display_order int default 0,
  created_at    timestamptz,
  updated_at    timestamptz,
  unique (def_id, key)
)

-- one row per MCP server registered as custom connector source
custom_mcp_servers (
  id            uuid primary key,
  label         text not null,                -- "internal-tools mcp"
  transport     text not null default 'http', -- 'http' (v1). 'stdio' reserved, deferred.
  url           text not null,                -- streamable-HTTP endpoint
  auth_scheme   text not null default 'none', -- 'none' | 'bearer' | 'custom_header' | 'sso'
  auth_secret   text,                         -- bearer: token (wick_enc_). null for none/sso/custom_header
  auth_headers  jsonb,                        -- custom_header: [{key, value(wick_enc_), secret}]
  auth_extra    jsonb,                        -- sso: {audience, ttl_seconds}. Free-form per scheme
  headers       jsonb,                        -- *extra* headers (any scheme) — KV array, independent of auth
  tools_cache   jsonb,                        -- last tools/list snapshot
  last_test_at  timestamptz,                  -- nullable; required non-null at save
  last_test_ok  boolean,
  created_at    timestamptz,
  updated_at    timestamptz
)
```

**Reuse existing tables:**

- `connectors` (instance rows) — untuk custom, satu instance per def
  di v1, auto-seeded sama path Bootstrap.
- `connector_operations` (per-op enabled state, tag ACL) — sama,
  bootstrap saat def-op created.
- `connector_runs` (audit) — sama, kolom `connector_id` resolve ke
  custom instance, `op_key` resolve ke def-op key.
- `configs` (per-instance config values) — sama, `owner="connector:<instance_id>"`.

### 3.2 JSON shapes

**`connector_defs.configs`:**

```json
[
  {
    "key": "base_url",
    "label": "Base URL",
    "widget": "url",
    "required": true,
    "secret": false,
    "desc": "API base URL. Example: https://api.stripe.com/v1"
  },
  {
    "key": "api_key",
    "label": "API Key",
    "widget": "secret",
    "required": true,
    "secret": true,
    "desc": "Stripe secret API key (sk_…)"
  }
]
```

Mirror dari shape `entity.StructToConfigs(Configs{})` — sehingga
`Module.Configs` bisa dibangun langsung dari array ini tanpa Go
reflection.

**`connector_def_ops.inputs`:** sama shape.

**`connector_def_ops.request`:**

```json
{
  "method": "POST",
  "url_template": "{{.cfg.base_url}}/charges",
  "headers": {
    "Authorization": "Bearer {{.cfg.api_key}}",
    "Content-Type": "application/json"
  },
  "body_template": "{ \"amount\": {{.in.amount}}, \"currency\": \"{{.in.currency}}\" }",
  "content_type": "application/json"
}
```

**Templating rules:**

- Go `text/template` dengan `.cfg.<key>` dan `.in.<key>` namespaces.
- Functions whitelist: `urlquery`, `js`, `printf`, `default`,
  `lower`, `upper`. **No `exec`, no shell, no file read.**
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
X-Tenant-Id: qiscus-prod
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
This prevents half-broken MCP rows from polluting `custom_mcp_servers`.

On save → tools snapshot cached to `custom_mcp_servers.tools_cache`
(JSONB). Admin can re-test anytime from the detail page to refresh.

**Why HTTP-only:** wick stays a forwarder. No spawn lifecycle, no
idle timeout, no respawn watcher, no node/python runtime in the wick
container, no arbitrary command exec. Existing stdio MCP servers can
be exposed via a small sidecar (`mcp-proxy`, `supergateway`, or
similar) — that complexity sits with the server owner, not wick.

### 5.2 Tool import

For each tool admin **picks to import**:

- One row in `connector_def_ops` with:
  - `mcp_source = {server_id, tool_name}`
  - `inputs` derived from MCP `inputSchema` (JSON Schema → wick
    widget grammar; see § 5.4)
  - `request` = `null` (executor will see `mcp_source != null` and
    use MCP proxy path)

Multiple tools from one MCP server can be grouped under **one**
`connector_defs` row (admin's choice: 1 def per server, or 1 def
per logical bundle). Default: 1 def per server, named after the
server label.

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
| ③ MCP server list | `/manager/connectors/custom/mcp-servers` | Tabel server: status, last test, # tools |
| ④ MCP server new | `/manager/connectors/custom/mcp-servers/new` | URL + auth scheme (none/bearer/header/SSO) + extra headers + inline Test connection (save gated by ≥1 success) |
| ⑤ MCP tool import | `/manager/connectors/custom/mcp-servers/{id}/import` | Checkbox grid of tools, per-tool input schema preview |
| ⑥ Manual builder | `/manager/connectors/custom/new/manual` | Meta → Configs → Operations stepper |
| ⑦ Custom detail | `/manager/connectors/{key}` | Same chrome as built-in; tambah "Edit definition" + badge "Custom" + reload banner kalau dirty |

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

## 12. Refactor surface — impact zones

### 12.1 Core (new)

| Zona | File / pkg | Catatan |
|---|---|---|
| Pkg baru | `internal/connectors/custom/` | `def.go` (CRUD), `executor.go` (generic ExecuteFunc), `mcp_proxy.go` (Flow B runtime), `curl_parser.go` (Flow A), `template.go` (text/template w/ whitelist funcs) |
| Schema | `internal/entity/connector_def.go` | `ConnectorDef`, `ConnectorDefOp`, `CustomMCPServer` structs |
| Migration | `internal/entity/migrations/NNNN_custom_connectors.go` | 3 new tables |
| Registry | `internal/connectors/registry.go` | `RegisterBuiltins` + `RegisterCustom(ctx, db)` — second pass yg baca DB |

### 12.2 Manager UI

| Zona | File / pkg | Catatan |
|---|---|---|
| Routes | `internal/manager/connectors.go` | + `/manager/connectors/custom/*` paths (new flow pages, mcp-servers, edit) |
| Views | `internal/manager/view/custom_*.templ` | `custom_curl.templ`, `custom_mcp.templ`, `custom_manual.templ`, `custom_review.templ` |
| Index page tweak | `internal/manager/view/connectors_index.templ` | + "New connector" dropdown btn (cURL / MCP / Blank), + "Custom" badge per card |
| JS | `internal/manager/js/custom_*.js` | cURL paste parser preview (live JSON), MCP tool picker, form steppers |

### 12.3 Connector framework

| Zona | File / pkg | Catatan |
|---|---|---|
| `pkg/connector` | (no public API change) | Generic executor punya `ExecuteFunc` yang valid; tetap `ctx` + Input/Cfg pattern |
| `connectors.Service` | `internal/connectors/service.go` | No change ideally — registry-driven; custom defs just look like more built-ins |
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

- `internal/docs/connectors-design.md` — section baru "Custom
  connectors" yang point ke file ini.
- `internal/docs/custom-connector/design.md` (file ini) — keep
  authoritative.
- `internal/docs/custom-connector/mockup.html` — paired mockup.
- `docs/guide/custom-connectors.md` (user-facing) — how-to: paste
  cURL, register MCP, manual form. Add to vitepress sidebar.

---

## 13. Acceptance checklist (implementation gate)

- [ ] `internal/connectors/custom/` package with: `Def`, `DefOp`,
  CRUD via `def.go`; generic `ExecuteFunc` via `executor.go`;
  template engine via `template.go` (whitelist enforced)
- [ ] `connector_defs`, `connector_def_ops`, `custom_mcp_servers`
  migrations land
- [ ] `registry.RegisterCustom` runs at boot, replays DB → registers
  one `connector.Module` per def, auto-seeds instance row (sama
  dengan Bootstrap built-in)
- [ ] Encrypted-fields integration: secret-tagged Configs/Input on
  custom def auto-decrypt + auto-mask via existing layer (no new
  Mask call)
- [ ] **Flow A · cURL parser** — `/manager/connectors/custom/new/paste`
  (cURL tab default): paste + regex parse + review + save. Parser
  handles `-X`, `-H`, `-d`, `-u`, URL
- [ ] **Flow A · AI parser** — same page, AI tab: paste anything →
  LLM extract via default provider → strict JSON schema validation →
  same review form. Tab hidden when no provider configured. Raw
  paste not persisted (only extracted def)
- [ ] **Flow B** — `/manager/connectors/custom/mcp-servers`: register
  server (URL + auth scheme: none/bearer/custom_header/sso), Test
  connection blocks Save until at least one success, `tools/list`
  cached, pick tools to import, JSON Schema → widgets
- [ ] **Flow C** — `/manager/connectors/custom/new/manual`: Meta /
  Configs / Operations stepper + Test button per op
- [ ] Custom def appears in `/manager/connectors` index with "Custom"
  badge; cards stay grouped by tag category alongside built-in
- [ ] **Tags / ACL** — at save, wick auto-creates `custom:<def_key>`
  filter tag (`IsFilter=true, IsSystem=false`) + tags instance row
  with [filter, Connector, category]. Default = admin-only (no user
  carries the tag). `/admin/tags/<id>` assigns to user groups to
  open access. `/manager/connectors/<key>/<id>` Access section shows
  current visibility state + "Open to all" shortcut (removes filter
  tag). Per-op `enabled` + `admin_only` reused as-is from built-in
  framework. Three independent off-switches: row-tag, op-enabled,
  op-admin-only
- [ ] Edit flow: bump `updated_at`, show "Reload" banner; one-click
  rebuild Module without server restart
- [ ] `connector_runs` writes the same shape for custom ops as for
  built-in
- [ ] Tests pass: cURL parser table-driven, template whitelist denies
  `exec`/file read, MCP proxy in-process roundtrip, secret leak path
- [ ] Docs: user-facing `docs/guide/custom-connectors.md` + sidebar
  entry; design.md + mockup.html kept in sync with code

---

## 14. Open questions (need user input before scoping)

These are the questions I want a human answer to before generating any
code:

1. **Naming** — `custom-connector`, `connector-builder`, atau `byoc`?
   Default rekomendasi: `custom-connector` (docs + UI) /
   `internal/connectors/custom/` (Go).
2. ~~**MCP transport priority**~~ — **RESOLVED 2026-06-05:**
   streamable-HTTP only di v1. Wick = forwarder murni. Stdio
   di-defer (alasan: process supervision overhead, gak match dengan
   "wick stays light" prinsip). User yang punya stdio MCP existing
   bisa pakai sidecar (mcp-proxy/supergateway) buat expose lewat
   HTTP.
3. **Body template engine** — Go `text/template` dgn whitelist
   functions cukup, atau perlu Mustache-style logic-less (lebih aman
   untuk admin yang nggak kenal Go template syntax)?
4. **Edit-while-running** — banner "Reload" cukup, atau perlu hot
   swap auto (test in background, atomic switch)? V1 saya
   propose: explicit reload button. Konfirmasi?
5. **Per-row Configs** — v1 saya kunci 1 def = 1 instance (sama
   seperti pasangan Meta+Configs di built-in module). User OK dengan
   itu, atau dari awal multi-row supaya satu Stripe def bisa punya
   row "live" + row "test"?
6. **Live Test button** — di Flow C butuh real upstream call dengan
   admin's Configs. OK untuk one-shot (mirror Connector Test page
   existing) atau perlu sandbox / mock?

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
