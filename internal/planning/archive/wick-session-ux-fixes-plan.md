# Plan: Wick session UX fixes — baca upload, indikator progress, curl retry

Dari sesi live user (screenshot): (M1) `read_file` gagal baca file yang di-upload user — "escapes the session workspace"; (M2/M4) setelah tool call, sesi keliatan **stuck** tanpa indikator apakah lagi jalan/nunggu; (M3) tombol **Copy-as-curl** di Model interactions belum ada (backend sudah ada, FE belum). Root cause tiap item sudah diverifikasi dari kode.

---

## 0. TODO

- [ ] **M1** — `read_file`/`edit_file` boleh baca **session uploads dir** (`<SessionDir>/uploads`), bukan cuma workspace. Sekarang sistem menyuruh AI baca `uploads/...` (via `[Attached files]`) tapi `fsResolvePath` menolaknya.
- [ ] **M2** — **Transparansi saat progress** (bukan ubah timeout — user pilih default 10m tetap). Masalah: bukan stuck, tapi shell command block 10m sampai timeout DAN tidak ada baris/indikator "lagi ngerjain apa" selama itu → user tak tahu apa yang jalan. Fix: Model interactions tampilkan baris **in-progress** (record SEBELUM call, update setelah) dgn loading + tool/tahap + elapsed.
- [ ] **M3** — FE tombol Copy-as-curl di `WickInteractions.svelte` (backend `BuildCurl` + route `/{seq}/curl` sudah jalan; FE belum manggil).
- [ ] Test + build.

**Keputusan user:** default shell timeout **tetap 10m**. Yang salah bukan durasinya — tapi selama 10m itu UI diem tanpa info AI lagi ngerjain apa. Perbaikan = visibility, bukan timeout.

---

## 1. Root cause (terverifikasi)

### M1 — read_file tolak file upload ← BUG
- Upload disimpan di `<SessionDir>/uploads/<hash>.ext` ([uploads.go:31](../../tools/agents/uploads.go#L31)).
- Workspace wick = `<SessionDir>/../projects/<pid>/files` (beda dir; lihat cwd di screenshot).
- Pool menyisipkan `[Attached files]` + **AbsPath ke uploads/** ke message text ([pool.go:37-39](../../agents/pool/pool.go#L37)) — jadi AI **disuruh** baca path itu.
- Tapi `fsResolvePath` ([tool_fs.go:36-57](../../agents/provider/wick/tool_fs.go#L36-L57)) menolak apa pun di luar `Workspace` → `read_file(uploads/...)` = "escapes the session workspace". Sistem menyuruh baca path yang tool-nya sendiri tolak.

### M2/M4 — tidak ada indikator saat sibuk ← UX gap (bikin "stuck")
- `generate` memang emit `heartbeatLine` tiap 15s ([engine.go]), TAPI parser **drop** system subtype selain `init` ([claude.go:141-160](../../agents/event/claude.go#L141-L160)) → heartbeat tidak pernah sampai UI.
- Saat tool jalan (mis. shell 4s–menit) atau vendor call lambat (74s di screenshot interaction #1), UI diam total → user tidak tahu jalan atau mati. Screenshot "running command..." = memang masih jalan, hanya tak ada sinyal.
- Butuh: event yang PARSER teruskan → UI render "running/thinking".

### M3 — curl belum ada di UI ← FE gap (bukan backend)
- Backend lengkap: `wick.BuildCurl` ([curl.go:26](../../agents/provider/wick/curl.go#L26)) + route `GET /providers/wick/interactions/{session}/{seq}/curl` ([handler.go:343](../../tools/agents/handler.go#L343)) balikin `{"curl": ...}`.
- `WickInteractions.svelte` hanya load+list interactions — **tidak ada** tombol/aksi curl, `api.ts` tak punya fungsi curl. Itu sebabnya "pernah minta tapi tak kelar": sisi server beres, sisi UI belum disambung.

---

## 2. Fix

### M1 — izinkan read dari uploads
`fsResolvePath` terima daftar root yang diizinkan, bukan cuma workspace. `read_file` (dan mungkin `edit_file`) confine ke `{workspace, sessionUploadsDir}`. `write_file` tetap workspace-only (menulis ke uploads tak masuk akal). `toolContext` sudah punya `SessionDir` → uploads = `filepath.Join(SessionDir, "uploads")`.
- Guard tetap: hanya dua root itu; tak ada path traversal (`..`) yang lolos (Clean + prefix check per root).
- Read-only terhadap uploads: jangan izinkan write/edit ke sana (hindari AI menimpa blob user).

### M2/M4 — indikator progress yang sampai UI
Opsi (pilih yang paling ringan & tidak bikin turn palsu):
- Emit event yang parser TERUSKAN sebagai sinyal "busy": mis. saat mulai tool call, `tool_use` sudah ke-emit (UI bisa tandai in-progress sampai `tool_result`). Untuk vendor call lambat sebelum tool pertama, butuh sinyal terpisah.
- Paling bersih: tambah subtype system yang parser kenali → AgentEvent bertipe "status/progress" yang FE render sebagai spinner "thinking…/running…", lalu hilang saat text/tool/done. Tidak masuk history, tidak jadi turn.
- FE: tampilkan indikator selama state = working/responding tanpa event baru selama >Ns.

### M3 — sambungkan curl ke FE
- `api.ts`: `apiGetWickInteractionCurl(base, session, seq)` → GET route di atas.
- `WickInteractions.svelte`: tiap baris interaksi punya tombol "Copy as curl" → fetch → copy ke clipboard (atau tampilkan modal/textarea). Handle 422 (interaction lama tanpa model_id / model sudah dihapus) dengan pesan yang jelas.

---

## 3. DoD
- [ ] M1: `read_file` pada AbsPath upload berhasil; write/edit ke uploads tetap ditolak; traversal tetap ditolak. Test.
- [ ] M2/M4: saat tool/generate berlangsung, UI menunjukkan indikator; hilang saat selesai.
- [ ] M3: tombol Copy-as-curl muncul per interaksi, meng-copy curl yang benar; error 422 tampil rapi.
- [ ] Build hijau, race-clean.

## 4. Out of scope
- Mengubah tempat penyimpanan upload (biarkan di `uploads/`).
- Streaming token-by-token (indikator busy cukup untuk masalah "stuck").
