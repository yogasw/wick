# Tools Config & Connections — Design Discussion

Catatan diskusi desain buat 3 masalah yang diangkat user:

1. Duplikat tools (logic sama, target beda — mis. auto-download GSheet ke sheet1 vs sheet2).
2. Env per-tool (config + default, gak hardcode di code).
3. Integrasi ke Slack / Notion / GSheet — OAuth / API, module "siap pakai" vs "butuh setup".

Dokumen ini hasil diskusi bertahap.

> **Status terbaru (2026-04-18).** #1 opsi A (multi-instance via `NewTool(meta, cfg)` register berkali-kali) dan #2 per-tool config sudah **shipped** — tapi dengan bentuk akhir yang beda dari draft di bawah:
>
> - Tabel `app_variables` → **`configs`** (scope `(owner, key)`; `owner=""` untuk app-level, `owner=meta.Key` untuk per-tool/job).
> - `configs.Spec` struct → **dihapus**. Tool deklarasi runtime-editable knob via typed `Config` struct + `wick:"..."` tags, framework pakai `entity.StructToConfigs(cfg)` untuk reflect ke rows.
> - Tool interface: `Specs() []tool.Spec` → **`Configs() []entity.Config`** (interface `tool.Configurable`).
> - Handler baca via `c.Cfg("key")` / `c.CfgInt` / `c.CfgBool` — scoped otomatis ke `Meta.Key`. `c.CfgOf(owner, key)` buat cross-tool (jarang).
>
> Bab 3 (connections), #4 permission model, #5 per-field `Locked`/user_overrides, dan #7 `ToolCtx`/`JobCtx` middleware chain **belum ship** — masih draft desain. Detail di bawah dipertahankan buat referensi arah jangka panjang; baca dengan catatan: tiap sebutan `configs.Spec`, `Specs()`, atau `app_variables` harus dibaca sebagai "konsep lama yang sekarang diwakili `entity.Config` + `Configs()` + tabel `configs`".

---

## 1. Multi-instance tools (duplikat tanpa copy-paste)

### Masalah
Sekarang tiap tool = 1 folder + 1 `NewTools()`. Kalau butuh "logic sama, target beda" (contoh: GSheet downloader ke 2 sheet berbeda), harus duplikasi kode.

### 3 opsi

**A. `New(cfg)` per-instance** — eksplisit, register berkali-kali
```go
gsheet.New(gsheet.Config{Key:"sales", Name:"Sales Sheet", SheetID:"..."})
gsheet.New(gsheet.Config{Key:"ops",   Name:"Ops Sheet",   SheetID:"..."})
```
- ✅ Route clean (`/tools/sales`, `/tools/ops`), tiap instance bisa beda icon/tag.
- ❌ Nambah instance = edit Go + rebuild.

**B. `NewMulti(cfgs...)` — satu card, banyak sub-route**
```go
gsheet.NewMulti([]Config{{Key:"sales",...},{Key:"ops",...}})
// render 1 card "GSheet Downloader" + dropdown pilih target
```
- ✅ 1 card di grid, user pilih target dari UI.
- ❌ Card jadi rame kalau 10+; susah kasih tag/icon berbeda.

**C. Instance = data, bukan code** ⭐ paling fleksibel jangka panjang
- Module `gsheet-download` daftar sekali.
- User bikin "preset" dari admin UI → simpan di tabel `tool_instances(owner, key, name, config_json)`.
- Tiap preset muncul sebagai card sendiri di grid.
- ✅ Nambah sheet = klik, no rebuild. Cocok nyambung ke #3 (connection).
- ❌ Perlu migration + CRUD UI.

### Rekomendasi
Mulai **A** (1 hari kerja, unblock sekarang). Kalau udah kerasa ada 5+ instance, upgrade ke **C**. Skip **B** — UX jelek di tengah.

### Catatan backward-compat
`NewTools()` tetap ada (single default instance) + tambah `NewWith(cfg)` (kustom). Tool existing gak kena breaking change.

---

## 2. Env per-tool — config + default, UI-editable

### Prinsip
Jangan bikin `.env` baru atau folder env terpisah. **Satu source of truth** = `app_variables` table yang udah ada di [configs/spec.go](../internal/configs/spec.go). Extend, jangan duplikat.

### Struktur Spec

Extend `Spec` dengan 2 field:
```go
type Spec struct {
    Owner string   // "" = app, "gsheet-download" = tool
    Scope string   // "global" | "instance:<key>"
    // ...existing fields (Key, Type, Default, Description, IsSecret, ...)
}
```

### Cara tool daftar
```go
func (h *Handler) Specs() []configs.Spec {
    return []configs.Spec{
        {Owner:"gsheet-download", Key:"api_key", IsSecret:true, ...},
    }
}
```
Bootstrap loop semua module, kumpulin Spec, reconcile ke DB — mekanisme sama kayak sekarang.

### UI — 4 opsi

**A. Inline di tool page** (cepet, simple)
- Tombol ⚙️ pojok kanan tool → modal form render Spec `Owner=<tool-key>`.
- User gak pindah halaman.
- ❌ Admin gak liat global state semua tool.

**B. Central page `/admin/tool-settings`** (kayak job settings sekarang)
- 1 halaman, accordion per tool.
- Rapih, admin-friendly.
- ❌ User bolak-balik pas setup tool baru.

**C. Hybrid** ⭐ saran
- Central page ADA (admin overview + bulk edit).
- Tool belum ke-config → banner di atas UI-nya: "⚠️ Setup required — API key missing. [Configure]". Deep-link ke section tool itu di central page.
- User baru: keliatan banner, klik, isi, balik. Admin: liat semua di 1 tempat.

**D. Per-tool "Setup" sub-page** (paling scalable, paling kerja)
- Tool bisa override halaman setup sendiri (GSheet butuh upload service-account JSON, beda dari input biasa).
- Default: auto-generate dari Spec.
- Override: tool implement `SetupView()` kalau butuh UX khusus.
- Ini yang dipake Retool, n8n, Zapier.

### Rekomendasi
Mulai **C** (hybrid). Naik ke **D** pas ada tool butuh UX non-standar (OAuth flow, file upload, test connection button).

---

## 3. Connection manager (Slack / Notion / GSheet)

### Pisah tabel dari Spec
```
app_variables     → Spec-based, per tool/app (API key, config value)
connections       → OAuth token, refresh token, expiry, scopes
```

**Kenapa dipisah:** OAuth punya lifecycle (refresh, revoke, re-consent). Kalau dicampur ke `app_variables` bakal kotor.

### Struktur admin

```
/admin
 ├── /settings          → app-level (nama, URL, session secret) — existing
 ├── /tool-settings     → Spec per tool/instance (dari #2 opsi C)
 └── /connections       → OAuth providers (Google, Slack, Notion)
```

### Tool declare kebutuhan di Meta()
```go
func (h *Handler) Meta() ui.Meta {
    return ui.Meta{
        Key: "gsheet-download",
        Requires: ui.Requires{
            Specs:       []string{"sheet_id"},
            Connections: []ui.ConnReq{
                {Provider:"google", Scope:"sheets.ro"},
            },
        },
    }
}
```

### Framework otomatis
- Cek `Requires` → kalau belum lengkap, card di grid dapat badge 🟡 + tool page render banner "Setup required" dengan CTA ke halaman yang bener.
- Kalau udah lengkap → badge 🟢 + tool jalan normal.
- Di handler: `connGoogle, _ := app.Conn(ctx, "google")` — dapet client yang udah fresh (auto-refresh token).

### Tiering module — UX badge

| `Kind` | Arti | Badge |
|---|---|---|
| `parser` | Pure JS, langsung jalan (convert-text, wa-me) | 🟢 ready |
| `env` | Butuh Spec terisi | 🟡 needs config |
| `connection` | Butuh OAuth | 🔴 not connected |

### Step bertahap (jangan langsung OAuth generic)

1. **Step 1 — API key first** (1-2 hari): `Kind:"env"` + `IsSecret:true` cukup buat Notion, Slack webhook, GSheet service account JSON. 80% use-case beres tanpa OAuth.
2. **Step 2 — Connection table** (nanti): pas butuh Slack user-level / GSheet user-scope, tambah `connections` table + 1 provider (pilih yang paling sering dipake dulu).
3. **Step 3 — Generic OAuth** (paling akhir): setelah 2-3 provider, baru abstraksi.

---

## 4. Permission model — siapa yang bisa manage connection

### Pertanyaan user
"Kenapa harus admin yang bisa nambah connection? Tapi admin bisa liat semua connection, gak sih?"

### 3 pilihan

**A. Admin-only** — admin connect, semua user pake
- ✅ Simple, 1 connection = semua orang dapet.
- ❌ Audit log provider (Google) nunjuk admin, bukan user asli.
- ❌ User gak bisa pake akun pribadi mereka.

**B. User-owned, private** — tiap user connect sendiri
- ✅ Audit trail bener, privacy bagus.
- ❌ 10 user = 10x connect → friction.
- ❌ Admin gak bisa monitor siapa connect apa.

**C. Hybrid** ⭐
- **Shared** (admin-managed): admin connect "Company Google" → semua user bisa pake. Buat tool internal (dashboard, shared workspace).
- **Personal** (user-managed): user connect akun sendiri dari `/settings/my-connections`. Buat tool yang butuh identitas user (post Slack as me, Notion personal).

Tool declare mana yang dia butuh:
```go
Connections: []ui.ConnReq{
    {Provider:"google", Scope:"sheets.ro", Owner:"shared"},
    {Provider:"slack",  Scope:"chat.write", Owner:"personal"},
}
```

### Visibility

| Siapa | Liat apa |
|---|---|
| Admin | Semua shared + **metadata** personal (nama user, provider, connected at) — gak liat token |
| User biasa | Shared (read-only status) + personal milik sendiri |

Admin perlu liat metadata personal buat:
- Revoke access kalau user keluar perusahaan.
- Debug "kenapa tool X error buat si Budi".
- Audit compliance.

**Token content gak pernah visible di UI** — cuma dipake internal buat API call.

### Rekomendasi
- Tim kecil (< 5 orang), trusted: **A** selamanya cukup.
- Multi-team / compliance: langsung **C**.
- Default: mulai **A** (shared), tambah **Personal** pas ada tool yang butuh.

---

## 5. Switch connection di tool — per-field permission model

### Requirement user
1. Tool stateless + punya config.
2. Config bisa diganti user (kalau punya akses).
3. Admin decide: "config X + connection Y ini user boleh override atau gak?"

Ini bukan 3 mode berbeda, tapi **per-field permission** dengan `Locked` flag.

### Desain

Tiap Spec + ConnReq punya `Locked bool` (admin-controlled):

```go
Specs: []configs.Spec{
    {Key:"sheet_id",  Owner:"gsheet", Locked:false, Default:"..."},  // user boleh ganti
    {Key:"api_base",  Owner:"gsheet", Locked:true,  Default:"..."},  // admin-only
},
Connections: []ui.ConnReq{
    {Provider:"google", Locked:false},  // user boleh switch ke akun sendiri
},
```

### Siapa bisa apa

| Field state | Admin | User biasa |
|---|---|---|
| `Locked:true` | edit | read-only, pake value admin |
| `Locked:false` | set default | override buat dirinya sendiri |

### Storage — 2 layer

```
app_variables (global)        → default value, set admin
user_overrides (per-user)     → override user, cuma untuk field Locked:false
```

Pas tool run:
```
effective_value = user_override ?? global_default
```

Sama pattern buat connection: global = connection yang admin bind ke tool (shared); user override = user pilih sendiri dari `/settings/my-connections`.

### UI flow

**Admin di `/admin/tool-settings/gsheet`:**
```
sheet_id   [abc123___]  ☐ Lock (user can override)
api_base   [https://]   ☑ Lock (admin only)
Google     [Company GA ▾]  ☐ Lock
```

**User di tool page:**
```
┌──────────────────────────────────────┐
│ GSheet Downloader                    │
├──────────────────────────────────────┤
│ Sheet ID: [abc123] ← editable         │
│ API base: [https://...] 🔒 (locked)   │
│ Google:   [Company ▾] ← bisa switch   │
│           ├ Company Google (shared)   │
│           └ My Personal (personal)    │
└──────────────────────────────────────┘
```

Lock icon 🔒 di field locked → user paham kenapa gak bisa edit.

### Keuntungan

- ✅ Satu mental model — gak ada "mode", cuma flag per-field.
- ✅ Admin in control — kasih freedom selektif (boleh ganti sheet, gak boleh ganti API endpoint).
- ✅ User gak confused — yang locked keliatan jelas.
- ✅ Skala ke banyak tool — framework yang handle, tool cuma declare.
- ✅ Audit-friendly — `user_overrides` punya `user_id + updated_at`.

### Edge case

1. **Connection shared — user switch ke personal, boleh?**
   - `Locked:false` → boleh. User "bring your own" account.
   - Admin bisa restrict scope: `AllowOverrideTo:"personal-only"` biar gak bisa pick shared connection orang lain.

2. **User override expired/invalid** (connection revoked, value kosong)
   - Auto fallback ke global default, kasih toast "Your override is invalid, using default".

3. **Admin ganti default, user udah punya override**
   - Override user menang (by design). Admin UI: "3 users have overrides — [View] [Force reset]".

### Interaksi sama multi-instance (#1 opsi C)

Kalau pattern udah instance-based (tool_instances sebagai data):
- Tiap instance punya `connection_id` sendiri di `config_json`.
- "Switch" = pindah ke instance lain, gak perlu dropdown switcher di dalam tool.
- Dua instance = dua card di grid, user klik yang mana aja.

Jadi kombinasi yang masuk akal:
- **Stateless form** (GSheet downloader) → Spec + ConnReq dengan `Locked:false`, user override per-field.
- **Punya workspace/state** (dashboard, explorer) → instance-based, tiap workspace = card sendiri.

---

## 6. Roadmap implementasi

| Step | Scope | Effort | Unblocks |
|---|---|---|---|
| 2.1 | Tambah `Owner` + `Locked` di Spec + `Specs()` di Module interface | 0.5 hari | semua per-tool env |
| 2.2 | `/admin/tool-settings` page (accordion per tool) | 1 hari | opsi B working |
| 2.3 | Banner "Setup required" di tool yang incomplete | 0.5 hari | opsi C hybrid done |
| 2.4 | `user_overrides` table + repo | 0.5 hari | per-user override |
| 2.5 | Admin UI lock toggle + user UI editable vs locked | 0.5 hari | permission model done |
| 3.1 | Migration `connections` table + repo | 0.5 hari | pondasi OAuth |
| 3.2 | Generic OAuth handler + 1 provider (Google dulu) | 1-2 hari | GSheet tool bisa jalan |
| 3.3 | `/admin/connections` page | 1 hari | user bisa manage |
| 3.4 | `Requires` di Meta + auto-banner/badge | 0.5 hari | UX nyambung |

**Total ~7.5 hari.**

### Urutan disarankan
1. **Step 2.1** dulu — paling kecil, unblock semua langkah lain. Bisa di-test di tool existing (metabase-query, wa-me) tanpa connection.
2. Step 2.2 + 2.3 → per-tool config UI working end-to-end.
3. Step 2.4 + 2.5 → permission model lengkap.
4. Step 3.x → connection baru setelah Spec-based env udah stabil.

### Prinsip yang di-pegang
- **Jangan over-engineer di awal**. Mulai admin-only shared connection (opsi A), tambah personal pas ada tool yang butuh.
- **Satu source of truth** per konsep. `app_variables` buat Spec, `connections` buat OAuth, `user_overrides` buat per-user — jangan campur.
- **Tool declare, framework handle**. Tool cuma bilang "aku butuh X + Y", framework yang render banner/badge/CTA.
- **API key first, OAuth later**. 80% integrasi internal beres pake API key / webhook. OAuth mahal dibangun, tunda sampai benar-benar butuh.

---

## 7. Jalur komunikasi — tool, job, connection

Bagian ini jawab pertanyaan: **"gimana cara manggil dan urutan nya"** dari beberapa sudut pandang (kontrak kode, runtime request, startup, resolusi value, sampai diagram).

### 7.1 Kontrak existing (sebelum perubahan)

Biar context jelas, ini kontrak sekarang di [pkg/tool/tool.go](../pkg/tool/tool.go) dan [pkg/job/job.go](../pkg/job/job.go):

```go
// Tool
type Module interface {
    Meta() Tool
    Register(mux *http.ServeMux, render RenderFunc)
}

// Job
type Job interface {
    Meta() Meta
    Run(ctx context.Context) (string, error)
}
```

Tool/job gak punya jalur ke `configs.Service` atau `connections`. Mereka berdiri sendiri. Desain di bawah nambahin **inject context** tanpa breaking existing tool.

### 7.2 Sudut pandang 1 — Kontrak API baru

**Prinsip:** tool/job gak `import configs` atau `import connections` langsung. Mereka terima akses via context yang udah **ter-scope** (tau siapa user-nya, tool apa yg manggil).

#### Module interface extension (opsional, backward-compat)

```go
// Interface lama tetap hidup
type Module interface {
    Meta() Tool
    Register(mux *http.ServeMux, render RenderFunc)
}

// Kalau butuh Spec/Connection, implement yg ini (superset)
type ConfigurableModule interface {
    Module
    Specs() []configs.Spec   // declare env/config
    Requires() Requires      // declare connection kebutuhan
}
```

Tool existing (convert-text, wa-me, oc-sticker) **gak perlu diubah** — framework skip config/connection setup buat mereka.

#### 3 primitive yang di-inject

**`ToolCtx`** — request-scoped, dipakai di handler tool:
```go
type ToolCtx interface {
    Cfg(key string) string             // baca Spec (user override → global default)
    Secret(key string) string          // sama, tapi Spec IsSecret
    Conn(provider string) (Connection, error)
    User() User                        // siapa yg manggil
    Log() zerolog.Logger
}

// Cara ambil dari handler
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
    tc := toolctx.From(r.Context())
    sheetID := tc.Cfg("sheet_id")
    g, _ := tc.Conn("google")
    // ... pake g
}
```

**`JobCtx`** — scheduler-scoped, dipakai di `Run()`:
```go
type JobCtx interface {
    Cfg(key string) string
    Secret(key string) string
    Conn(provider string) (Connection, error)
    Log() zerolog.Logger
    // NO User() — job jalan tanpa user, pake shared connection aja
}

// Signature baru (non-breaking via context.Context)
func (h *Handler) Run(ctx context.Context) (string, error) {
    jc := jobctx.From(ctx)
    sheetID := jc.Cfg("sheet_id")
    g, _ := jc.Conn("google")
    // ...
}
```

**`Connection`** — abstraksi di atas OAuth/API key:
```go
type Connection interface {
    HTTPClient() *http.Client   // udah auto-refresh token
    Token() string              // raw, buat Authorization header manual
    Provider() string
    Owner() string              // "shared" | "personal"
}
```

Tool gak peduli itu OAuth atau API key — dapetin `HTTPClient()` siap pakai.

### 7.3 Sudut pandang 2 — Runtime request (tool HTTP)

Urutan apa yg terjadi pas user klik tool di grid dan submit form:

```
1. HTTP request masuk ke chi router
   ↓
2. Middleware chain:
   a. session middleware    → resolve user dari cookie
   b. auth middleware       → redirect ke login kalau belum
   c. visibility middleware → cek user berhak akses tool ini
   d. toolctx middleware    → build & inject ToolCtx:
      ├─ Load Spec values (global_default + user_override)
      ├─ Resolve Connection (shared binding atau user personal)
      └─ Put ke r.Context() via toolctx.key
   ↓
3. Handler tool jalan:
   tc := toolctx.From(r.Context())
   sheetID := tc.Cfg("sheet_id")
   g, err := tc.Conn("google")
   ↓
4. Handler render templ / stream response
```

Kunci: **middleware yg resolve, handler tinggal pakai**. Handler gak tau logic override/fallback — itu urusan middleware.

### 7.4 Sudut pandang 3 — Runtime job (cron worker)

Worker jalan terpisah dari server HTTP. Urutan:

```
1. Cron scheduler fire sesuai schedule di tabel `jobs`
   ↓
2. Job dispatcher:
   a. Load Spec values (global only — job gak punya user context)
   b. Resolve Connection (shared only — personal gak applicable)
   c. Build JobCtx, taruh di context.Context
   ↓
3. jobService.Run(ctx)
   ↓ (hit handler.Run)
4. handler.Run(ctx):
   jc := jobctx.From(ctx)
   cfg := jc.Cfg(...)
   g, _ := jc.Conn("google")
   ↓
5. Return (markdown, error) → worker simpan ke run log
```

Beda utama dari tool: **gak ada user**, jadi connection `Owner:"personal"` gak bisa dipakai dari job. Kalau tool butuh personal connection dan juga versi cron-nya, tool harus kasih Spec `run_as_user_id` biar job tau identitas mana yg dipake.

### 7.5 Sudut pandang 4 — Startup / bootstrap order

Urutan strict — tiap step butuh output step sebelumnya:

```
1. DB connect (gorm open)
   ↓
2. configs.Service.Bootstrap(ctx)
   → Reconcile app-level Spec (app_name, app_url, session_secret)
   ↓
3. Module discovery:
   for m in tools.All() + jobs.All():
       if m implements ConfigurableModule:
           configs.ReconcileSpecs(m.Specs())  // scoped by Owner
   ↓
4. connections.Service.Bootstrap(ctx)
   → Load semua connection dari DB ke cache
   → Cek token expired → mark need-refresh
   ↓
5. Module registration:
   for m in tools.All():
       m.Register(mux, render)
   for j in jobs.All():
       scheduler.Register(j)
   ↓
6. Middleware chain dibentuk:
   router.Use(session, auth, visibility, toolctx)
   ↓
7. HTTP server start (atau worker start)
```

Ini **extend** bootstrap existing — sekarang udah ada step 1, 2, 5, 7. Yg baru: step 3 (module Spec reconcile) dan step 4 (connection bootstrap).

### 7.6 Sudut pandang 5 — Resolusi value (precedence chain)

Pas `tc.Cfg("sheet_id")` dipanggil, framework jalanin urutan ini:

```
┌─────────────────────────────────────────────────────┐
│ tc.Cfg("sheet_id")                                  │
│                                                     │
│  1. Spec.Locked? ─────── yes ──→ skip step 2        │
│                                                     │
│  2. user_overrides[user_id][tool][key]              │
│     ada? ─── yes ──→ return                         │
│                                                     │
│  3. app_variables[owner=tool][key=sheet_id]         │
│     ada? ─── yes ──→ return                         │
│                                                     │
│  4. Spec.Default (dari kode)                        │
│                                                     │
│  5. return ""                                       │
└─────────────────────────────────────────────────────┘
```

Buat `tc.Conn("google")`:

```
┌─────────────────────────────────────────────────────┐
│ tc.Conn("google")                                   │
│                                                     │
│  1. ConnReq.Locked? ─── yes ──→ skip step 2         │
│                                                     │
│  2. user_overrides[user][tool][conn:google]         │
│     ada + valid (belum revoke)? ─── yes ──→ return  │
│                                                     │
│  3. tool_connections[tool][provider=google]         │
│     ada + valid? ─── yes ──→ return                 │
│                                                     │
│  4. return ErrNotConfigured                         │
│     → framework render banner "Connect Google"      │
└─────────────────────────────────────────────────────┘
```

### 7.7 Sudut pandang 6 — Contoh lengkap (GSheet Downloader)

Tool yg ngumpulin semuanya:

```go
package gsheet

type Handler struct{}

func New() *Handler { return &Handler{} }

// ─── Meta (existing) ─────────────────────────
func (h *Handler) Meta() tool.Tool {
    return tool.Tool{
        Name:        "GSheet Downloader",
        Description: "Download Google Sheet as CSV",
        Path:        "/tools/gsheet",
        Icon:        "📊",
    }
}

// ─── Specs (baru, opsional) ──────────────────
func (h *Handler) Specs() []configs.Spec {
    return []configs.Spec{
        {Owner:"gsheet", Key:"sheet_id", Locked:false,
         Description:"Target sheet ID"},
        {Owner:"gsheet", Key:"range", Locked:false, Default:"A:Z"},
    }
}

// ─── Requires (baru, opsional) ───────────────
func (h *Handler) Requires() tool.Requires {
    return tool.Requires{
        Connections: []tool.ConnReq{
            {Provider:"google", Scope:"sheets.readonly", Locked:false},
        },
    }
}

// ─── Register (existing) ─────────────────────
func (h *Handler) Register(mux *http.ServeMux, render tool.RenderFunc) {
    mux.HandleFunc("GET /tools/gsheet", h.view(render))
    mux.HandleFunc("POST /tools/gsheet/download", h.download)
}

// ─── Handler ─────────────────────────────────
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
    tc := toolctx.From(r.Context())

    sheetID := tc.Cfg("sheet_id")
    rng := tc.Cfg("range")

    g, err := tc.Conn("google")
    if err != nil {
        http.Error(w, "Google not connected", 400); return
    }

    resp, err := g.HTTPClient().Get(fmt.Sprintf(
        "https://sheets.googleapis.com/v4/spreadsheets/%s/values/%s",
        sheetID, rng))
    // ... stream CSV ke w
}
```

Runtime flow pas user POST `/tools/gsheet/download`:

```
request
  ↓
session    → user = {id:"u123", role:"user"}
  ↓
auth       → ok
  ↓
visibility → "/tools/gsheet" allowed for role "user"
  ↓
toolctx    → resolve:
             sheet_id = user_overrides["u123"]["gsheet"]["sheet_id"]
                      ?? app_variables["gsheet"]["sheet_id"]
                      ?? Spec.Default ("")
             range = same chain
             google = user_pref["u123"]["google"]
                    ?? tool_connections["gsheet"]["google"]
             → inject ToolCtx into r.Context()
  ↓
h.download(w, r):
  tc.Cfg("sheet_id") → "abc123"
  tc.Conn("google")  → Connection{client auto-refresh}
  → fetch sheets API
  → stream CSV
```

### 7.8 Sudut pandang 7 — Diagram keseluruhan

```
┌─────────────────────────────────────────────────────────────┐
│ STARTUP                                                      │
│                                                              │
│  DB                                                          │
│   ↓                                                          │
│  configs.Bootstrap       (app-level spec)                    │
│   ↓                                                          │
│  for each module:                                            │
│     module.Specs()  →  reconcile (Owner-scoped)              │
│     module.Requires() → index                                │
│   ↓                                                          │
│  connections.Bootstrap  (load token cache)                   │
│   ↓                                                          │
│  module.Register(mux)                                        │
│   ↓                                                          │
│  middleware chain: session → auth → visibility → toolctx     │
│   ↓                                                          │
│  http.ListenAndServe    /   worker.Start()                   │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ RUNTIME — Tool (HTTP request)                                │
│                                                              │
│  HTTP req                                                    │
│    ↓                                                         │
│  session  → user resolved                                    │
│    ↓                                                         │
│  auth     → allowed                                          │
│    ↓                                                         │
│  visibility → tool accessible                                │
│    ↓                                                         │
│  toolctx  → build ToolCtx:                                   │
│                • load spec (user override → app default)     │
│                • resolve connection (user pref → shared)     │
│                • put in r.Context()                          │
│    ↓                                                         │
│  handler.X(w, r)                                             │
│    tc := toolctx.From(r.Context())                           │
│    tc.Cfg(...) / tc.Conn(...)                                │
│    ↓                                                         │
│  response                                                    │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ RUNTIME — Job (cron fire)                                    │
│                                                              │
│  scheduler tick                                              │
│    ↓                                                         │
│  dispatcher                                                  │
│    ↓                                                         │
│  build JobCtx (no user, shared connection only)              │
│    ↓                                                         │
│  put in context.Context                                      │
│    ↓                                                         │
│  handler.Run(ctx)                                            │
│    jc := jobctx.From(ctx)                                    │
│    jc.Cfg(...) / jc.Conn(...)                                │
│    ↓                                                         │
│  (markdown, err) → run log                                   │
└─────────────────────────────────────────────────────────────┘
```

### 7.9 Sudut pandang 8 — Mapping ke kode existing

Dari kontrak skrg di repo:

| Komponen | File sekarang | Yg perlu diubah |
|---|---|---|
| Tool contract | [pkg/tool/tool.go](../pkg/tool/tool.go) | Tambah `Requires` struct + `ConfigurableModule` interface |
| Job contract | [pkg/job/job.go](../pkg/job/job.go) | `Run` signature tetap, tambah konvensi ambil dari `ctx` |
| Spec | [internal/configs/spec.go](../internal/configs/spec.go) | Tambah field `Owner` + `Locked` |
| Config service | [internal/configs/service.go](../internal/configs/service.go) | `Bootstrap` loop `module.Specs()`, tambah `Get(owner,key)` overload |
| Tool registry | [internal/tools/registry.go](../internal/tools/registry.go) | Gak berubah |
| Job registry | [internal/jobs/registry.go](../internal/jobs/registry.go) | Gak berubah |
| ToolCtx (baru) | `internal/pkg/toolctx/` | Bikin package baru — middleware + accessor |
| JobCtx (baru) | `internal/pkg/jobctx/` | Bikin package baru |
| Connections (baru) | `internal/connections/` | Service + repo + OAuth handler |
| Middleware chain | `internal/pkg/api/server.go` (kira-kira) | Tambah `toolctx.Middleware()` ke chain |

### 7.10 Prinsip komunikasi (ringkas)

- **Tool/job gak `import configs` langsung** — selalu via `ToolCtx` / `JobCtx`.
- **Middleware yg resolve**, handler tinggal pake. Logic override/fallback di satu tempat.
- **Context = scope**. `ToolCtx` tau user + tool + request. `JobCtx` tau cuma tool.
- **Connection abstract di balik `HTTPClient()`**. Tool gak tau OAuth vs API key.
- **Declare first, use later**. Tool deklarasi `Specs()` + `Requires()` di startup → framework validasi pas runtime → user dapet banner kalau incomplete.
- **Backward compat**: tool tanpa `Specs()`/`Requires()` jalan apa adanya kayak sekarang.
