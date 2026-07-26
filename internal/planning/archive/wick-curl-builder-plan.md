# Plan: Wick "Copy as curl" → curl builder

Copy-as-curl sudah ada tapi outputnya multi-line (`\` continuation) yang **kepotong saat di-paste ke Postman**. User mau UI builder: pilih format, atur bearer token (placeholder / custom / reveal asli), body editable, env ditampilkan, copy per-bagian.

---

## 0. TODO

- [x] **C-B1** ✅ `BuildRequest` → `CurlRequest{Method,URL,Headers,AuthHeader,AuthScheme,Body,EnvHint}` ([curl.go](../../agents/provider/wick/curl.go)). `BuildCurl` jadi wrapper (backward-comp, 8 test lama tetap hijau).
- [x] **C-B2** ✅ 4 renderer: `RenderSingleLine` (Postman-safe, no `\`), `RenderMultiline` (bash), `RenderRawHTTP` (Postman import), `RenderJSONBody`. Test: `TestRenderSingleLine_NoContinuation` (bukti fix truncation), `TestRenderRawHTTP_Shape`, dll.
- [x] **C-B3** ✅ Bearer di-inject saat render (placeholder/custom/real), struct nggak bawa secret. Gemini query-param auth handled (`TestGeminiQueryParamAuth`).
- [x] **C-B4** ✅ `GET /providers/wick/models/{id}/key` (admin-only) decrypt via `globalConfigs.DecryptSecret`. Endpoint curl return semua format + env (key=placeholder) sekaligus.
- [x] **C-F1** ✅ `CurlBuilderModal.svelte`: format toggle, bearer (placeholder/custom + Get-real-key), editable textarea (reset), env block, copy per-bagian + copy-all. Wired di `WickInteractions` (tombol "Copy request"). FE build + TS check 0 error.
- [x] Test + build ✅ — 13 curl test PASS, Go+FE build hijau.

---

## 1. Root cause truncation
`renderCurl` ([curl.go:141](../../agents/provider/wick/curl.go#L141)) pakai `\`+newline continuation + single-quote multiline body. Postman "Import → Raw text" tak selalu handle bash line-continuation → baris ke-2+ ke-drop → request kepotong. Fix: sediakan **single-line** (no `\`) + **raw HTTP** (yang Postman import paling mulus).

## 2. Desain

### Backend — structured curl
Refactor `BuildCurl` → `BuildRequest(rec, model) → CurlRequest{Method, URL, Headers map, Body json, EnvHint}` (vendor-shape logic tetap: openai/anthropic/gemini). Lalu renderer:
- `renderSingleLine(req, bearer)` — 1 baris, Postman-safe.
- `renderMultiline(req, bearer)` — `\` bash (format lama).
- `renderRawHTTP(req, bearer)` — `POST /path HTTP/1.1` + headers + blank + body.
- `renderJSONBody(req)` — body doang, pretty.
Bearer value di-inject saat render (placeholder / custom / real), tidak disimpan di struktur.

### Backend — reveal token (admin)
New endpoint `GET /providers/wick/models/{id}/key` (admin-only) → decrypt `WickModel.APIKey` via `secretDecryptor` → `{key: "<plaintext>"}`. User bilang: admin yang input key, jadi reveal OK (dia sendiri). Tetap admin-gated + tidak pernah masuk log.

### FE — curl builder modal
Ganti tombol "Copy as curl" → buka modal:
- **Format** toggle: Single-line · Bash · Raw HTTP · JSON body.
- **Bearer**: radio placeholder / custom-input / [Get real] (fetch reveal endpoint, isi input, warning kecil).
- **Body**: `<textarea>` editable (default dari format); copy pakai isi textarea (edit menang).
- **Env**: block env hint (base_url, model) ditampilkan; format single-line/bash boleh reference `$WICK_MODEL_API_KEY`.
- **Copy**: per-bagian (URL / headers / body) + Copy full. Toast konfirmasi.
Reuse: bukan DetailModal (butuh interaksi) — komponen `CurlBuilderModal.svelte` sendiri.

## 3. DoD
- [ ] Single-line curl paste ke Postman utuh (tak kepotong).
- [ ] Raw HTTP format valid buat Postman Import.
- [ ] Bearer: placeholder default; custom isi; Get reveal token asli (admin) → masuk output.
- [ ] Body editable; copy ambil hasil edit.
- [ ] Env ditampilkan; copy per-bagian jalan.
- [ ] Build hijau, test backend renderer + reveal endpoint.

## 4. Out of scope
- Menjalankan request dari UI (cuma copy).
- Menyimpan token custom (sesi saja, tidak persist).
