# Design: AI Router — dari 9router jadi payung multi-router

Status: IMPLEMENTED (2026-07-12) — semua work-stream W0–W11 selesai + terverifikasi (go build/vet/test hijau, FE airouter 19 test + providers 104 test hijau). Open question §6.1 (`/_next/` concurrent) dijawab dengan opsi (c): rewrite `/_next/`→subpath + fallback active-asset-router (`ActiveAssetRouter`).
Scope: generalisasi fitur `9router` yang ada jadi **airouter** — payung yang bisa
menampung banyak "router provider" (9router, OmniRoute, dst), tiap router punya
folder sendiri + register, spawn-nya nyumbang env/args lewat hook. Full proxy,
tetap bisa diakses dari luar. FE menu jadi bisa switch/cek antar router.

Referensi router kedua: OmniRoute — <https://github.com/diegosouzapw/OmniRoute>
(npm `omniroute`, dashboard lokal port 20128, OpenAI-compatible `/v1`).

---

## TODO — work breakdown (urut kerja)

Tiap item bisa jadi 1 PR. Urutan dijaga supaya tiap tahap kompilasi + hijau.

- [x] **W0 — Scaffold paket** `internal/agents/airouter/` + subfolder `router9/`, `omniroute/`. Belum ada logic, cuma package doc + interface kosong biar layout kekunci.
- [x] **W1 — Extract core generik**: pindahin `manager.go`/`rewrite.go`/`broadcast.go`/`logbuf.go` dari `internal/tools/agents/9router/` ke `airouter/`, parameterize `pkgName`/`port`/`prefix` jadi `Descriptor`. Behaviour identik, cuma sekarang lewat descriptor.
- [x] **W2 — Registry + descriptor**: `Register(Descriptor)` / `List()` / `Get(id)`. Port allocator per-router (2 router default port 20128 → bentrok, harus di-remap loopback).
- [x] **W3 — 9router jadi router pertama**: `router9/router9.go` isi descriptor (pkg `9router`, start-args `--port/--host/--no-browser/--log/--skip-update`). Mount pindah `/9router/*` → `/airouter/9router/*`.
- [x] **W4 — Spawn hook**: `provider.RouterSpawnContribution` (injected var, no import cycle) + `router9/spawn.go` isi kontribusi claude/codex. Copot blok inline `router9Args` di [codex/spawn.go](../../../agents/provider/codex/spawn.go) + blok env di [claude/spawn.go](../../../agents/provider/claude/spawn.go).
- [x] **W5 — Config migrasi**: key `router9*` → `airouter*` (master `airouter_enabled`, per-router `airouter_<id>_autostart` / `_external`), baca key lama sebagai fallback saat boot.
- [x] **W6 — Instance field**: `Use9router`→`UseAIRouter` + `AIRouterProvider` + `AIRouterModels` + `AIRouterAPIKey`, migrasi row lama (default provider `9router`).
- [x] **W7 — Server wiring**: mount `/airouter/<id>/v1/` (unauthed) + `/airouter/<id>/` (admin) + `/_next/` handling, host-allowlist exempt per subtree, boot-autostart loop semua router.
- [x] **W8 — Tambah OmniRoute**: `omniroute/omniroute.go` (pkg `omniroute`, port via `PORT` env) + `omniroute/spawn.go`. Bukti kalau "tambah router = tambah folder + register".
- [x] **W9 — FE AI Router SPA**: `fe/agents/router9/` → `fe/agents/airouter/`, tambah **router switcher** (list router + status badge, klik = pindah dashboard). Nav "9router" → "AI Router".
- [x] **W10 — FE provider config**: `Router9Config.svelte` → `AIRouterConfig.svelte` — toggle "Route through AI Router" + picker router + model slots (di-fetch dari registry, beda per router+agent).
- [x] **W11 — Docs + test**: rename `docs/guide/agents/9router.md`, generalize test yang ada, tambah test OmniRoute.

Open question yang harus dijawab sebelum W7 selesai: **`/_next/` asset collision** kalau 2 dashboard Next.js jalan barengan (lihat §6.1).

---

## 0. Tujuan

`9router` sekarang = 1 fitur yang meng-embed 1 tool (npm `9router`) via iframe +
proxy `/9router/v1`. Kita mau naikin jadi **airouter**: 1 payung yang bisa nampung
**banyak** router provider sejenis. Sifat produk yang dituju:

1. **Multi-router** — 9router dan OmniRoute jalan berdampingan; ke depan tinggal
   tambah (Gemini-router, dst). Menu bisa switch / cek tiap router.
2. **Folder-per-router + register** — nambah router = bikin 1 folder di
   `internal/agents/airouter/<id>/` + panggil `Register(...)`. Ngak nyentuh core.
3. **Spawn via hook** — tiap router nyumbang env + args-nya sendiri ("on spawn
   tambah apa") lewat registry, jadi spawner claude/codex/gemini **ngak banyak
   berubah**. Perbedaan antar router (base URL, nama env, flag) diserap router-nya.
4. **Full proxy, tetap reachable dari luar** — path per-router `/airouter/<id>/...`,
   sama seperti `/9router/v1` sekarang yang bisa dipakai off-machine (di-gate).

Ini bukan rombak dari nol: layer 9router yang ada (manager proses, reverse proxy +
body rewrite, API proxy byte-perfect, broadcast, log buffer, autostart) semuanya
**udah generik** — cuma di-hardcode ke 1 pkg/port/prefix. Design ini
meng-generalize itu, bukan nulis ulang.

---

## 1. Kondisi sekarang (diukur dari repo)

Fitur `9router` berlapis rapi:

| Layer | File | Peran |
|---|---|---|
| Core proses+proxy | [internal/tools/agents/9router/manager.go](../../../tools/agents/9router/manager.go) | lifecycle npm pkg (install/version), lifecycle proses (start/stop/restart/wait), reverse proxy dashboard (rewrite body buat subpath), API proxy `/v1/*` (pass-through + capture), WS proxy, log buffer, broadcaster |
| Core (helper) | `9router/rewrite.go`, `broadcast.go`, `logbuf.go` | rewrite root-absolute URL, SSE request stream, tail log |
| HTTP wiring | [9router/handlers.go](../../../tools/agents/9router/handlers.go) | `ConfigStore` interface, page SPA, control endpoints, autostart hook |
| Glue app | [internal/tools/agents/router9.go](../../../tools/agents/router9.go) | back `ConfigStore` pakai config service, master switch, wrapper proxy |
| Mount | [internal/pkg/api/server.go:1646-1669](../../../pkg/api/server.go#L1646) | `/9router/v1/` (unauthed) + `/9router/` (admin) + `/_next/` (admin), boot autostart gate, host-allowlist exempt |
| Spawn | [provider/router9.go](../../../agents/provider/router9.go), [codex/spawn.go:241](../../../agents/provider/codex/spawn.go#L241), [claude/spawn.go:261](../../../agents/provider/claude/spawn.go#L261) | kalau `Instance.Use9router`, spawn CLI nunjuk `/9router/v1` + inject env/args |
| Config | [config/general.go:24-26](../../../agents/config/general.go#L24) | `Router9Enabled`, `Router9Autostart`, `Router9ExternalAPI` |
| FE | [fe/agents/router9/](../../../../fe/agents/router9/), `providers/.../Router9Config.svelte` | SPA dashboard (tab Dashboard/Requests/Settings), config per-instance |

**Hardcode yang harus di-parameterize** (semua di `manager.go`):

```go
const pkgName    = "9router"     // npm package
const port       = 20128         // dashboard port (loopback)
const MountPrefix = "/9router"    // wick-root path
args := []string{"--port", ..., "--host","127.0.0.1", "--no-browser","--log","--skip-update"}
```

Sisa `manager.go` (install, start, proxy, rewrite, capture, WS, log, status TTL) —
**generik, tinggal nerima descriptor.**

**Kontrak spawn hari ini** (yang bakal jadi isi SpawnHook tiap router):

- claude → env `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU}_MODEL` (slot per-tier, optional).
- codex → args `-c model_provider=9router -c model_providers.9router.base_url=... -c ...wire_api=responses -c auth_mode=apikey`, env `OPENAI_API_KEY`. Slot `model` + `subagent`.

---

## 2. Keputusan desain (kekunci)

Tiga fork sudah dijawab user:

1. **Concurrent** — banyak router boleh jalan barengan. Konsekuensi: dua-duanya
   default port 20128 → airouter **wajib remap** port loopback tiap router
   (alokasi port di core). Bukan "satu aktif gantian".
2. **Hard rename** `/9router` → `/airouter/9router`. Path lama `/9router*` dibuang.
   (Risiko: tool eksternal yang di-hardcode ke `/9router/v1` putus — lihat §5.)
3. **Folder-per-router** di `internal/agents/airouter/<id>/`, register terpusat.
   Pindah dari `internal/tools/agents/9router/` ke `internal/agents/airouter/`
   (sejajar paket agents lain: `provider/`, `workflow/`, `project/`).

Penamaan router provider:

| id (stabil, dipakai di path + config key) | Display | npm pkg | catatan |
|---|---|---|---|
| `9router` | `9router` | `9router` | router pertama, existing |
| `omniroute` | `OmniRoute` | `omniroute` | user nyebut "multirouter"; repo aslinya OmniRoute |

---

## 3. Arsitektur target

### 3.1 Directory layout

```
internal/agents/airouter/
  airouter.go       # package doc + public surface re-export
  descriptor.go     # Descriptor struct + RouterProvider/SpawnHook interface
  registry.go       # Register/List/Get; init() tiap router folder daftar ke sini
  manager.go        # generic Manager (dari 9router/manager.go, di-parameterize)
  proxy.go          # reverse proxy + rewrite + apiProxy + WS (dari manager.go+rewrite.go)
  broadcast.go      # per-router request broadcaster (moved)
  logbuf.go         # per-router log buffer (moved)
  handlers.go       # generic HTTP handlers, path /airouter/<id>/...
  spawn.go          # resolve router + delegate ke SpawnHook; inject ke provider
  config.go         # ConfigStore interface (per-router autostart/external + master)
  router9/
    router9.go      # Descriptor + init(){ airouter.Register(desc) }
    spawn.go        # SpawnHook: Contribute(claude/codex) + Slots
  omniroute/
    omniroute.go    # Descriptor (pkg omniroute, port via PORT env)
    spawn.go        # SpawnHook: Contribute(claude/codex)
```

Core (`airouter/*.go`) **ngak pernah** import subfolder router. Subfolder yang
import core + daftar via `init()`. Wiring app (satu file glue di `tools/agents`)
blank-import `router9` + `omniroute` biar `init()`-nya jalan.

### 3.2 Descriptor + Registry

```go
// descriptor.go
type Descriptor struct {
    ID          string // "9router" | "omniroute" — path + config key
    DisplayName string // UI label
    NpmPackage  string // "9router" | "omniroute"
    BinName     string // command di PATH (biasa == NpmPackage)
    PrefPort    int    // port default (20128); di-remap kalau bentrok
    IconSVG     string // ikon menu (inline svg)
    Hook        SpawnHook
    // Launch nyusun exe+args+env buat listen di `port` bind loopback.
    // Tiap router beda cara: 9router pakai --port flag, omniroute pakai PORT env.
    Launch func(bin string, port int) (args []string, env []string)
}

// registry.go
func Register(d Descriptor)          // dipanggil dari init() router folder
func List() []Descriptor             // buat menu switcher + boot autostart
func Get(id string) (Descriptor, bool)
```

Registry disortir stabil by ID (jangan pakai map order) biar menu konsisten.

### 3.3 Manager generik + alokasi port

`Manager` sekarang dipegang **per-router** (bukan singleton global). Registry
bikin 1 `Manager` per descriptor. Perubahan dari yang ada:

- Field `pkgName`/`port`/`prefix` → diisi dari `Descriptor` saat construct.
- Start: panggil `desc.Launch(bin, boundPort)` gantiin args hardcoded.
- **Port allocation**: `PrefPort` bisa bentrok (dua router 20128). Core cari port
  bebas mulai dari `PrefPort` (dial-check loopback), simpan `boundPort` di manager.
  Proxy + `backendReachable()` pakai `boundPort`, bukan konstanta. Port ngak pernah
  bocor ke user (semua loopback + di-proxy).

Sisanya (install/version TTL cache, WS proxy, capture+broadcast, SSE log/req
stream, status state-machine) dipindah apa adanya.

### 3.4 Path & mounting

| Sekarang | Target | Auth |
|---|---|---|
| `/9router/` | `/airouter/9router/` | admin (dashboard iframe) |
| `/9router/v1/` | `/airouter/9router/v1/` | unauthed (spawn CLI), host-exempt loopback |
| `/_next/` | lihat §6.1 | admin |
| page `/tools/agents/9router` | `/tools/agents/airouter` (+ `?router=<id>`) | admin |
| control `/tools/agents/9router/{status,start,...}` | `/tools/agents/airouter/<id>/{status,start,...}` | admin |

`server.go` ngak hardcode 1 mount lagi — dia **loop** `airouter.List()` dan mount
tiap router:

```go
for _, d := range airouter.List() {
    base := "/airouter/" + d.ID
    r.Handle(base+"/v1/", airouter.APIProxy(d.ID))                      // unauthed
    r.Handle(base+"/",    authMidd.RequireAdmin(airouter.RootProxy(d.ID)))
}
```

Host-allowlist exempt (`router9APIExempt`) → generalize jadi cek prefix
`/airouter/<id>/v1` untuk tiap router (loopback selalu exempt; off-machine exempt
kalau per-router `external` on).

### 3.5 Spawn hook (inti permintaan)

Target: spawner claude/codex **cuma manggil 1 fungsi**, sisanya router yang urus.

**Masalah import cycle**: `provider/codex` import `provider`; kalau spawner manggil
`airouter`, dan `airouter` import `provider` (butuh `Type`/`Instance`) → cycle.
**Solusi**: pola yang udah dipakai repo (`provider.SetSecretDecrypter`) — taruh
titik-panggil di `provider` sebagai fungsi var yang di-inject saat boot.

```go
// internal/agents/provider/airouter.go  (paket provider, ZERO import ke airouter)
type RouterContribution struct { Args, Env []string }

// di-set sekali saat boot oleh wiring airouter.
var routerSpawn func(ins *Instance, t Type) (RouterContribution, error)
func SetRouterSpawn(fn func(*Instance, Type) (RouterContribution, error)) { routerSpawn = fn }

// dipanggil spawner. Nil/tidak-di-set atau instance ngak pakai airouter → kosong.
func RouterSpawnContribution(ins *Instance, t Type) (RouterContribution, error) {
    if routerSpawn == nil || ins == nil || !ins.UseAIRouter { return RouterContribution{}, nil }
    return routerSpawn(ins, t)
}
```

```go
// internal/agents/airouter/spawn.go  (import provider — OK, satu arah)
type SpawnHook interface {
    // Contribute nyumbang args+env buat agent type `t`, base = wick-origin
    // "http://127.0.0.1:<WICK_PORT>/airouter/<id>/v1", key = plaintext API key.
    // Router yang ngak support suatu type balikin (nil,nil,nil).
    Contribute(t provider.Type, ins provider.Instance, base, key string) (args, env []string, err error)
    // Slots = model picker yang router ini expose buat type `t` (buat FE).
    Slots(t provider.Type) []provider.RouterSlot
}

func Init() { // dipanggil saat boot
    provider.SetRouterSpawn(func(ins *provider.Instance, t provider.Type) (provider.RouterContribution, error) {
        d, ok := Get(ins.AIRouterProvider)
        if !ok { return provider.RouterContribution{}, fmt.Errorf("unknown router %q", ins.AIRouterProvider) }
        base := "http://127.0.0.1:" + os.Getenv("WICK_PORT") + "/airouter/" + d.ID + "/v1"
        key  := resolveKey(*ins) // decrypt AIRouterAPIKey, fallback default
        a, e, err := d.Hook.Contribute(t, *ins, base, key)
        return provider.RouterContribution{Args: a, Env: e}, err
    })
}
```

Spawner jadi **tipis** — contoh codex, blok `router9Args` diganti:

```go
// codex/spawn.go — SEBELUM: ~35 baris router9Args() inline.
// SESUDAH:
contrib, err := provider.RouterSpawnContribution(opt.Instance, provider.TypeCodex)
if err != nil { return nil, err }
args = append(args, contrib.Args...)
...
cmd.Env = append(cmd.Env, contrib.Env...)
```

Isi lama pindah ke `router9/spawn.go`:

```go
func (h hook) Contribute(t provider.Type, ins provider.Instance, base, key string) (args, env []string, err error) {
    switch t {
    case provider.TypeClaude:
        env = []string{"ANTHROPIC_BASE_URL="+base, "ANTHROPIC_AUTH_TOKEN="+key}
        if m := ins.AIRouterModels["opus"];   m != "" { env = append(env, "ANTHROPIC_DEFAULT_OPUS_MODEL="+m) }
        if m := ins.AIRouterModels["sonnet"]; m != "" { env = append(env, "ANTHROPIC_DEFAULT_SONNET_MODEL="+m) }
        if m := ins.AIRouterModels["haiku"];  m != "" { env = append(env, "ANTHROPIC_DEFAULT_HAIKU_MODEL="+m) }
    case provider.TypeCodex:
        args = []string{"-c","model_provider=9router","-c","model_providers.9router.base_url="+toml(base), ... }
        if m := ins.AIRouterModels["model"]; m != "" { args = append(args, "--model", m) }
        env = []string{"OPENAI_API_KEY="+key}
    }
    return
}
```

`omniroute/spawn.go` = kontribusi analog tapi ke `/airouter/omniroute/v1` (OmniRoute
OpenAI-compatible; claude pakai `ANTHROPIC_BASE_URL` juga, codex `model_provider=omniroute`).
**Beda antar router diserap di sini** — persis "tiap router bisa beda buat gemini/claude/codex".

Payoff: nambah router = tambah folder + `Register` + implement `Contribute`/`Slots`.
Spawner `claude`/`codex`/(future `gemini`) ngak berubah lagi.

### 3.6 Config & migrasi

`config/general.go`:

| Lama | Baru |
|---|---|
| `Router9Enabled` (master) | `AIRouterEnabled` (master, default true) |
| `Router9Autostart` | per-router `airouter_<id>_autostart` |
| `Router9ExternalAPI` | per-router `airouter_<id>_external` |

Per-router knob disimpan by id (bukan field statis) karena jumlah router dinamis —
key pattern `airouter_<id>_autostart` / `airouter_<id>_external` di owner `agents`.
Master tetap 1 field statis.

**Migrasi boot** (baca key lama kalau baru kosong):
`router9enabled`→`airouter_enabled`, `router9autostart`→`airouter_9router_autostart`,
`router9external_api`→`airouter_9router_external`.

### 3.7 FE

**AI Router SPA** (`fe/agents/router9/` → `fe/agents/airouter/`): tambah **router
switcher** di header — daftar router (dari registry) + status badge; klik = ganti
`activeRouter`, iframe src `/airouter/<id>/`, endpoint status/log/req jadi
`/tools/agents/airouter/<id>/...`. Tab Dashboard/Requests/Settings tetap, tapi
scoped ke router aktif. Nav sidebar "9router" → "AI Router".

**Provider config** (`Router9Config.svelte` → `AIRouterConfig.svelte`): toggle
"Route through AI Router" + dropdown pilih router (`9router`/`OmniRoute`) + model
slot yang di-fetch dari registry (`GET /tools/agents/airouter/<id>/slots?type=claude`)
karena slot beda per router+agent + input API key (encrypted, `secret` tag).

---

## 4. Nambah router baru (payoff — checklist)

Buat nambah router `foo` ke depan:

1. `internal/agents/airouter/foo/foo.go` — isi `Descriptor{ID:"foo", NpmPackage:"foo", PrefPort:..., Launch:...}` + `func init(){ airouter.Register(desc) }`.
2. `foo/spawn.go` — implement `SpawnHook.Contribute` (env/args per agent type) + `Slots`.
3. Blank-import `_ ".../airouter/foo"` di file wiring `tools/agents`.
4. Selesai. Ngak nyentuh core, server.go, spawner, atau FE (menu auto dari registry).

---

## 5. Migrasi & back-compat

- **Instance field**: `Use9router`→`UseAIRouter`, `Router9Models`→`AIRouterModels`,
  `Router9APIKey`→`AIRouterAPIKey`, tambah `AIRouterProvider` (default `"9router"`).
  Migrasi row lama saat load (map field lama → baru, provider `9router`).
- **Config key**: fallback baca `router9*` (§3.6).
- **Path `/9router/v1` (hard rename)**: dibuang. Spawn internal aman (base URL
  dihitung ulang tiap spawn dari `WICK_PORT`). **Risiko**: kalau ada tool eksternal
  yang di-hardcode ke `/9router/v1`, itu putus. Kalau nanti kepakai, bisa tambah
  alias `/9router/v1` → `/airouter/9router/v1` (1 baris mount) — default: ngak.

---

## 6. Open questions / risiko

### 6.1 `/_next/` asset collision (HARD — jawab sebelum W7 kelar)

9router (Next.js) emit URL root-absolute `/_next/*` yang lolos body-rewriter, jadi
sekarang di-catch di root `/_next/` dan di-proxy balik. Dengan **2 dashboard
Next.js jalan barengan**, request `/_next/*` ngak bawa info router mana → ambigu.

Opsi:
- **(a) Rewrite lebih keras**: body-rewriter ubah `/_next/` → `/airouter/<id>/_next/`
  di HTML/JS. Masalah: chunk yang dirakit runtime dari fragmen JS lolos (ini
  alasan asli kenapa ada catch-all root). Perlu shim runtime / `<base>` injection.
- **(b) Referer-based**: catch `/_next/` di root, route by Referer path
  `/airouter/<id>/`. Catatan existing: Referer fragile (module-script/preload kirim
  origin-only Referer tanpa path). Bisa false-negative.
- **(c) Batasi**: cuma 1 dashboard *ke-render* pada satu waktu (menu switch), proses
  boleh concurrent tapi iframe cuma 1. `/_next/` di-route ke "foreground router" yang
  di-set saat switch (cookie/last-active). Paling simpel, cukup buat "cek router lain".

Rekomendasi awal: **(c)** — sesuai "1 dashboard aja" (proses concurrent, tapi view
1 router pada satu waktu; `/_next/` ikut router yang lagi di-view). (a) sebagai
perbaikan lanjut kalau butuh 2 iframe sekaligus.

### 6.2 Port allocation & persist

Port loopback di-remap dinamis (§3.3). Apakah perlu di-persist biar stabil antar
restart? Sementara: alokasi ulang tiap boot (loopback + di-proxy, user ngak lihat
port) — cukup. Persist kalau ada tool luar yang mau nusuk port langsung (ngak ada).

### 6.3 OmniRoute start-args

OmniRoute set port via **`PORT` env**, bukan flag; flag `--host/--no-browser/--log/
--skip-update` ala 9router belum terkonfirmasi ada. `Launch` per-router (§3.2)
sengaja fleksibel buat nyerap beda ini. Perlu verifikasi flag OmniRoute pas W8
(coba `omniroute --help`).

---

## 7. Peta perubahan file

**Pindah/rename:**
- `internal/tools/agents/9router/` → `internal/agents/airouter/` (core) + `airouter/router9/` (descriptor+hook).
- `fe/agents/router9/` → `fe/agents/airouter/`.
- `docs/guide/agents/9router.md` → `docs/guide/agents/airouter.md`.

**Edit:**
- [internal/tools/agents/router9.go](../../../tools/agents/router9.go) → glue airouter (ConfigStore per-router, master switch, wrapper proxy loop).
- [internal/pkg/api/server.go](../../../pkg/api/server.go) — mount loop `airouter.List()`, exempt generalize, boot autostart loop.
- [internal/agents/provider/codex/spawn.go](../../../agents/provider/codex/spawn.go) & [claude/spawn.go](../../../agents/provider/claude/spawn.go) — copot blok router9, panggil `RouterSpawnContribution`.
- [internal/agents/provider/router9.go](../../../agents/provider/router9.go) → `provider/airouter.go` (Instance field baru, injected `routerSpawn`, `RouterSlot`).
- [internal/agents/config/general.go](../../../agents/config/general.go) — `AIRouterEnabled` + per-router knob.
- Instance struct (field baru + migrasi).

**Baru:**
- `internal/agents/airouter/omniroute/{omniroute.go,spawn.go}`.
- `fe/agents/providers/.../AIRouterConfig.svelte`.

---

## 8. Test plan

- Pindah + generalize test existing: `9router/{rewrite,broadcast,logbuf,proxy_capture}_test.go`, `router9_gate_test.go`, `provider/router9_test.go`, `codex/spawn_router9_test.go`.
- Baru: registry (Register/List/Get, port allocator pilih port bebas), spawn-hook resolve (instance → router → contribution) buat 9router **dan** omniroute, config migrasi (key lama → baru), instance-field migrasi.
- Gate test tetap: master off = semua surface mati (404); non-admin = 403 dashboard/control; `/airouter/<id>/v1` loopback exempt, off-machine 403 kecuali external on.
