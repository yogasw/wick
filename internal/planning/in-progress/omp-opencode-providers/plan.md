# Provider baru: OMP (oh-my-pi) dan opencode

Status: in-progress · branch `ai/feat/omp-opencode-providers` (dari master `f8388ea2`)
Pemilik keputusan: Yoga. Implementasi dikerjakan sub-agent per irisan, main agent review + gate + deploy.

## Tujuan

1. Wick bisa menjalankan sesi agent lewat dua CLI baru, `omp` dan `opencode`, setara
   claude/codex: jalan headless, event turn terbaca di chat, resume sesi, MCP wick
   tersambung, skill + context file terbaca.
2. Tiap **instance = satu akun**. User bisa menambah banyak instance per type
   (mis. 3 opencode, 2 omp-codex) dari halaman Providers dengan alur yang sama
   halusnya seperti menambah instance codex hari ini: add → login lewat TTY
   browser → status akun → (usage kalau tersedia) → pakai.
3. Tidak ada load-balancing antar akun di dalam CLI. Satu instance hanya kenal
   satu akun. (Failover antar instance = fase berikutnya, di luar irisan ini.)

## Fakta yang sudah diverifikasi (source 29 Sep 2026)

Clone ada di `<session>/research/oh-my-pi` (commit fc671eb) dan `<session>/research/opencode` (7945de2).

OMP (`omp`, TypeScript di Bun + native Rust, dirilis sebagai satu binary):
- Headless: `omp -p "<prompt>"`, `--mode json` = stream event JSONL
  (`packages/coding-agent/src/modes/print-mode.ts`, `printableEvent`). Event:
  `agent_start/end`, `turn_start/end`, `message_start/update(delta)/end`,
  `tool_execution_start/update/end`, `auto_compaction_*`, `auto_retry_*`,
  `todo_*`. `agent_end.isTerminal === false` = masih akan lanjut.
- Resume: `--continue`, `--resume <id|path>`; `--session-dir <dir>`; `--cwd`;
  `--append-system-prompt <text|file>`; `--yolo`/`--approval-mode yolo`;
  `--no-title`; `--max-time`; `--model <provider/id>`; `--thinking <level>`.
- Isolasi akun: `--profile <name>` (atau env `OMP_PROFILE`) mengisolasi auth,
  sesi, settings, cache di `~/.omp/profiles/<name>/agent`. `PI_CODING_AGENT_DIR`
  hanya berlaku untuk profile default.
- Login non-TUI: `omp login [<provider>]` (cetak URL, baca prompt dari stdin,
  simpan ke `agent.db` profile aktif). Callback OAuth lokal: anthropic `54545`,
  openai-codex `1455`. Satu profile yang di-login berkali-kali = multi akun
  dengan rotasi otomatis — **wick tidak boleh melakukan itu**; satu instance =
  satu profile = satu login.
- Usage: `omp usage` (5 jam / mingguan per akun). Cek flag JSON-nya di `cli/usage-cli.ts`.
- Model: `omp models` (`cli/models-cli.ts`).
- Skill/context: membaca `.claude/skills`, `.agents/skills`, `CLAUDE.md`, `AGENTS.md`,
  `~/.codex/AGENTS.md` (`docs/skills.md`, `docs/context-files.md`). Skill user-level
  provider lain harus opt-in `enabledProviders`.
- MCP: `docs/mcp-config.md`.
- Hook: ekstensi TS di `.omp/hooks/pre|post` — tidak kompatibel dengan gate
  wick hari ini → gate/hook capability OFF untuk MVP (seperti gemini).
- Memory bawaan OFF secara default — biarkan OFF; memory tetap urusan wick.
- Mode `--mode rpc` (proses hidup, `steer`/`follow_up`) = fase 2, bukan MVP.

opencode (`opencode`, TypeScript di Bun, satu binary):
- Headless: `opencode run [message..]` + `--format json` (JSONL
  `{type,timestamp,sessionID,...}`): `step_start`, `step_finish`, `text`
  (dikirim setelah part teks selesai, tanpa delta), `reasoning`, `tool_use`
  (dikirim saat tool completed/error, tidak ada event mulai), `error`.
  Lihat `packages/opencode/src/cli/cmd/run.ts` fungsi `emit`.
- Resume: `-c`, `-s <sessionID>`, `--fork`; `-m provider/model`; `--agent`;
  `-f` file; `--title`.
- Auth: `opencode auth login [-p provider] [-m method]`, `auth list`, `auth logout`;
  disimpan di `~/.local/share/opencode/auth.json` (XDG data dir). Satu kredensial
  per provider. Isolasi akun per instance = data dir per instance — cari env
  resmi di source (`packages/opencode/src/global` / `xdg`) sebelum memakai
  `XDG_DATA_HOME` mentah.
- Claude Pro/Max **dilarang** di opencode (docs providers); ChatGPT Plus/Pro resmi.
- Skill: `.claude/skills`, `~/.claude/skills`, `.agents/skills`, `.opencode/skills`.
  Rules: `AGENTS.md`, fallback `CLAUDE.md`.
- Permission: `docs/permissions.mdx` — headless wick butuh semua tool `allow`.
- MCP: `docs/mcp-servers.mdx` (config `mcp` di opencode.json; `OPENCODE_CONFIG`).
- `opencode serve` (REST + SSE, banyak sesi satu proses) = fase 2, bukan MVP.

Host: `bun` tidak terpasang, tidak dibutuhkan (binary sudah membawa Bun).
Binary claude/codex dipanggil lewat shim cgroup di `~/.local/share/wick/bin/*`
(`MemoryMax=1200M`, `agents.slice`) — omp/opencode wajib ikut pola yang sama
(`cmd/cli/memory_wrapper.go`, `internal/agents/provider/memscope`).

## Desain

### Type & instance
- `internal/agents/provider/provider.go`: `TypeOMP Type = "omp"`,
  `TypeOpencode Type = "opencode"`; tambahkan ke `SupportedTypes()` (urutan UI
  setelah gemini, sebelum wick), `isSupported`, default seed bootstrap
  (**jangan** auto-seed instance kalau binary tidak ada — ikuti perilaku gemini).
- Semua titik yang menyebut `TypeGemini`/`"gemini"` di luar paket gemini harus
  ditinjau untuk dua type baru (daftar hasil grep ada di bawah).

### Isolasi akun per instance (inti permintaan user)
- OMP: instance menyimpan nama profile (default = `wick-<instanceName>`), wick
  selalu menambahkan `--profile <p>` ke argv spawn DAN login DAN usage.
  Catalog field `OMP_PROFILE` (bisa di-override).
- opencode: instance menyimpan data dir (default
  `<wick data>/providers/opencode/<instanceName>`), dibuat saat instance disimpan,
  diinjeksi lewat env yang tepat ke spawn DAN login DAN `auth list`.
- Rename instance tidak boleh memutus akun: nama profile/dir disimpan eksplisit
  di config instance saat dibuat, bukan diturunkan ulang dari nama.
- Delete instance: tanya/beri opsi hapus folder kredensial (ikuti pola yang ada
  untuk codex kalau ada; kalau tidak ada, jangan hapus otomatis).

### Spawn (MVP = satu proses per turn, sama seperti codex exec)
- Paket baru `internal/agents/provider/omp/` dan `internal/agents/provider/opencode/`
  dengan struktur cermin `gemini/` + `codex/`: `capability_init.go`, `catalog.go`,
  `spawn.go`, `mcp_config.go`, `skilldir.go`, `hide_console_*.go`, `doc.go`, tests.
- OMP argv: `--profile <p> -p --mode json --cwd <workspace> --no-title --yolo
  [--resume <sid>] [--model ...] [--append-system-prompt <file>] <prompt via stdin>`.
- opencode argv: `run --format json [-s <sid>] [-m ...] [--agent ...] <prompt>`;
  permission allow-all + MCP wick lewat config per-spawn.
- Parser event → model event wick yang sama dengan yang dipakai codex/claude
  (teks, reasoning, tool start/end, usage token, error, selesai). Untuk opencode,
  tool yang cuma punya event selesai dirender sebagai start+end sekaligus.
- Tangkap session id dari stream untuk resume (OMP: cari di event awal /
  header sesi; opencode: field `sessionID`).
- Prompt sistem wick (aturan + blok memory) diteruskan: OMP lewat
  `--append-system-prompt <file>`; opencode lewat mekanisme yang tersedia
  (agent/instructions config) — pilih yang tidak menimpa AGENTS.md project.
- Exit code / error limit akun dikenali dan dilaporkan jelas (pesan "akun
  instance X kena limit") — dasar untuk failover fase 2.
- `SendMode` default: `queue` (seperti codex) — pesan susulan menunggu turn selesai.

### Login, akun, usage, model (halaman Providers)
- `internal/agents/provider/logintty/omp.go`: login argv
  `--profile <p> login <provider>`; provider dipilih user di UI (minimal
  `openai-codex`, `anthropic`; tampilkan peringatan policy untuk anthropic).
  Parse URL OAuth dari output (pola yang sama dengan codex). Account probe:
  baca identitas akun dari profile (lewat CLI kalau ada output JSON, bukan
  membaca sqlite langsung kecuali terpaksa).
- `logintty/opencode.go`: `auth login -p <provider>`; probe via `auth list`.
  Tampilkan catatan bahwa Claude subscription tidak didukung opencode.
- Usage: OMP → `omp --profile <p> usage` di-parse ke kartu usage yang sama
  dengan codex (cache + pace gate yang sudah ada di `usage_probe.go`).
  opencode → tidak ada; kartu menampilkan "tidak tersedia".
- Model picker: seed awal + refresh dari `omp models` / `opencode models`.

### UI/UX (fe/agents/providers)
- Dua type baru muncul di pilihan "Add provider" dengan ikon + deskripsi singkat.
- Alur add instance: nama otomatis disarankan (`omp`, `omp-2`, …), profile/dir
  otomatis terisi dan tampil read-only-with-override, lalu langsung tawarkan
  tombol **Login** (TTY browser yang sudah ada) — tanpa langkah config manual.
- Kartu instance: akun terhubung (email/plan), status binary (ketemu/versi),
  usage (OMP), badge "1 instance = 1 akun".
- Banyak instance satu type ditampilkan berkelompok dan mudah dibedakan
  (nama + akun).
- Ikuti skill repo `.claude/skills/fe-module` dan `design-system`. FE wajib
  `npx vite build` di `fe/agents/providers` sebelum `wick build`.

### Resource guard
- Shim cgroup untuk `omp` dan `opencode` sama seperti claude/codex
  (`MemoryMax` dari instance/global). Tambahkan ke daftar wrapper
  (`cmd/cli/memory_wrapper.go`, `memscope/wrapper`).

## Titik sentuh (hasil grep "gemini" di luar paket gemini)

`internal/agents/pool/factory.go`, `internal/agents/pool/pool.go`,
`internal/agents/skillsync/sync.go`, `internal/agents/workflow/setup/providers.go`,
`internal/agents/workflow/nodes/agent.go`, `internal/agents/provider/memscope/wrapper/wrapper.go`,
`internal/mcpconfig/install.go`, `internal/pkg/api/server.go`, `cmd/cli/memory.go`,
`cmd/gate/main.go`, `internal/entity/agent_profile.go`, `internal/tools/agents/providers.go`,
`internal/tools/agents/view/models.go`, `internal/tools/agents/memory_handler.go`,
`internal/tools/provider-storage/handler.go`, `fe/common/ui/src/Composer.svelte`,
`fe/agents/providers/src/lib/components/*`, `docs/guide/agents/pool.md`.

## Irisan kerja

1. **Backend spawn** (sub-agent #1): type, catalog, isolasi profile/dir, spawn +
   parser event + resume + MCP + skilldir + wiring pool/factory + shim cgroup, dengan
   unit test parser dari fixture yang disusun dari source (tanpa akun asli).
2. **Akun & UI** (sub-agent #2): logintty omp/opencode, account probe, usage OMP,
   model list, halaman Providers (add/login/kartu/multi instance), docs pool.md.
3. **Main agent**: review diff, gate unit test (skill `wick-support-tools-unit-test`
   mode CHANGED), install binary omp/opencode di `<session>/tooling/agents-bin`
   (bukan global), build + deploy ke host (skill `redeploy-and-update-wick` mode C),
   lapor ke Yoga. Login akun asli dilakukan Yoga dari UI. PR satu kali setelah Yoga OK.

## Di luar irisan ini (fase 2)
- OMP `--mode rpc` / opencode `serve` proses hidup (steer, hemat spawn).
- Failover otomatis antar instance saat akun kena limit.
- Gate/hook command untuk omp/opencode.

## Aturan kerja untuk sub-agent
- Jangan push, jangan deploy, jangan reload, jangan ubah repo lain.
- Semua file kerja di folder sesi; jangan `/tmp`.
- Test: `export GOWORK=off GOFLAGS=`; jalankan dengan `env -u DATABASE_URL`.
- Jangan tebak flag CLI: setiap flag/field yang dipakai harus dicek di clone source di
  `<session>/research/...` dan dicantumkan file:line-nya di laporan.
- Laporan akhir: daftar file:line yang diubah, apa yang belum diverifikasi live, test
  yang dijalankan + hasil.

## Irisan 4 — binary provider dikelola wick (install, update, rollback)

Permintaan Yoga (29 Sep): binary provider bisa di-install/update dari wick, disimpan di bawah
data dir wick, switch otomatis waktu nggak ada yang pakai, versi lama disimpan 1–2 buat
rollback tanpa download ulang. Untuk sekarang khusus `omp` dan `opencode`; claude/codex/gemini
tetap tidak dikelola (flag per type, bisa dinyalakan nanti).

### Sumber rilis (sudah dicek 29 Sep lewat GitHub API)
- omp: `github.com/can1357/oh-my-pi` releases, asset `omp-<os>-<arch>` (linux: `omp-linux-x64`,
  `omp-linux-arm64`, `omp-linux-musl-{x64,arm64}`), binary mentah. Tiap asset punya field
  `digest: sha256:…` di API + `SHA256SUMS.txt`. Deteksi arch/musl ikuti `omp.sh/install`
  (salinannya: `<session>/tooling/agents-bin/omp-install.sh`).
- opencode: `github.com/anomalyco/opencode` releases, asset `opencode-linux-<x64|arm64>[-baseline][-musl].tar.gz`
  (baseline = CPU tanpa AVX2), isinya binary `opencode`. Digest sha256 di API. Aturan
  pemilihan asset ikuti `opencode.ai/install` (`<session>/tooling/agents-bin/opencode-install.sh`).
  Jangan ambil asset `opencode-desktop-*`.
- Verifikasi: sha256 WAJIB cocok sebelum dipasang; setelah itu jalankan `<bin> --version` dan
  harus mengeluarkan versi rilis (omp: `omp/<ver>`, opencode: `<ver>`). Batas ukuran download
  wajar (mis. 600 MB). Hanya https ke github.com/objects.githubusercontent.com — URL disusun dari
  template per type, bukan input bebas.

### Tata letak
```
<wick data dir>/providers/bin/<type>/
  versions/<ver>/<binary>        # satu folder per versi terpasang
  current                         # nama versi aktif (file teks, atomic write) — bukan symlink ke PATH
  state.json                      # versi terpasang, sumber, sha256, waktu pasang, status job terakhir
```
- Resolusi binary: instance `omp`/`opencode` TANPA `Binary` override → pakai versi `current` yang
  dikelola wick. `Binary` override tetap menang (mode manual, tidak dikelola).
- Shim cgroup (memory wrapper) harus membungkus binary terkelola juga — REAL diarahkan ke path
  versi yang di-resolve saat spawn.

### Update tanpa memotong sesi
- Klik Update → job background: cek rilis terbaru → download ke `versions/<new>.partial` →
  verifikasi sha256 + `--version` → rename ke `versions/<new>` → set `current=<new>`.
- Karena spawn per turn (MVP), turn BARU langsung memakai versi baru; proses yang sedang
  berjalan tetap memakai file versi lama (file tidak dihapus selama masih dipakai).
- Wick melacak versi apa yang dipakai tiap proses aktif (path binary saat spawn). UI
  menampilkan: "v18.4.3 aktif · 2 sesi masih jalan di v18.4.2". Setelah proses lama selesai,
  status berubah otomatis.
- Retensi: simpan versi aktif + N versi sebelumnya (default 2, setting global). Versi di luar
  itu dihapus hanya kalau tidak dipakai proses mana pun; kalau masih dipakai, dihapus setelah
  prosesnya selesai.
- Nanti kalau ada mode proses hidup (omp rpc / opencode serve / codex app-server), switch
  terjadi saat sesi itu berakhir/idle — desain pelacakan di atas sudah menampung itu.

### Rollback
- Daftar versi terpasang per type: versi, tanggal pasang, sha256 singkat, "N sesi aktif".
- "Pakai versi ini" → set `current` (instan, tanpa download). "Hapus" → nonaktif kalau versi
  itu current atau masih dipakai.
- Update gagal (sha256 salah, `--version` gagal, download putus) → `current` tidak berubah,
  error tampil di kartu.

### UI (Providers)
- Per type terkelola: kartu/section "Binary" berisi versi aktif, versi terbaru di GitHub (cek
  manual + cache 1 jam), tombol Install (kalau belum ada) / Update, progress (download %, verify,
  switching), daftar versi + rollback + hapus, catatan sesi yang masih di versi lama.
- Form Add instance untuk omp/opencode: default "Binary: dikelola wick"; kalau belum terpasang,
  tawarkan Install langsung dari form. Field Binary path jadi opsi lanjutan (override manual).
- Semua tombol aksi admin-only (ikuti guard admin yang dipakai halaman Providers sekarang).

### Config
- `providers.managed_binaries.<type>.enabled` (default true untuk omp/opencode, tidak ada untuk
  type lain), `providers.managed_binaries.keep_versions` (default 2). Ikuti pola userconfig yang ada.
- Capability per type: interface kecil (mis. `ReleaseSource`: Latest(ctx), Asset(os, arch, cpu),
  VerifyVersion(bin)) supaya type lain bisa ditambahkan tanpa mengubah alur update.

### Pemisahan binary terkelola vs binary sistem (konfirmasi Yoga 29 Sep)
- Binary yang diunduh wick HANYA hidup di `<data dir>/providers/bin/<type>/`. Wick tidak pernah
  menulis ke PATH (`/usr/local/bin`, `~/.local/bin`, dll) dan tidak menyentuh binary yang dipasang
  manual/sistem.
- Asset dipilih otomatis dari host: `runtime.GOOS`/`GOARCH` (x64/arm64), libc (glibc vs musl), dan
  untuk opencode dukungan AVX2 (tanpa AVX2 → varian `-baseline`). Hasil deteksi ditampilkan di UI
  ("linux-x64 · glibc · AVX2") dan dicatat di state.json per versi. Kombinasi yang tidak ada
  asset-nya → pesan jelas, bukan download asal.
- Instance memilih sumber binary: "Dikelola wick" (default untuk omp/opencode) atau "Path manual"
  (field Binary). UI kartu selalu menyebut sumber mana yang sedang dipakai + path yang di-resolve.

### Uji: wick benar-benar menjalankan CLI untuk cek versi (permintaan Yoga 29 Sep)
- Probe versi: setelah install/update dan setiap Re-check, wick sendiri mengeksekusi
  `<binary terkelola> --version` (timeout pendek, env bersih, lewat resolver yang sama dengan
  spawn) dan menampilkan outputnya di kartu. Versi hasil probe harus sama dengan versi rilis yang
  diunduh; kalau beda → status merah, `current` tidak dipindah.
- Test integrasi (di-gate env, mis. `WICK_E2E_PROVIDER_BIN=1`, tidak jalan di unit gate biasa):
  unduh rilis terbaru omp + opencode lewat installer wick ke dir sementara di folder sesi,
  verifikasi sha256, jalankan `--version`, cek parse versi, lalu uji update ke versi yang sama =
  no-op dan rollback ke versi sebelumnya tanpa download ulang.
- Setelah deploy, main agent menjalankan alur nyata di host (Install omp → cek versi via API/UI
  → Update → Rollback) dan melaporkan output `--version` yang terbaca oleh wick.

### Kontrak versi per provider + keamanan binary (permintaan Yoga 29 Sep)
- Tiap type punya pengetahuan versi sendiri di paket provider-nya (bukan di UI): argv cek versi
  (`--version`), parser output → semver (omp: `omp/18.4.3`, opencode: `1.18.33`, claude/codex/gemini
  mengikuti probe yang sudah ada), dan pembanding dengan tag rilis (`v18.4.3` ↔ `18.4.3`).
  Probe binary yang sudah ada (`provider.Probe`) memakai kontrak yang sama supaya satu sumber.
- Urutan aman saat install/update, tidak boleh ditukar:
  1. download ke `.partial` (https ke host GitHub saja, batas ukuran),
  2. sha256 dicocokkan dengan digest dari GitHub API (omp juga cross-check `SHA256SUMS.txt`),
     GAGAL → file dihapus, TIDAK pernah dieksekusi,
  3. baru setelah lolos: jalankan `--version` di lingkungan terbatas — timeout ±15s, env kosong
     (tanpa DATABASE_URL, token, HOME asli; HOME diarahkan ke dir sementara), cwd dir sementara,
     dibungkus cgroup memori seperti spawn agent,
  4. versi hasil parse harus sama dengan tag rilis yang diminta, GAGAL → hapus, `current` tetap,
  5. baru rename ke `versions/<ver>` dan pindahkan `current`.
- state.json menyimpan sha256 per versi; setiap rollback/aktivasi ulang menghitung ulang sha256
  file di disk dan menolak kalau berubah (deteksi binary yang diutak-atik setelah dipasang).

### Manual, bukan otomatis + bisa diperluas ke provider lain (Yoga 29 Sep)
- Wick TIDAK pernah mengunduh atau meng-update sendiri. Download hanya terjadi saat admin klik
  Install/Update (atau pilih versi tertentu dari daftar rilis). Cek rilis terbaru cuma memberi
  tanda "update tersedia".
- Kalau binary belum ada (tidak terkelola & tidak ketemu di PATH), kartu instance dan form Add
  menampilkan status "binary belum terpasang" dengan dua jalan: tombol "Download dari GitHub"
  (managed) atau isi Path manual.
- Bisa memilih versi spesifik (bukan cuma latest) dari daftar rilis — berguna untuk pin/rollback
  ke versi yang belum pernah diunduh.
- Registry sumber binary: satu interface (`ReleaseSource`: daftar rilis, pilih asset per
  os/arch/libc/cpu, digest, versi dari output `--version`) + `Register(type, source)`. Menambah
  provider lain (claude, codex, gemini, dll) = satu file implementasi + register, tanpa mengubah
  alur install/update/rollback atau UI. Dokumentasikan langkah menambah provider di
  docs/guide/agents/providers.md.

## Status & sisa pekerjaan sampai selesai (update 30 Sep 2026)

Aturan tetap: deploy dulu dari branch `ai/feat/omp-opencode-providers` untuk dicek Yoga; commit lokal bertahap boleh setelah gate hijau (izin Yoga 30 Sep); push + SATU PR ke master hanya setelah Yoga OK.

### Sudah terdeploy
- 0.1.379–0.1.384: irisan 1–4 (type omp/opencode, isolasi akun & MCP, binary terkelola + UX progress/versi, fix URL login & bus env, resolve model + error spawn tampil, live models + filter `a|b !x`, ikon opencode).
- 0.1.385 (commit ab1dc086, ae1c8365, c2fa112d, 79ac54ac): mode server opencode (`opencode serve` per instance, default ON, OFF = `opencode run` per turn tetap ada), idle reaper wajib (default 10 menit), stop = abort request, toggle "Load Claude/Codex skills" (default OFF), live models + hosted default ON, ikon omp.
- Hasil ukur (research/oc-perf): per turn `run` ~6 s / 550–790 MB; server warm 1,5–1,7 s, satu proses ~520 MB. `BUN_ARGUMENTS`/`NODE_OPTIONS`/`BUN_OPTIONS=--smol` tak berefek; `BUN_JSC_forceRAMSize` −20% RSS tapi CPU 2–3,5×.

### Irisan 5 — auth & akun (sedang, sub-agent bc1b18ad)
Brief: `files/research/oc-perf/BRIEF-auth-multiaccount.md`.
1. Detail provider: Connection/auth paling atas & terbuka; SEMUA section lain collapsed default dengan ringkasan di header; satu komponen collapsible; state di localStorage.
2. omp multi-akun native (`<profile>/agent/agent.db` tabel `auth_credentials`, rotasi & pin oleh omp): daftar akun + status/usage, Add account, logout.
3. Fix "Token expires 1/1/1" (zero time) + mapping Plan/Organization Codex.
4. Login API key (omp & opencode), disimpan terenkripsi di env instance, nama env per provider dari katalog CLI.
5. Daftar OAuth dinamis (omp registry; opencode ChatGPT + Copilot), yang belum dites = "beta".
6. opencode multi-provider per instance (`auth.json` = satu slot per provider): daftar, tambah, hapus via `opencode auth logout`.
7. Komponen daftar akun generik (dipakai omp & opencode).

### Irisan 6 — model akun: Instance → Provider → (Akun) → Model (keputusan Yoga "ikut wick")
- Akun = satu login ke satu provider. Satu tombol "Add account" (pilih provider → OAuth/API key).
- omp: semua akun native di satu profile. opencode: wick membuat folder data (XDG) baru otomatis untuk akun kedua pada provider yang sama; provider berbeda boleh satu folder.
- Picker (pakai grouping + live-set yang sudah ada di ProviderPicker): type → instance → provider → [Auto | akun A | akun B — hanya jika provider itu punya >1 akun] → model. Nilai pin memakai format live-set wick `<akun|default>@<model>` (aman untuk id model ber-`/`).
- Rotasi: omp native; opencode dikerjakan wick (usage limit/error kuota → akun berikutnya pada provider yang sama). Server mode opencode: satu server per akun.
- Pin akun omp lewat `/session pin <n|email>` di mode RPC (lihat irisan 7); kalau tak bisa, picker hanya menampilkan akun yang melayani.

#### BE dibuat generik (Yoga 30 Sep: "cuma butuh diperluas di BE, biar provider lain gampang di-expand")
FE sudah generik: `ComposerModelOption.live` = baris set yang bisa di-drill, `loadModels(value, {entry})` = level 4, pin `<entry>@<model>` (fe/common/ui/src/composer-types.ts, ProviderPicker). Yang masih khusus wick ada di BE:
- `providerOptionModelsJSON` (internal/tools/agents/handler.go ~2583): `?entry=` hanya untuk `TypeWick` → `expandLiveWickSet`.
- Resolusi pin set saat spawn: `resolveLiveSetFallback` (internal/agents/provider/wick/spawn.go:258).
Rencana: satu kontrak di paket provider, registri per type (pola sama dengan registri managedbin):
```go
// ModelSets exposes a provider type's grouped picker levels.
type ModelSets interface {
    Sets(ctx, ins) ([]ModelChoice, error)            // baris live=true (wick: live set; omp/opencode: provider, atau "provider · akun" bila >1 akun)
    Expand(ctx, ins, entry string) ([]ModelChoice, error)
    Resolve(ins, entry, model string) (SpawnPin, error) // akun/model yang dipakai spawn
}
func RegisterModelSets(t Type, s ModelSets)
```
- Handler & spawn memanggil registri, bukan `if TypeWick`. wick direfaktor jadi implementasi pertama (perilaku sama, test lama tetap hijau), lalu omp & opencode.
- **Generik penuh, kedalaman bebas (Yoga 30 Sep: "buat reusable/generic, case kayak ini bakal banyak")** — bukan 1 level set:
  - BE: `Expand(ctx, ins, path []string)`; tiap `ModelChoice` bisa `live` (punya anak) di level mana pun. Contoh path: `["codex"]` → akun; `["codex","akunA"]` → model. Satu provider satu akun → level akun dilewati (implementasi tak mengembalikan level itu).
  - API: `?entry=` menerima path ber-encode (mis. `codex/akunA`, tiap segmen di-escape); respons sama bentuknya di semua level.
  - FE ProviderPicker: ganti `setDrill` tunggal dengan TUMPUKAN drill (breadcrumb + back), cache per path; `loadModels(value, {entry: path})`. Label terpilih = rangkaian segmen ("opencode · yoga · Codex · akun A · gpt-5.x").
  - Pin: `<path>@<model>` (path ber-escape, model sesudah `@` terakhir yang tidak ter-escape) — pin wick lama `<entry>@<model>` = path 1 segmen, tetap valid (kompatibel mundur, ada test).
  - Satu komponen/tipe tree yang sama dipakai juga di tempat lain yang butuh pilihan bertingkat (project default model, preset, schedule).
- Provider baru cukup: implement `ModelSets` + `RegisterModelSets`, tanpa sentuh handler/FE.

### Irisan 7 — mode server omp
- `omp --mode rpc --no-ui` persisten, toggle per instance default ON, OFF = `-p` per turn tetap. Idle kill wajib, stop = abort. Ukur satu turn omp dulu dengan akun asli.

### Irisan 8 — terminal web (gotty)
- gotty (sorenisanerd/gotty, MIT) sebagai binary terkelola (managedbin), dijalankan per permintaan di 127.0.0.1 port & kredensial acak, `--once --permit-write`, env instance; dibuka di modal wick lewat reverse proxy wick (auth wick, admin saja, diaudit). Kill saat tab tutup / idle. Isi: `omp --profile <p>`, `opencode`, `omp login`, `omp usage`, `opencode auth login`.

### Penutup
- Hapus env instance tak berguna di host Yoga (`BUN_ARGUMENTS`, `NODE_OPTIONS`) — manual oleh Yoga.
- Revoke key Zen testing; hapus entri `opencode` di auth.json instance bila sudah di-revoke.
- Setelah semua irisan OK di host: squash-free push branch + SATU PR ke master, pindahkan plan ke `done/`.

### Tambahan — model yang terdaftar tapi tak bisa dipakai akun (Yoga 30 Sep, sesi e1e6a2a1 & fbccd99b)
- omp/yoga (akun ChatGPT plan free): default = model pertama `omp models` = `openai-codex/gpt-5.5` → Codex `model_not_found`. `omp models --json` tak punya flag akses per model (hanya id/context/cost/thinking; `accountAccess` cuma program cyber & multi-akun). Jadi daftar ≠ jaminan bisa dipakai.
- Rencana: (1) error `model_not_found`/no-access menandai model itu "tak tersedia untuk akun ini" per instance/akun (cache, reset lewat refresh) → abu-abu di picker, tak jadi default; (2) default = model terakhir yang terbukti jalan di instance, bukan urutan pertama; (3) pesan error jelas + plan akun (omp sudah memberi plan_type).
- Crash-loop 4× karena error model sudah diperbaiki di 63408bcb (error model = error turn biasa).
