# Design: Wick sebagai Platform, Agent sebagai Flagship — Modular by Profile

Status: TODO (design / belum implementasi)
Scope: 3 work-stream. Connector ops -> category SENGAJA di luar scope (dikerjakan
developer lain).

---

## 0. Tujuan

Meluruskan arah produk wick tanpa pivot 180 derajat: posisikan wick sebagai
**platform** dengan **wick-agent sebagai flagship app**, lalu jadikan
tools/connector benar-benar modular (dipilih saat build/install lewat *profile*),
dan hilangkan pain "update terkendala di wick CLI".

Premis ini bukan ide baru yang dipaksakan ke arsitektur — backbone-nya sudah ada
di kode (`RegisterBuiltins`/`RegisterLabSamples` + `wick init` scaffold). Design ini
merapikan dan mengekspos kemampuan itu, bukan merombak.

---

## 1. Konteks & bukti (kondisi saat ini)

Diukur langsung dari repo:

- `internal/tools/` = 6 modul, `internal/connectors/` = 13 dir (7 builtin di
  `RegisterBuiltins` + `crudcrud` sample di `RegisterLabSamples` + 4 connector runtime
  via `connectors.Register(...)` saat boot: wickmanager/workflow/notifications/
  customconnector; `custom` = paket pendukung, bukan modul tersendiri), `fe/agents/` = 10 SPA.
  (Diukur ulang dari repo per sync upstream master terbaru.) Pusat gravitasi produk sudah
  pindah ke connector; "tools sebagai wajah" sudah tidak akurat.
- `internal/tools/registry.go` dan `internal/connectors/registry.go` punya pola identik:
  `RegisterBuiltins()` (on untuk semua app) + `RegisterLabSamples()` (binary lab) +
  downstream `app.RegisterTool`/`app.RegisterConnector`. `cmd/lab/root.go` tinggal milih
  mana yang di-wire.
- `cmd/cli/init.go` (`wick init`) men-scaffold downstream app dari `template/` (bawa
  `connectors/`, `tools/`, `jobs/`, `web/`, `main.go` sendiri). Inilah jalur "bikin
  tools/connector sendiri" — sudah ada, bukan hipotetis.
- `cmd/cli/upgrade.go` (`wick upgrade`) bump CLI binary + dep `github.com/yogasw/wick`
  di go.mod. Downstream menarik wick sebagai **satu Go module utuh**.
- `cmd/cli/build.go` (`wick build`) sudah punya flag `--headless` -> sinyal bahwa
  varian build-time memang feasible.
- `scripts/install.sh` BELUM punya pemilihan module (cuma "Component status").

Akar pain "update terkendala": downstream depend ke satu module monolitik;
`RegisterBuiltins()` compile-in semua connector; update = bump seluruh monolith,
tidak bisa ambil subset, tidak ada "lite" beneran.

---

## 2. North Star

- **wick = platform**: registry + `wick init` scaffold + MCP surface. Ini moat-nya.
- **wick-agent = flagship**: experience default `wick init`, home screen produk.
- **tools/connector = kapabilitas modular**: dipilih saat **build/install** lewat profile.
- **"custom tools via AI" tidak dibunuh**: direposisi jadi jalur downstream-app
  (`wick init`), bukan dihapus.

Ini memuaskan dua arah yang sempat berseberangan: agent jadi wajah (visi Yoga) +
modularitas/fokus dengan platform di bawahnya (visi Gung).

---

## 3. Work-stream #1 — Decouple upgrade core dari scaffold (pain fix)

### 3.1 Masalah

Tiga sumbu update saat ini bercampur:
(a) CLI binary, (b) dep wick core di go.mod, (c) kode/customisasi app user.
`wick upgrade` menangani (a)+(b). Gap ada di (c): file hasil scaffold (`main.go`,
registration list, `wick.yml`) bisa stale atau ketimpa saat upgrade/re-init.

### 3.2 Desain

- **Entrypoint contract stabil**: downstream `main.go` cuma memanggil `app.Run()`.
  Profile aktif dibaca runtime dari config DB (lihat 4.1), jadi `main.go` TIDAK menyebut
  profile sama sekali dan tetap stabil lintas pilihan profile maupun upgrade core.

      func main() {
          app.Run() // profil = config row di DB (runtime), tidak disebut di sini
      }

- Konsekuensi: upgrade core (b) **tidak pernah** memaksa edit `main.go`. Nambah/ubah
  connector builtin = pekerjaan di balik profile, bukan di file user.
- `wick upgrade` dibuat **non-destruktif** terhadap file user: hanya bump go.mod +
  regenerate bagian yang memang ditandai generated.

### 3.3 Asumsi yang harus divalidasi (task awal di plan)

Arti persis "terkendala di wick CLI" belum 100% dikonfirmasi: apakah (i) re-tidy dep
seluruh monolith, (ii) re-scaffold yang menghapus customisasi, atau (iii) versi CLI
binary vs dep go.mod yang gampang drift. Plan WAJIB diawali investigasi singkat untuk
memastikan fix #1 menyasar penyebab nyata, bukan dugaan.

---

## 4. Work-stream #2 — Registration profiles (keystone)

Mekanisme terpilih (KEPUTUSAN 2026-06-20, lihat §9): **named function
(`profileModules`/`RegisterProfile`) yang sumber profilnya = config row di DB**, di-set
via command `<app> config profile <name>`. BUKAN build-time ldflag, BUKAN env. (Opsi C
pisah-repo tetap ditolak; lihat 4.4. Build tags untuk pengecilan binary = jalur terpisah,
lihat 4.2.)

### 4.1 Konsep

Layer "profile" di atas `RegisterBuiltins` yang sudah ada. `profileModules(profile)`
menyaring himpunan builtin per `Meta.Key`; `RegisterProfile(profile)` me-register subset
itu. Tiga profil:

- `agent` — connector minimal yang dibutuhkan agent (default: github, httprest, slack).
- `full`  — semua 7 builtin (perilaku sekarang; default).
- `lite`  — tanpa builtin (lihat 4.5: connector platform tetap on).

Profil **disimpan sebagai config row di DB** (`configs.KeyProfile`, default `full`) dan
**dibaca saat boot** lewat `configsSvc.Profile()` sebelum `RegisterProfile`. Di-set lewat
command `<app> config profile <name>` — pola sama dengan `allowed-origins` yang sudah ada,
dan jalan **tanpa boot connector** (`withConfigsService` cuma buka `configs.Service`
singkat). Karena `install.sh` mengunduh binary **prebuilt** (tidak build dari source),
seleksi build-time tidak mungkin: install.sh memilih profil dengan memanggil
`<app> config profile <name>` setelah unduh. Ini realisasi visi "install.sh milih module"
dengan **1 artifact release**, tanpa env, tanpa rebuild.

### 4.2 Build tags (opsional, untuk lite binary kecil)

Untuk `lite` yang benar-benar mengecilkan binary (bukan sekadar tidak me-register),
gunakan build tags per grup connector sehingga paket connector berat tidak ikut
ter-compile. Default (tanpa tag) = full, supaya tidak memecah perilaku existing.

### 4.3 Kenapa ini cocok

- Reuse pola registry yang sudah ada -> arsitektur baru minimal.
- Build/install-time selectable -> sesuai visi "bongkar pasang".
- Tidak ada module split -> tidak menambah friksi versioning (lihat 4.4).

### 4.4 Yang ditolak

- **Opsi B versi env (runtime flag lewat env var)**: ditolak sebagai mekanisme utama
  (Yoga: "ngak usah env"). Seleksi runtime tetap dipakai, tapi sumbernya **config row di
  DB**, bukan env — lebih eksplisit, persisten, reuse infra `configs` + command
  `<app> config`. Catatan: seleksi runtime (env maupun DB) **tidak** mengecilkan binary —
  semua connector tetap ter-compile; pengecilan fisik cuma lewat build tags (4.2).
- **Opsi C (pisah Go module/repo, ide Yoga)**: versioning independen tapi berat,
  menambah friksi go-module multi-repo, dan justru **memperparah** pain #1 jangka
  pendek. Premature — bisa ditinjau ulang setelah #1+#2 stabil.

### 4.5 Yang TIDAK digerbang profile (temuan validasi vs master — penting)

Profile hanya menyaring himpunan `RegisterBuiltins` (7 connector publik). **Empat
connector lain di-register runtime** lewat `connectors.Register(...)` saat boot (butuh
Deps seperti configsSvc/jobsSvc/runner) di `internal/pkg/api/server.go` + `server_mcp.go`,
sehingga **di luar kendali profile** dan tetap on di SEMUA profile termasuk `lite`:

- `wickmanager` (server.go ~978, server_mcp.go ~90)
- `workflow` / wfconn (server.go ~969, server_mcp.go ~141)
- `notifications` (server.go ~986, server_mcp.go ~97)
- `customconnector` (server.go ~1025)

Ini **disengaja**: keempatnya connector platform/infra (self-management, workflow engine,
notifikasi, host custom-connector) yang memang seharusnya selalu hadir. Konsekuensi:
`lite` berarti "tanpa 7 builtin", BUKAN "tanpa connector sama sekali". Kalau kelak
diinginkan profile yang juga mematikan connector platform ini, butuh perubahan terpisah
pada call-site `connectors.Register(...)` — di luar scope plan ws2.

---

## 5. Work-stream #3 — FE: home -> agent, tools jadi "Mini Tools"

- Default landing route = agent (flagship).
- UI admin tools/connector dikelompokkan di menu **"Mini Tools"** (analogi helpdesk).
- Ini **perubahan information-architecture / routing**, BUKAN rewrite. Tools tetap
  first-class secara fungsi; hanya turun di hierarki navigasi.
- Diikat ke profile (dibaca runtime dari config DB, bukan build): profil `agent`/`full`
  -> home agent + Mini Tools terisi; `lite` boleh menyembunyikan Mini Tools. FE membaca
  profil aktif lewat config/endpoint, bukan flag build-time.
- Paling kelihatan tapi paling gampang di-rollback -> dikerjakan **terakhir**.

---

## 6. Sequencing

- **Phase 1**: #1 + #2 (backbone — pain fix + profile). Saling terkait: profile
  adalah bagian dari entrypoint contract.
- **Phase 2**: #3 (FE IA revamp). Bergantung pada profile (#2) untuk menentukan
  home & visibilitas Mini Tools.

Tiap phase jadi file plan tersendiri di `internal/planning/todo/modular-platform/`.

---

## 7. Strategi testing

- #1: integration test memastikan `wick upgrade` tidak menyentuh `main.go` user;
  bump go.mod terverifikasi.
- #2: table-driven test `profileModules` (tiap profil → himpunan modul tepat); unit test
  `configs.Profile()` (default full + nilai ter-set); unit test `parseProfileArg`
  (validasi full|agent|lite). Build-tag variant: ditunda (4.2).
- #3: FE route test (home resolve ke agent; menu Mini Tools render sesuai profile).

---

## 8. Out of scope

- **Connector ops -> category (`connector.Cat`)**: dikerjakan developer lain. Design
  ini tidak menyentuh builder `pkg/connector` untuk grouping ops.
- Pisah connector ke repo terpisah (Opsi C #2) — ditunda.
- Perubahan model akses/tags — tidak tersentuh.

---

## 9. Keputusan & open questions

Keputusan (revisi 2026-06-20, arahan Yoga — membalik keputusan build-time sebelumnya):

- **Profile = config row di DB** (`configs.KeyProfile`, default `full`), di-set via command
  `<app> config profile <name>` (pola sama dengan `allowed-origins`), dan **dibaca saat boot**
  sebelum `RegisterProfile`. BUKAN build-time `wick build --profile`, BUKAN env var.
- Alasan: `install.sh` mengunduh binary prebuilt (tidak build), jadi seleksi harus runtime;
  command `config` jalan tanpa boot connector (`withConfigsService`) sehingga install.sh bisa
  set profil pasca-unduh. Konsekuensi: ganti profil butuh restart (registrasi saat boot) — wajar.

Open questions:

1. Nama & lokasi final API profile (`app.WithProfile` vs paket `profile` vs flag) —
   diputuskan di plan Phase 1 setelah investigasi 3.3.
2. Granularitas profile: cukup 3 (agent/full/lite) atau perlu custom profile
   per-downstream? Default mulai dari 3; custom menyusul kalau ada kebutuhan nyata.
3. Isi profile `agent`: default plan = {github, httprest, slack}. Sejak sync upstream
   terbaru, `googleworkspace` diperluas besar (gmail/calendar/meet/drive/docs/sheets/
   slides) sehingga jadi kandidat kuat untuk ikut di profile `agent`. Keputusan isi
   akhir = produk; default konservatif tetap 3 connector sampai diputuskan (lihat
   catatan di `agentConnectors()`, plan ws2 Task 2).
