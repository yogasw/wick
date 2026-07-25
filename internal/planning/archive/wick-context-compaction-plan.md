# Plan: Wick context overflow — akurasi cap + mid-loop compact + overflow fallback

**Gejala user:** set `MaxContextTokens=500000` (lalu 400000), tetap kena
`HTTP 400: maximum prompt length is 500000 but the request contains 557641 tokens`.
Flow mati, harus ulang dari awal. Mau mekanisme compact ala Claude — **AI meringkas, data penting tidak hilang**, dan **beneran nge-cap** sebelum overflow + fallback saat overflow.

---

## 0. TODO

- [x] **B1** — ✅ `estimateTokens` hitung `FunctionCall.Args` + `FunctionResponse.Response` via `partChars` ([history.go](../../agents/provider/wick/history.go)). Akar cap meleset — tool result 30k/call dulu nggak kehitung. Test `TestEstimateTokens_CountsToolPayloads`.
- [x] **B2** — ✅ `maybeCompact` dipanggil tiap iterasi tool-call loop ([engine.go](../../agents/provider/wick/engine.go)), bukan cuma awal turn.
- [x] **B3** — ✅ `tokenCalibration` di-ratchet dari `PromptTokenCount` real (smoothing α=0.3, floor 1.0, cap 3.0). `calibratedEstimate` dipakai `maybeCompact`. Test `TestCalibrateFromUsage_*`.
- [x] **B4** — ✅ `generateWithOverflowRecovery` + `isContextOverflowError`: vendor overflow → `compactAggressively` (0.7) → retry (max 2×), emit thinking note. Test `TestGenerateOverflowRecovery_*`.
- [x] **B5** — ✅ Section "Context window and long tool output" di `immutable_wick.md` (wick-only). Test guard di `default_test.go`.
- [x] Test + build ✅ — 11 test PASS, race-clean, build hijau.

### Ronde 2 (dari diskusi lanjutan — kasus yang B1-B5 belum tutup)

- [x] **C1** — ✅ Cap tool result universal di ingestion ([toolresult.go](../../agents/provider/wick/toolresult.go), `capToolResult`, cap 30k). Result gede → spill full ke `<SessionDir>/tool-out/<call_id>.txt`, context dapat head+tail + guidance "grep/head jangan baca semua". UI/store tetap dapat full. Test `TestCapToolResult_*`.
- [x] **C2** — ✅ Persist summary ke **sidecar** `<SessionDir>/compaction.json` (`{summary, covered_through}`), BUKAN conversation.jsonl (hindari race + nggak ngotori UI). `loadHistory` bawa `[marker+summary] + turn setelah cutoff`, **drop** turn sebelum cutoff (jawab kekhawatiran "masih bawa yang udah di-compact"). Marker eksplisit "everything above compacted". Summary di-pin di `trimToBudget` (nggak kebuang). Test `TestLoadHistory_SidecarSkipsCoveredTurns`, `TestCompactionSidecar_*`, `TestTrimToBudget_PinsSummaryNote`.

---

## 1. Root cause (3 lapis, terverifikasi dari kode)

### B1 — `estimateTokens` buta tool call/result ← PRIMARY
[history.go:87-100](../../agents/provider/wick/history.go#L87-L100): hanya menjumlah `len(p.Text)`.
Tapi history wick penuh `FunctionCall` (args JSON) dan **`FunctionResponse.Response`** (hasil tool — shell/connector bisa **30k char** per panggilan). Semua ini `p.Text == ""` → **tidak dihitung**.

Konsekuensi: 20 tool call × ~20k char hasil = ~400k char (~100k token) **invisible** ke estimator. User set 500k, estimator kira ~300k, real 557k → tembus. Ini alasan langsung `557641 > 500000`.

### B2 — Compaction cuma dicek 1× per turn ← PRIMARY
[engine.go:144](../../agents/provider/wick/engine.go#L144): `maybeCompact` dipanggil sekali di awal `runTurn`, sebelum append user msg. Tapi di dalam loop [engine.go:149-218](../../agents/provider/wick/engine.go#L149-L218) history **tumbuh tiap tool result** (`respParts` di-append). Satu turn dengan banyak tool call balloon jauh melewati budget **tanpa** pernah re-check → request ke vendor over.

### B3 — Estimasi chars/4 kasar
chars/4 bisa meleset ±30% tergantung konten (JSON padat, non-ASCII). Vendor kasih `PromptTokenCount` real tiap response ([adapter_*.go]) — belum dipakai untuk kalibrasi. Tanpa kalibrasi, threshold 0.8×budget bisa masih optimistik.

### B4 — Tidak ada fallback overflow ← kenapa "harus ulang dari awal"
[engine.go:156-163](../../agents/provider/wick/engine.go#L156-L163): `generate` error → `emit(errorLine)` → turn **mati**. Overflow 400 diperlakukan sama seperti bad-key. Tidak ada auto-compact-and-retry, jadi kerja user hilang.

---

## 2. Desain (mirip Claude /compact, AI-driven, no data loss)

### Fix B1 — estimator sadar tool content
`estimateTokens` juga hitung:
- `FunctionCall`: nama + `Args` (marshal JSON) length
- `FunctionResponse`: `Response` (marshal JSON) length
- `p.Text` seperti sekarang
Ini bikin cap merefleksikan ukuran request sebenarnya. Satu helper `partChars(p)`.

### Fix B2 — compaction mid-loop
Di dalam tool-call loop, setelah append tool results tiap ronde, panggil `maybeCompact` lagi sebelum `generate` berikutnya. Compaction sudah aman dipanggil berkali-kali (in-place, idempotent-ish). Ini yang mencegah balloon dalam satu turn.

### Fix B3 — kalibrasi real-token
Simpan rasio `realTokens/estimatedTokens` dari `PromptTokenCount` response terakhir; skala estimasi berikutnya. Konservatif: pakai `max(1.0, ratio)` supaya hanya menaikkan (aman), tidak menurunkan cap. Optional/secondary — B1+B2 sudah menutup kasus utama.

### Fix B4 — overflow fallback + retry ★ (jawaban "tak perlu ulang")
1. Klasifikasi error overflow di `vendorErrorMessage`/helper baru: cocokkan
   `maximum prompt length` / `context length` / `too many tokens` / `prompt is too long` / `413`.
2. Di `generate` (atau runTurn loop): saat error = overflow →
   - jalankan compaction **agresif** (fraksi lebih besar, mis. 0.7 oldest),
   - kalau masih di atas budget, ulangi sampai turун cukup / minimal history,
   - lalu **retry** `generate` sekali. Batasi retry (mis. 2×) supaya tidak infinite.
3. Kalau tetap gagal setelah retry → baru emit error (tapi sudah usaha selamatkan).

### Fix B5 — system prompt guidance
Tambah baris ke immutable/preset wick: jelaskan konteks otomatis diringkas saat penuh (jadi AI tak kaget kehilangan detail lama), dan anjuran: untuk output tool besar, minta ringкас / simpan ke file lalu baca sepotong, bukan dump 30k ke context. Ini mengurangi laju balloon.

---

## 3. Kenapa set 500000 tetap 557641 (jawaban langsung)
Karena budget 500000 dibandingkan dengan **estimasi yang tidak menghitung tool results** (B1) dan **hanya dicek sekali di awal turn** (B2). Selama tool-call loop, ratusan-ribu token hasil tool menumpuk tanpa terlihat estimator dan tanpa re-check, jadi request final (557641) jauh di atas 500000 saat akhirnya dikirim. Bukan budget-nya yang salah — mekanisme ukur + waktu cek-nya yang salah.

---

## 4. DoD
- [ ] `estimateTokens` menghitung tool args + results (unit test dengan FunctionResponse besar)
- [ ] Compaction kepicu di tengah turn saat tool results menumpuk (test loop)
- [ ] Overflow vendor → auto-compact + retry, turn selesai (bukan mati); test dengan fake LLM yang 400 sekali lalu sukses
- [ ] System prompt menyebut auto-compact
- [ ] Build hijau, race-clean

## 5. Out of scope
- Token counting exact via API count-tokens endpoint — chars/4 + kalibrasi cukup.

---

## 6. Ronde 2 — desain (C1 tool-result cap, C2 persist summary)

### C1 — Tool result tunggal kegedean ← kenapa B1-B5 belum cukup
**Gejala user:** bukan chat numpuk, tapi **satu tool result** kegedean (mis. load logs 100MB, `cat` file besar, connector balikin ratusan-ribu token sekali). Langsung tembus window.

**Kenapa B1-B5 tidak menutup:** compaction meringkas turn **oldest** ([compact.go] `compactBy` potong `history[:cut]`), tapi result raksasa ada di **tail** (baru). Compact berapa kali pun tak menyentuhnya; kalau tail sendiri > window, `generateWithOverflowRecovery` nyerah.

**Fakta infra (terverifikasi):** store SUDAH spill event besar ke `thinking/<turn>/<event>.json` dengan `Large=true` ([store.go:123](../../store/store.go#L123), threshold `DefaultTraceInlineBytes=10k`). TAPI itu jalur **display/storage**. Jalur **context AI** beda: [engine.go:229](../../agents/provider/wick/engine.go#L229) append `result` string **mentah utuh** ke `FunctionResponse` tanpa cek ukuran. Jadi conversation.jsonl ramping, tapi context AI nelen semua.

**Fix C1 (cap di ingestion, manfaatin pola spill):**
1. Sebelum append result ke `e.history`, cek ukuran. Di atas cap (mis. `toolResultMaxChars`, sejajar shell 30k) →
2. Tulis full result ke file di session dir (mis. `<SessionDir>/tool-out/<call_id>.txt`).
3. Yang masuk history = **head + tail + catatan**: `"[tool output N chars — truncated. Full output saved to <path>. Don't read it all (expensive); search it with shell: grep/head/awk on that path to pull only what you need.]"`.
4. Guidance mendorong AI **cari pakai script** (pengalaman user: Claude nolak 100MB lalu bikin script cari manual) — bukan `read_file` semua.

Cap berlaku **universal** (semua tool, bukan cuma shell). Shell 30k existing tetap; ini lapis kedua di engine ingestion yang menangkap connector/wick_execute/tool apa pun.

### C2 — Persist compaction summary lintas respawn
**Gejala:** compaction cuma di `e.history` (in-memory per-spawn), tak ditulis balik. Setelah respawn (idle-kill/restart/ganti model), `loadHistory` baca `conversation.jsonl` mentah lalu `trimToBudget` **DROP** chat terlama (bukan ringkas) — kerja ringkasan hilang, atau re-compact dari nol tiap proses.

**Fakta infra:** sisi **baca** sudah siap — `turnToContent` handle `kind:"compaction"` ([history.go:67](../../agents/provider/wick/history.go#L67)). Yang belum: sisi **tulis**.

**Fix C2:**
1. Saat `compactBy` sukses, tulis summary sbg turn `kind:"compaction"` ke `conversation.jsonl` (lewat store, atau append langsung — pilih yang tak bikin duplikasi saat replay).
2. `loadHistory`: prefer ringkasan tersimpan; `trimToBudget` jadi jaring pengaman terakhir, bukan jalur utama.
3. Hindari re-compact turn yang sudah diringkas (idempoten saat respawn).

### DoD ronde 2
- [ ] C1: result > cap tidak pernah masuk history utuh; full ada di file; note+guidance ada; test dengan result raksasa.
- [ ] C1: kasus tail-sendiri-kegedean tidak lagi bikin turn mati (di-cap dulu, jadi muat).
- [ ] C2: summary tertulis ke conversation.jsonl; respawn membaca ringkasan, bukan drop mentah; test load-after-compact.
- [ ] Build hijau, race-clean.
