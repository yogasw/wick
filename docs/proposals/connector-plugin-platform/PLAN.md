# Wick Connector — Plugin-over-gRPC Architecture Plan

> Proposal: pindah dari connector **in-process** (compiled in core) ke model
> **plugin eksternal** yang di-build terpisah, di-versioning sendiri, di-download
> per kebutuhan, dan berkomunikasi dengan core lewat gRPC (pakai
> `hashicorp/go-plugin`).

---

## 1. Kondisi sekarang (baseline)

```
┌───────────────────────────────────────┐
│            wick (1 binary)             │
│                                        │
│   MCP layer  ──►  connector registry   │
│                       │                │
│        ┌──────────────┼──────────────┐ │
│        ▼              ▼              ▼ │
│    github         slack         notion │   ← semua in-process (Go)
│   (Go pkg)       (Go pkg)      (Go pkg) │
└───────────────────────────────────────┘
```

- Semua connector dikompilasi jadi satu binary.
- Config + credential lewat dashboard, disimpan terenkripsi (`wick_enc_`).
- Tambah connector baru = recompile + release ulang seluruh core.
- Call connector = function call biasa (~nanodetik, zero IPC).

**Masalah yang mau dipecahkan:**
1. Core makin gemuk tiap nambah connector.
2. Tiap connector baru / fix kecil = release seluruh binary.
3. Tidak bisa marketplace "download connector yang dibutuhkan saja".
4. Crash / bug di satu connector bisa bawa mati seluruh proses.
5. Susah nerima kontribusi pihak ketiga (harus masuk ke source core).
6. Build lama — ubah 1 connector = rebuild seluruh core (berat di Termux/arm64).

---

## 2. Target arsitektur (plugin-over-gRPC)

```
┌─────────────────────┐         ┌──────────────────────────┐
│   wick core (host)  │         │  connector plugin (proc)  │
│                     │  exec   │                            │
│  plugin manager  ───┼────────►│  github-connector (binary) │
│        │            │handshake│                            │
│        │  gRPC over │◄────────┤  expose service:           │
│        │  UDS/stdio │  port   │   - Schema()               │
│        ├───────────►│ Execute │   - Execute(op, args)      │
│        │            │◄────────┤   - Validate()             │
│        │            │ result  │   - Health()               │
│        ▼            │         │                            │
│  in-proc fast-path  │         │  (Go / Rust / Python /...) │
│  (core connectors)  │         └──────────────────────────┘
└─────────────────────┘
```

- Host cuma tahu **kontrak proto**, tidak peduli bahasa plugin.
- MCP layer (`wick_get`/`wick_execute`) jadi **fasad** — client (Claude) ngk
  tahu bedanya connector in-proc vs plugin. Migrasi transparan ke user.

---

## 3. PRINSIP INTI: pisahkan METADATA dari PROSES

Ini fondasi semua keputusan di bawah.

```
manifest.json (STATIS, data)        proses plugin (RUNTIME)
─────────────────────────           ──────────────────────
name, version, proto_version        cuma hidup saat Execute()
schema, operations, config fields   lazy spawn, idle kill
os/arch, sha256, signature          0 RAM saat idle
        │
        ▼
wick_list  → baca manifest semua connector    ← ZERO spawn, data doang
wick_get   → baca manifest 1 connector         ← ZERO spawn, data doang
wick_execute → spawn (kalau belum) → jalanin    ← baru ada proses
```

`list`/`get` cukup baca file manifest — **tidak membangunkan proses apa pun.**
Proses plugin baru hidup saat `execute`.

---

## 4. Tidak semua connector jadi plugin (HYBRID)

| Connector | Model | Alasan |
|-----------|-------|--------|
| `wickmanager`, `workflow`, `custom-connector` | **in-proc** | akses internal perut wick; lewat IPC malah ngaco |
| `github`, `slack`, `notion`, `context7`, dll | **plugin** | cuma manggil API luar; aman dipisah proses |

**Aturan:** connector yang cuma panggil API eksternal → plugin. Connector yang
menyentuh state internal wick → tetap in-process. Hybrid selamanya OK.

---

## 5. Repo terpisah + versioning independen

```
wick core         v2.1.0
github-connector  v1.4.2   ← repo sendiri, release sendiri, tag sendiri
slack-connector   v0.9.1   ← repo sendiri, release sendiri, tag sendiri
```

- Connector versi **sama** = **ngk rebuild, ngk re-download**. Cuma yang berubah yang di-pull.
- Core naik versi ≠ semua connector ikut naik. Independen.
- Tiap connector deklarasi `proto_version`; core cek kompatibilitas saat load.
- Kontribusi pihak ketiga = repo connector sendiri, ngk perlu merge ke core.

### 5.1 Output build = 2 artifact (binary + manifest)

Tiap release connector hasilkan **2 file**: binary executable + manifest deskriptif.
Manifest = data statis (info, daftar operasi, schema). Binary = yang di-spawn
**hanya saat execute**.

```
github-connector-v1.4.2/
├── github-connector          # binary executable (spawn cuma saat execute)
└── plugin.json                # manifest: info + daftar operasi + schema (baca doang)
```

**plugin.json** (JSON, bukan XML/YAML — wick udah JSON-heavy: MCP & schema pakai
JSON, ngk perlu dep YAML, `--dump-manifest` natural keluar JSON):

```json
{
  // === identitas connector — sama kayak connector.Meta wick ===
  "key": "github",                              // Meta.Key — slug unik (dipakai di tool_id)
  "name": "GitHub",                             // Meta.Name — display name
  "description": "Repo, issue, PR via GitHub API", // Meta.Description
  "icon": "github",                             // Meta.Icon
  "kind": "connector",                          // connector | tool | job (infra sama, kontrak beda)

  // === metadata plugin (di luar Meta) ===
  "version": "1.4.2",
  "proto_version": 1,
  "entry": "./github-connector",                // binary yg di-spawn saat execute
  "os_arch": ["linux/arm64", "linux/amd64"],    // WAJIB ada arm64 utk Termux
  "sha256": "abc123...",

  // === operations DIKELOMPOKKAN per kategori — persis connector.Module.Operations []Category ===
  // (Category{ Title, Description, Ops []Operation }; tiap Op = Key/Name/Description/Destructive/Input)
  "operations": [
    { "title": "Issues", "description": "Operasi issue",
      "ops": [
        { "key": "issues.create", "name": "Create Issue", "description": "Buat issue baru",
          "destructive": false,
          "input": { "repo": "string", "title": "string", "body": "string" } }
      ] },
    { "title": "Pull Requests", "description": "Operasi PR",
      "ops": [
        { "key": "pr.merge", "name": "Merge PR", "description": "Merge pull request",
          "destructive": true,
          "input": { "repo": "string", "number": "int" } }
      ] }
  ],

  // === config (= connector.Configs / []entity.Config) — DEFAULT config, di-seed ke DB saat Register/Bootstrap ===
  "configs": [
    { "key": "token",    "type": "text", "is_secret": true,  "required": true,
      "description": "GitHub personal access token" },
    { "key": "base_url", "type": "text", "value": "https://api.github.com",
      "required": false, "description": "Override utk GitHub Enterprise" }
  ]
}
```

> **Struktur manifest = bentuk JSON dari `connector.Module` PERSIS:**
> `operations` itu **`[]Category`** (`Category{Title, Description, Ops []Operation}`),
> **bukan** array `Operation` datar — sesuai `connector.Module.Operations []Category`
> (`pkg/connector/connector.go`). Operasi datar diambil runtime via `mod.AllOps()`
> (flatten dari semua Category). Identitas (`key`/`name`/`description`/`icon`) =
> `connector.Meta`; `configs` = `[]entity.Config`. Karena manifest mirror struct
> apa adanya, **`--dump-manifest` = `json.Marshal(mod)`** → byte-identik, mustahil
> drift.

> **PENTING — yang di-`Register` itu Module utuh, bukan cuma Meta.** Default config
> (`configs`) ikut di Module. Saat `Register(mod)` → `Bootstrap` →
> `seedModuleRows()` rekonsiliasi schema config ke DB (seed row default,
> tambah/hapus field yang berubah). Persis alur connector builtin sekarang —
> plugin nebeng mekanisme seed yang sama, ngk bikin baru.

**Alur — `list`/`get` ngk pernah sentuh binary:**
```
wick_list     → scan semua plugin.json             → ZERO spawn binary
wick_get      → baca 1 plugin.json (operasi+schema) → ZERO spawn binary
wick_execute  → BARU spawn binary (entry) → jalanin op → idle kill
```
Binary tidur di disk sampai ada `execute`. Itu yang bikin **RAM idle = 0**.

**Anti-drift — generate manifest DARI binary saat build (jangan tulis tangan):**
```bash
go build -o github-connector ./...
./github-connector --dump-manifest > plugin.json   # di CI, otomatis
```
Schema jadi single-source-of-truth dari kode. Mustahil beda sama binary.

### 5.2 Layout folder runtime (generik, grup per kind)

Folder plugin = **milik app, bukan connector-spesifik**. Dikelompokkan per `kind`
biar **tools & jobs nyusul pakai infra sama** (lihat §18).

```
$WICK_DATA/plugins/                # base dir app (mis. ~/.wick-agent/.../plugins)
├── connectors/
│   ├── github/   { plugin.json, github-connector }
│   └── slack/    { plugin.json, slack-connector }
├── tools/        # (NANTI) plugin kind=tool, infra sama
│   └── ...
└── jobs/         # (NANTI) plugin kind=job, infra sama
    └── ...
```

**Ngk pakai file index (registry.json) — folder + manifest = source of truth:**

| Pertanyaan | Sumber |
|------------|--------|
| Apa yang terinstall? | **scan `plugins/*/*/plugin.json`** langsung (folder = kebenaran) |
| Enabled/disabled, config, account? | **DB wick yang sudah ada** (sama persis kayak connector sekarang: `configs` table + operation states) |
| Apa yang available (belum install)? | GitHub Releases API (cache) |

- **Startup:** scan `plugins/*/*/plugin.json` → register per `kind` (data doang, no spawn).
- **Install:** download 2 file ke `plugins/<kind>/<name>/` + verify `sha256`.
- **Disable:** flag di DB (bukan file). File tetap di disk.
- Alasan: file index = sumber kedua yang bisa **drift** dari folder asli. Scan
  langsung lebih simpel & mustahil desync. Plugin count kecil → scan tiap boot murah.

> Catatan resource: **RAM** kelar (binary ngk hidup sampai execute). **Disk**
> masih nyata (~5-15MB/binary bawa runtime) tapi cuma yang di-install yang makan.

---

## 6. Lifecycle connector (install → run → mati)

### 6.1 Siklus instalasi & status
```
install (download + verify sha256/sig)
   → register manifest ke registry        (saat startup-scan ATAU hot-reload)
   → [ enable  ⇄  disable ]               (toggle, binary tetap di disk)
   → uninstall (hapus binary + manifest)
```

**PENTING — bedakan 2 LIST berbeda:**

| Status | Dashboard / **Marketplace** (UI manusia) | `wick_list` LLM (executable) | Bisa execute? |
|--------|------------------------------------------|------------------------------|----------------|
| installed + enabled | tampil (grup **Installed**) | **ya** | ya |
| installed + disabled | tampil (grup Installed, badge off) | tidak | tidak |
| **available (not installed)** | tampil (grup **Available**, tombol Install) | tidak | tidak (install dulu) |

- **`wick_list` ke LLM** = cuma `installed + enabled` (yang beneran bisa dijalankan).
  Yang disabled / belum diinstall **ngk dikasih ke LLM** — toh ngk bisa di-execute.
- **Dashboard/marketplace (manusia)** = tampilkan **semua**. Split:
  **atas = Installed** (enabled + disabled), **bawah = Available** (siap diinstall).

### 6.2 Marketplace — sumber list "Available" dari GitHub Releases API

List connector yang **belum diinstall** ditarik dari **GitHub Releases API**.

```
LIST DASHBOARD = merge 2 sumber:
  1. INSTALLED  → scan lokal  ~/.wick-agent/agents/plugins/*/plugin.json   (offline, instan)
  2. AVAILABLE  → GitHub Releases API                                     (remote, di-cache)
        │
        ▼
  ┌─ Installed ────────────────┐
  │ github   v1.4.2  [enabled] │
  │ slack    v0.9.1  [disabled]│
  ├─ Available (marketplace) ──┤
  │ notion   v2.1.0  [Install] │
  │ jira     v1.0.0  [Install] │
  └────────────────────────────┘
```

**Cara narik dari GitHub Releases API (2 opsi):**

| Opsi | Mekanisme | Cocok |
|------|-----------|-------|
| **A. Registry repo terkurasi** (rekomendasi) | 1 repo indeks (mis. `yogasw/wick-connectors`); tiap connector = release/tag; `GET /repos/{org}/wick-connectors/releases` → daftar + asset (`plugin.json`, binary) | terkontrol, bisa di-sign, aman |
| **B. Topic discovery** | GitHub Search API `topic:wick-connector` → tiap repo `GET /releases/latest` → baca asset | terbuka, komunitas, tapi rate-limit & trust lebih berat |

**Penting — marketplace = metadata, ngk download binary:**
```
wick_list (dashboard)  → GitHub Releases API: nama, versi, deskripsi   ← ngk download apa pun
wick_get  (available)  → ambil ASSET plugin.json dari release           ← cuma manifest (~KB), bukan binary
install                → BARU download binary + plugin.json + verify sha256/sig
```
Liat operasi/schema connector available = tarik `plugin.json`-nya doang (KB), binary
tetap di server sampai user klik **Install**.

**Cache + rate-limit (GitHub API limit ketat):**
- Cache hasil Releases API (mis. 15 menit), pakai **ETag**/`If-None-Match`.
- Refresh manual ("Refresh marketplace") + refresh berkala.
- Reuse pola yang sudah ada: `CatalogRefresh()` (lihat §17) — sekarang dipakai
  buat probe MCP server; mekanisme cache+refresh-nya sama persis.

### 6.3 Kapan manifest didaftarkan
**Saat startup-scan + bisa reload** — BUKAN saat spawn:
```
startup:   scan folder plugins/ → baca manifest INSTALLED → register registry
marketplace: tarik AVAILABLE dari GitHub Releases API → cache (ngk masuk registry sampai diinstall)
install:   download → verify → taruh di plugins/<name>/ → hot-reload register
disable:   flag off → hilang dari wick_list LLM (tetap di dashboard grup Installed)
enable:    flag on → muncul lagi di wick_list
```

---

## 7. Spawn policy (kapan proses hidup & mati)

Pilih per-connector:

```
LAZY (default):    execute pertama → spawn (~10-50ms, sekali)
                   → idle timeout (mis. 5 menit no request) → kill
                   → execute lagi → spawn ulang

EAGER:             spawn saat startup → selalu warm (untuk connector super-hot)

WARM POOL:         pool N proses, scale naik-turun ikut traffic
```

**Rekomendasi wick:** LAZY + idle-kill default.
- Cold-start cuma sekali (~puluhan ms), schema udah cache → call berikut warm.
- Idle lama → kill → 0 RAM. Request lagi → spawn lagi.

### 7.1 Proteksi resource (penting di Termux RAM terbatas)
- **Cap concurrent plugin** (mis. max 5 proses; sisanya antri).
- **Idle timeout pendek** (kill cepat habis dipakai).
- **LRU evict** (RAM mepet → kill plugin paling lama nganggur).

---

## 8. Untung vs Rugi (jujur)

### Untung
| # | Keuntungan |
|---|------------|
| 1 | Build & release terpisah, versioning independen |
| 2 | Marketplace / download on-demand |
| 3 | Isolasi crash (panic plugin ≠ mati core) |
| 4 | Polyglot (Go/Rust/Python asal ngomong proto) |
| 5 | Sandbox keamanan per-proses (cgroup, seccomp, FS jail) |
| 6 | Kontribusi pihak ketiga tanpa merge core |
| 7 | Core ramping, startup cepat, RAM idle rendah |

### Rugi
| # | Kerugian |
|---|----------|
| 1 | Overhead IPC ~10-200µs/call (ketutup latency API) |
| 2 | Versi proto host↔plugin harus cocok |
| 3 | Lifecycle kompleks (spawn/health/restart/evict) |
| 4 | Build matrix OS×arch (wajib linux/arm64 utk Termux) |
| 5 | Supply-chain → wajib signing + checksum |
| 6 | RAM saat banyak plugin aktif barengan (tiap proses bawa Go runtime sendiri ~10-30MB) |
| 7 | Disk lebih besar (tiap binary bawa runtime ~5-15MB) |

---

## 9. Apakah jadi enteng & cepet? (analisis jujur)

```
ENTENG ✅   core binary kecil, startup cepat, RAM idle rendah, list/get zero-proses
SAMA   ≈    throughput per-call (I/O-bound, IPC 0.1ms ketutup API 50-500ms)
BERAT  ❌   RAM saat banyak plugin warm barengan, disk total
```

**Realita pemakaian:** dari 20 connector terinstall, biasanya cuma 2-3 aktif per
sesi. Sisanya 0 proses, 0 RAM (cuma manifest di disk). Connector dipanggil
sporadis. Dengan idle-kill, model plugin **sering lebih hemat RAM** dari monolith
di pemakaian normal — yang nganggur beneran nol, bukan nempel di core.

Worst case = **burst** (banyak connector di-trigger bareng, mis. workflow fan-out).
Dijaga pakai cap concurrent + idle timeout + LRU evict (lihat 7.1).

**Kesimpulan:** bukan "tiap call lebih ngebut", tapi **lebih modular + core lebih
ramping + skalabel + RAM idle hemat**. Untung utama = release independen,
marketplace, isolasi crash, kontribusi komunitas.

---

## 10. Build jadi lebih cepat

```
monolith:   ubah 1 connector → rebuild SELURUH core + semua connector
plugin:     ubah 1 connector → build connector itu DOANG (detik, bukan menit)
```

- **Iterasi 1 connector** → jauh lebih cepat (paling kerasa harian di Termux/arm64).
- **Build core** → lebih cepat (connector ngk ikut compile).
- **CI** → build paralel per-repo, cache per-repo, release independen.
- Trade-off jujur: **clean build SEMUA dari nol** sedikit lebih lambat (tiap
  plugin link runtime sendiri) — tapi jarang kejadian.

---

## 11. Latency — kenapa overhead ngk kerasa

Connector itu **I/O-bound** (nunggu API eksternal). IPC porsi kecil:

```
func call in-proc      ~0.000s   (ns)
gRPC UDS lokal         ~0.1ms
gRPC TCP loopback      ~0.3ms
spawn proses per-call  ~10-50ms  ← JANGAN spawn per-call
panggil API GitHub     ~50-500ms ← dominan
```

Overhead gRPC 0.1ms vs call API 50-500ms = **<0.5%**, asal proses **persisten**.

### Trik tutup overhead
1. **Persistent subprocess** — spawn sekali, pakai ribuan call.
2. **Unix Domain Socket** — skip stack TCP/IP, ~2-5× lebih cepat dari TCP localhost.
3. **Connection reuse** — gRPC HTTP/2 multiplex 1 koneksi.
4. **Schema caching** — `Schema()` sekali saat load, cache di host.
5. **Streaming RPC** — list besar pakai server-streaming.
6. **Hybrid fast-path** — connector hot tetap in-process.
7. **Warm pool** — plugin sering dipakai dijaga warm.
8. **Shared memory** — payload besar lewat mmap, gRPC kirim handle.
9. **Batching** — banyak op kecil → `ExecuteBatch`.
10. **Protobuf binary** — encode cepat & kecil, jangan double-encode JSON.

---

## 12. Kontrak proto (sketsa)

> Snippet di bawah **lengkap & compile-able** (semua message yang direferensi
> didefinisikan). Detail field bisa berkembang — anggap titik awal, bukan final.

```proto
syntax = "proto3";
package wick.connector.v1;

service Connector {
  rpc Schema   (SchemaRequest)   returns (SchemaResponse);
  rpc Execute  (ExecuteRequest)  returns (ExecuteResponse);
  rpc ExecuteStream (ExecuteRequest) returns (stream Chunk);   // bulk
  rpc Validate (ValidateRequest) returns (ValidateResponse);
  rpc Health   (HealthRequest)   returns (HealthResponse);
}

message Error {
  string code    = 1;
  string message = 2;
}

message ExecuteRequest {
  string operation = 1;          // "issues.create"
  bytes  args_json = 2;
  map<string,string> creds = 3;  // host decrypt wick_enc_ -> plain
  string request_id = 4;         // tracing
  string session_id = 5;         // jaga scoping session wick
}

message ExecuteResponse {
  bytes  result_json = 1;
  Error  error = 2;
  map<string,string> meta = 3;   // rate-limit, pagination cursor, dll
}

message Chunk {                   // potongan stream utk ExecuteStream
  bytes  data = 1;
  bool   eof  = 2;
  Error  error = 3;
}

// Schema() balikin manifest (Meta + categories + configs) sbg JSON —
// inilah yang dipakai `--dump-manifest`.
message SchemaRequest  {}
message SchemaResponse { bytes manifest_json = 1; }

message ValidateRequest  { map<string,string> creds = 1; }
message ValidateResponse { bool ok = 1; Error error = 2; }

message HealthRequest  {}
message HealthResponse { bool healthy = 1; string detail = 2; }
```

---

## 13. Keamanan & distribusi

1. **Manifest** per connector: `name, version, proto_version, os/arch, sha256, sig`.
2. **Registry**: awal cukup GitHub Releases + index JSON. Nanti registry sendiri.
3. **Verifikasi**: cek `sha256` + signature (cosign / minisign) sebelum exec.
4. **Versi proto negotiation**: handshake tolak plugin proto incompatible.
5. **Sandbox**: plugin pihak ketiga jalan dengan user/cgroup terbatas, FS
   read-only kecuali tmp, network allow-list.
6. **Credential**: host yang decrypt `wick_enc_`, kirim plaintext ke plugin
   lewat UDS + mTLS lokal (di-handle go-plugin). Plugin **ngk pernah** pegang
   master key.
7. **Dashboard config tetap di host**. Plugin cuma deklarasi butuh field apa
   lewat `Schema()`. Host simpan + enkripsi.

---

## 14. Rencana migrasi bertahap

| Fase | Isi | Risiko |
|------|-----|--------|
| 0 | Definisi `proto` + handshake + plugin manager (`hashicorp/go-plugin`) | rendah |
| 1 | 1 connector pilot di-port ke plugin (mis. github), sisanya in-proc; ukur di Termux | rendah |
| 2 | Registry sederhana + manifest + verifikasi checksum/sig | sedang |
| 3 | Lifecycle penuh: lazy spawn, idle-kill, cap concurrent, LRU evict, enable/disable | sedang |
| 4 | Sandbox + streaming + warm pool | sedang |
| 5 | Buka marketplace pihak ketiga | tinggi (supply-chain) |

---

## 15. Library

- **`github.com/hashicorp/go-plugin`** — handshake, mTLS lokal, versi nego,
  lifecycle subprocess, gRPC + net/rpc. Dipakai Terraform, Vault, Nomad, Packer.
  **Jangan tulis dari nol.**
- **`google.golang.org/grpc`** + `protoc` toolchain untuk kontrak.
- **`sigstore/cosign`** atau **minisign** untuk signing binary.

---

## 16. Keputusan kunci

| Pertanyaan | Rekomendasi |
|------------|-------------|
| Spawn per-call vs persistent? | **Persistent** |
| UDS vs TCP? | **UDS** (host sama) |
| Port semua vs hybrid? | **Hybrid** (internal in-proc, eksternal plugin) |
| Spawn policy? | **Lazy + idle-kill** + cap concurrent |
| Daftar manifest kapan? | **Startup-scan + hot-reload** (bukan saat spawn) |
| Registry sendiri vs GitHub Releases? | **GitHub Releases dulu** |
| Bahasa plugin pertama? | **Go** (buka polyglot nanti) |
| Versioning? | **Per-repo, independen** dari core |

---

## 17. Pemetaan ke kode wick NYATA (grounding)

> Dibaca dari `yogasw/wick` (clone `wick-src/`). Ini bikin rencana di atas
> nyambung ke file asli, bukan teori.

### 17.1 Temuan KUNCI: wick UDAH punya connector eksternal

Wick **sudah** punya jalur connector di luar binary core — **custom MCP connector**:
proses core ngobrol JSON-RPC over HTTP ke **MCP server remote**.

- `internal/connectors/custom/mcp_client.go` — client JSON-RPC streamable HTTP,
  `buildHeaders()` resolve auth, `decrypt()` buka `wick_enc_`.
- `internal/connectors/custom/service.go` — simpan definisi custom; 2 jenis:
  `manual` (template HTTP) & `mcp` (server eksternal).
- `CatalogRefresh()` (`internal/mcp/handlers/connectors.go`) — probe `/tools/list`
  ke MCP server, operasi live jadi ops.

**Artinya:** plugin-over-gRPC = **saudara LOKAL** dari yang sudah ada.
Custom-MCP = connector eksternal *remote (HTTP)*. Plugin-gRPC = connector
eksternal *lokal (subprocess + UDS)*. Seam-nya sudah ada — tinggal tambah
transport baru, bukan bongkar arsitektur.

### 17.2 Kontrak yang dipertahankan (jangan diubah)

`pkg/connector/connector.go`:
```go
type ExecuteFunc func(c *Ctx) (any, error)
type Operation struct { Key, Name, Description string; Input []entity.Config;
                        Execute ExecuteFunc; Destructive bool }
type Category struct { Title, Description string; Ops []Operation }
type Module struct { Meta Meta; Configs []entity.Config; Operations []Category;
                     HealthCheck HealthCheckFunc; OAuth *OAuthMeta }
type Meta struct { Key, Name, Description, Icon string; Fixed bool;
                   LiveCatalog bool; ... }
```
- `Module` (Meta + Configs + **Operations `[]Category`**) **memetakan ke plugin.json**
  apa adanya (§5.1) — `operations` di manifest = `[]Category`, operasi datar diambil
  via `mod.AllOps()`. **Bukan** array `Operation` datar.
- Flag **`LiveCatalog`** sudah ada untuk catalog dinamis (MCP). Plugin manifest
  cukup pakai mekanisme yang sama — bedanya sumber = `plugin.json` statis, bukan
  probe live.

### 17.3 Titik sambung (seam) yang harus disentuh

| # | Apa | File | Yang berubah utk plugin |
|---|-----|------|--------------------------|
| 1 | **Call site eksekusi** | `internal/connectors/service.go` (`Execute`, sekitar `op.Execute(cctx)`) | kalau connector = plugin → ganti panggilan in-proc jadi **gRPC call** ke subprocess |
| 2 | **Registry** | `internal/connectors/registry.go` (`Register`, `RegisterBuiltins`) | tambah jalur register dari hasil scan `plugin.json` (bukan cuma builtins Go) |
| 3 | **Bootstrap/startup** | `internal/connectors/service.go` (`Bootstrap`) | scan folder `plugins/` → register manifest |
| 4 | **Ctx → gRPC** | `pkg/connector/ctx.go` (`Cfg`, `Input`, `HTTP`, `Mask`, `ReportProgress`) | field `Ctx` jadi field `ExecuteRequest`; `ReportProgress` jadi server-stream |
| 5 | **Creds** | `internal/connectors/enc.go` + `service.go` (`unmaskMap`, `collectSensitiveValues`, masker) | host tetap decrypt `wick_enc_` → kirim plaintext lewat UDS+mTLS; masker tetap di host |
| 6 | **MCP dispatch** | `internal/mcp/handlers/connectors.go` (`WickList`/`WickGet`/`WickExecute`) | **ngk berubah** — udah jadi fasad; ngk peduli in-proc vs plugin |

### 17.4 Yang bikin migrasi ringan

- **Seam tunggal**: hampir semua logika plugin masuk di balik `op.Execute`.
  Connector plugin = `Module` yang `Execute`-nya stub gRPC. Sisa engine
  (rate-limit, run record, masking, audit) ngk berubah.
- **Creds flow cocok**: host sudah decrypt lalu inject ke `Ctx` (service.go
  bagian load+decrypt). Tinggal arahkan plaintext ke `ExecuteRequest.creds`
  lewat channel lokal aman, bukan ke struct in-proc.
- **Dispatch transparan**: `WickList/Get/Execute` sudah resolve lewat `Module`,
  jadi MCP client (Claude) ngk lihat bedanya.

### 17.5 Status repo saat baca

- Clone: `wick-src/` (shallow, dari `yogasw/wick` master).
- **Belum ada** `.proto`, subprocess, atau plugin loader — konfirmasi greenfield
  untuk transport plugin (poin §15 library `hashicorp/go-plugin` tetap relevan).
- Ada skill repo **`connector-module`** yang mendokumentasikan kontrak
  `pkg/connector.{Meta,Module,Operation,Ctx}` — acuan saat implement.

### 17.6 Langkah implementasi konkret (revisi Fase 0–1)

1. Definisi `proto` dari `Module`/`Operation`/`Ctx` yang ada (1:1).
2. Tambah `PluginModule` adapter: implement `connector.Module` di mana tiap
   `Operation.Execute` = stub gRPC ke subprocess (pakai `hashicorp/go-plugin`).
3. Loader: scan `plugins/*/plugin.json` → bikin `PluginModule` → `Register()`.
4. Sambungkan di `Bootstrap` (startup-scan) + reload.
5. Port **1 connector** (mis. `github`) jadi binary terpisah + `--dump-manifest`,
   sisanya tetap in-proc. Ukur latency & RAM di Termux.

---

## 18. Generalisasi: plugin platform (connector → tools → jobs)

> **Scope sekarang = connector.** Tapi infra dirancang generik biar **tools &
> jobs** nyusul tanpa bikin ulang. Folder & platform sudah disiapkan (§5.2).

### 18.1 Pisahkan PLATFORM (shared) dari KONTRAK (per-kind)

```
┌─────────────────── PLUGIN PLATFORM (shared, sekali bikin) ───────────────────┐
│ transport: gRPC + UDS + hashicorp/go-plugin                                   │
│ manifest:  plugin.json (field `kind`)                                          │
│ loader/registry: scan plugins/<kind>/*, hot-reload, enable/disable           │
│ lifecycle: lazy spawn, idle-kill, cap concurrent, LRU evict                   │
│ marketplace: GitHub Releases API, install/verify (sha256+sig), cache+ETag    │
│ security: host decrypt wick_enc_ → inject via UDS+mTLS; sandbox              │
└──────────────────────────────────────────────────────────────────────────────┘
        ▲                      ▲                       ▲
   kontrak connector      kontrak tool            kontrak job
   (proto service)        (proto service)         (proto service)
   Schema/Execute(op)/    sesuai Executor         sesuai job runner
   Validate/Health        tool wick               (schedule/run/report)
```

- **Yang dipakai bareng (90% kode):** semua di kotak atas. Bikin **sekali**.
- **Yang beda per-kind (10%):** cuma **definisi proto service** + adapter ke
  interface internal wick masing-masing.

### 18.2 Kenapa ini realistis di wick

Connector, tool, dan job di wick **sudah berbagi** tata bahasa schema yang sama —
tag `wick:"..."` (dipakai connector, tool, **dan** job; lihat skill repo
`tool-module` & `connector-module`). Jadi refleksi schema → manifest jalan sama
buat ketiganya. Platform plugin tinggal nambah 1 proto service per kind.

### 18.3 Per-kind contract (sketsa)

| Kind | Interface internal wick | Proto service plugin |
|------|--------------------------|----------------------|
| connector | `connector.Module` / `op.Execute(Ctx)` | `Schema`, `Execute(op,args)`, `Validate`, `Health` |
| tool | executor tool wick | `Schema`, `Run(input)`, `Health` |
| job | job runner (schedule + run) | `Schema`, `Run(trigger)`, `Health` (+ metadata jadwal di manifest) |

### 18.4 Urutan

1. **Sekarang:** bangun platform + kontrak **connector** (Fase 0-1 di §14).
   Platform dibikin generik dari awal (field `kind`, folder per-kind), walau cuma
   connector yang dipakai dulu.
2. **Nanti (tool):** tambah proto service `tool` + adapter. Platform ngk berubah.
3. **Nanti (job):** tambah proto service `job` + adapter (jadwal di manifest).
   Platform ngk berubah.

> Prinsip: **jangan over-build sekarang** — implement platform secukupnya buat
> connector, TAPI desain interface-nya (manifest `kind`, folder, loader) sudah
> sadar-multi-kind biar tools/jobs cuma "colok" proto service baru, bukan refactor.

---

## 19. Proto sync & versioning

> Proto = kontrak bersama core ↔ tiap plugin (repo terpisah). "Sync" = jaga semua
> repo pegang versi proto cocok **tanpa rebuild barengan**.

### 19.1 Satu sumber proto, berversi

```
wick-connector-proto/           # 1 repo / 1 folder = source of truth
├── connector.proto
├── buf.yaml                     # lint + breaking-check config
└── gen/go/...                   # stub hasil protoc, di-publish sbg Go module
```
- **Go-only (sekarang):** publish jadi **Go module berversi**
  `github.com/yogasw/wick-connector-proto`. Core + tiap plugin `go get …-proto@v1`.
  Satu dep, satu sumber — ngk ada copy-paste `.proto`.
- **Polyglot (nanti):** pakai **Buf Schema Registry (BSR)** — push proto sekali,
  tiap plugin (Rust/Python) pull + codegen di bahasanya.

### 19.2 DUA angka versi (jangan dicampur)

| Angka | Contoh | Naik kapan | Dipakai utk |
|-------|--------|-----------|-------------|
| **semver modul proto** | `v1.4.2` | tiap perubahan (minor utk additive) | dependency `go get` |
| **`proto_version`** (int) | `1`, `2` | **cuma pas breaking** | handshake host↔plugin |

`proto_version` naik **sesedikit mungkin**. Makin jarang naik = makin jarang
plugin dipaksa rebuild.

### 19.3 Disiplin kompatibilitas

| Perubahan | semver modul | `proto_version` | Plugin lama |
|-----------|--------------|-----------------|-------------|
| Tambah field (tag baru) | minor `v1.1` | **tetap** | **ngk perlu rebuild** ✅ |
| Hapus / ganti tipe / renumber tag | major `v2.0` | **naik** | wajib rebuild + migrasi |

**Aturan emas:** field tag number **ngk boleh** dipakai-ulang / di-renumber.
Cuma boleh **nambah**. Itu yang bikin plugin lama + host baru (atau sebaliknya)
tetap jalan tanpa rebuild barengan.

### 19.4 Handshake — tolak yang ngk cocok

Pakai `Negotiate`/`VersionedPlugins` bawaan `hashicorp/go-plugin`:
```
host:   "support proto_version 1, 2"
plugin: proto_version 1   → cocok → load
plugin: proto_version 3   → ngk didukung → TOLAK load + error jelas (jangan crash)
```
Breaking butuh transisi → host support **v1 DAN v2** sekaligus 1 window; plugin
migrasi sesuai tempo, v1 di-drop belakangan.

### 19.5 CI proto — breaking ketahan, ngk diam-diam

```
on: push (folder proto berubah)
  buf lint
  buf breaking --against '.git#branch=main'
    ├─ ngk ada breaking → minor bump, proto_version TETAP, codegen, publish module
    └─ ADA breaking      → BLOCK PR / label merah → manusia putuskan: emang mau proto_version++?
```
Breaking selalu lewat review manusia. Additive = auto.

---

## 20. CI/CD auto-build & release plugin

> Tujuan: connector berubah → **auto rebuild**, artifact **otomatis dinamai
> `nama-versi-os-arch`**, manifest digenerate, attach ke GitHub Release.

### 20.1 Layout repo connector (monorepo-of-connectors, opsi rekomendasi)

```
wick-connectors/                 # 1 repo, tiap subfolder = 1 connector
├── github/    { go.mod, *.go, VERSION }
├── slack/     { go.mod, *.go, VERSION }
└── .github/workflows/release.yml
```
- `VERSION` per connector = sumber versi (atau git tag `github/v1.4.2`).
- CI **path-filter**: cuma connector yang foldernya berubah yang di-rebuild.

### 20.2 Pipeline (GitHub Actions, sketsa)

```yaml
on:
  push:
    branches: [main]
    paths: ['*/**']                      # deteksi folder connector berubah
jobs:
  detect:                                # cari connector mana yg berubah
    outputs: { changed: <list folder> }  # mis. git diff --name-only → unik folder
  build:
    needs: detect
    strategy:
      matrix:
        name: ${{ fromJson(needs.detect.outputs.changed) }}
        os_arch: [linux/arm64, linux/amd64, darwin/arm64]   # WAJIB arm64 (Termux)
    steps:
      - read VERSION → $VER
      - GOOS/GOARCH dari matrix → build:
          go build -o ${name}-${VER}-${GOOS}-${GOARCH} ./${name}
      - generate manifest dari binary:
          ./${name}-... --dump-manifest > plugin.json
      - sha256sum > .sha256 ; cosign sign
      - upload artifact
  release:
    needs: build
    steps:
      - gh release create "${name}/v${VER}" \
          ${name}-${VER}-*-* plugin.json *.sha256 *.sig
```

### 20.3 Konvensi nama artifact (otomatis)

```
github-1.4.2-linux-arm64       ← <name>-<version>-<goos>-<goarch>
github-1.4.2-linux-amd64
github-1.4.2-darwin-arm64
plugin.json                    ← manifest (1 per release, generate dari binary)
github-1.4.2-linux-arm64.sha256
github-1.4.2-linux-arm64.sig
```
Nama = `nama-versi-os-arch`. Host pas install pilih asset yang cocok dengan
`os_arch` device (Termux → `linux-arm64`). Nama + versi terbaca dari nama file &
`plugin.json`, ngk perlu ditulis tangan.

### 20.4 Nyambung ke marketplace (§6.2)

`gh release create "${name}/v${VER}"` → muncul di **GitHub Releases API** →
otomatis jadi entri **Available** di marketplace wick (§6.2). Jadi: push connector
→ CI build + release → langsung nongol di marketplace. Zero langkah manual.

> Catatan: build matrix per os/arch jalan **paralel** → cepat. Cuma connector yang
> berubah yang di-rebuild (path-filter) → hemat CI.

### 20.5 Mass-rebuild & auto-bump saat KONTRAK core berubah

**Premis penting (koreksi):** ngk semua perubahan core maksa recompile semua.
Cuma **breaking** (proto_version naik) yang maksa. Additive (minor proto) =
connector lama tetap kompatibel → **nol rebuild** (lihat §19.3). Jadi mass-rebuild
= jarang & terkontrol.

**Dua pemicu rebuild:**

| Pemicu | Scope | Mekanisme |
|--------|-------|-----------|
| Folder 1 connector berubah | connector itu | path-filter (§20.2) |
| **`proto_version` naik (breaking)** | **SEMUA connector** | fan-out + auto-bump (di bawah) |

**Auto-bump — JANGAN edit VERSION satu-satu.** Pas proto breaking, otomasi:

```yaml
# .github/workflows/proto-bump.yml di repo connectors
on:
  repository_dispatch:               # dikirim dari repo proto pas rilis proto vN (major)
    types: [proto-released]
jobs:
  fanout:
    strategy: { matrix: { name: <semua folder connector> } }
    steps:
      - go get github.com/yogasw/wick-connector-proto@vN   # naik ke proto baru
      - go test ./${{ matrix.name }}                       # pastikan masih jalan
      - bump VERSION ${{ matrix.name }} (patch++)          # OTOMATIS, script kecil
      - go build ... && gh release create "${name}/v${newVER}"
      - git commit -m "chore(${name}): rebuild utk proto vN"
```
- **1 workflow handle semua connector** → zero edit manual. VERSION di-bump script
  (patch++), bukan tangan.
- **Alternatif Renovate/Dependabot:** bot auto-PR bump dep shared-proto di tiap
  connector → CI per-connector jalan (path-filter dep) → rebuild + release. Fully
  automated, ngk perlu repository_dispatch.

**Integrasi ke release workflow core (master→release):**

Workflow rilis core sekarang cek VERSION. Tambah 1 job:
```
job check-proto:
  baca proto_version di rilis ini vs rilis terakhir
    ├─ naik (breaking) → repository_dispatch {type: proto-released, vN} ke repo connectors
    │                     → trigger fan-out di atas (rebuild + auto-bump semua)
    └─ sama (additive)  → ngk ngapa-ngapain (connector lama aman, ngk usah rebuild)
```
Jadi rilis core yang breaking **otomatis mendorong rebuild semua connector**, dan
yang additive **ngk ganggu** connector sama sekali. Ngk ada langkah edit manual
di kedua kasus.

> Ringkas: edit-satu-satu cuma kejadian kalau ngk ada otomasi. Dengan fan-out +
> auto-bump VERSION (script) + repository_dispatch dari release workflow, breaking
> proto = 1 trigger → semua connector rebuild & rilis sendiri.
