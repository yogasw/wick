# Wick Image Generation — `generate_image` sebagai tool, engine-nya wick provider (design)

Status: **proposal — belum diimplement**. Butuh sign-off soal scope fase 1,
bentuk registry model image, dan jalur output (artifact vs imagecard) sebelum
ada kode yang landing.
Update terakhir: 2026-07-29.

**Paradigm:** text-to-image di wick **bukan module tools baru** dan **bukan
provider baru**. Dia satu lapis tipis di atas fondasi yang sudah ada:

- **Kredensial + registry model** → reuse `provider.WickModel`
  ([provider.go:139](../../../agents/provider/provider.go#L139)) — API key
  terenkripsi per-model, `Kind`/`APIFormat`/`BaseURL` sudah membedakan vendor.
- **Vendor call** → adapter baru sejajar `adapter_openai.go` /
  `adapter_gemini.go`, bukan menumpang `LLM` interface (request shape T2I beda
  total: tak ada history / tools / reasoning).
- **Permukaan aksi** → satu tool `generate_image`, di-expose lewat
  `externalToolDefs` ([external_tools.go:51](../../../agents/provider/wick/external_tools.go#L51))
  sehingga claude/codex/gemini dapat tool yang sama, bukan wick-only.
- **Output** → tulis file ke session workspace → `store.Artifact` (Kind
  `image`) derive otomatis read-time ([store.go:61](../../../agents/store/store.go#L61));
  `imagecard` fence dipakai untuk galeri multi-gambar.

Jadi: **source tetap wick provider yang proses, permukaannya tools.** Fleksibel
mau connect ke vendor mana pun tanpa user mengurus key dua kali.

Paired mockup: [`mockup.html`](mockup.html). Update keduanya barengan.

---

## TODO

**Fase 1 (MVP) — keputusan yang diusulkan (butuh sign-off):**

- ◻ **`ImageGen` interface terpisah, bukan perluasan `LLM`** — `LLM`
  ([llm.go:22](../../../agents/provider/wick/llm.go#L22)) bicara
  Contents/Config/Reasoning; T2I hanya butuh prompt + size + n. Interface
  sendiri, file `imagegen.go`.
- ◻ **Registry model reuse `WickModel` + flag `Modality`** — tambah field
  `Modality string` (`text` default | `image`). Model picker chat memfilter
  `!= image`; tool `generate_image` memfilter `== image`. **Tanpa tabel baru.**
- ◻ **Adapter fase 1: OpenAI + Gemini saja.** Anthropic tidak punya endpoint
  T2I → sengaja tidak ada `imagegen_anthropic.go`. `openrouter`/`other`
  ikut jalur OpenAI-compatible.
- ◻ **Tool tunggal `generate_image`**, param: `prompt` (wajib), `n`,
  `size`, `model` (opsional override). Default model dari
  `WickConfig.ImageModel`.
- ◻ **Output = file di workspace, bukan base64 di tool result.** Simpan ke
  `<session cwd>/generated/<ts>-<n>.png`, tool result balikin path relatif +
  URL. Alasan: `imagecard` fence **butuh URL langsung**, base64 tak render;
  dan `Artifact` derive dari trace read-time, jadi preview gratis.
- ◻ **Expose lewat MCP** (`internal/mcp/handlers/image.go`, pola
  [todo.go](../../../mcp/handlers/todo.go)) → satu definisi, semua provider.
- ◻ **Test button per-model image** — reuse pola probe model row: 1 gambar
  terkecil untuk validasi key + model id + base URL.
- ◻ **Guardrail biaya** — image call jauh lebih mahal dari text turn. Cap
  `n` (default max 4) + hitung di spawn-log.

**Fase berikutnya:**

- → **Fase 2 — UI manual** `internal/tools/image-gen/` tipis (form prompt →
  hasil), **memanggil** `wick.ImageGen`, bukan implement HTTP sendiri.
- → **Fase 3 — image-to-image / edit + mask** (OpenAI edits, Gemini
  image-in-image). Butuh input attachment, bukan cuma prompt.
- → **Fase 4 — provider-storage sink** — hasil ke
  [provider-storage](../../../tools/provider-storage/) (S3/lokal) supaya URL
  tahan lama, bukan cuma dalam session dir.
- → **Fase 5 — profil sub-agent `image`** — agent yang beriterasi (susun prompt →
  generate → nilai hasil → ulangi), memakai `generate_image` sebagai tangannya.
  **Nol kode baru**: satu row `agent_profiles` begitu
  [multi-agent](../multi-agent/design.md) Fase 1 mendarat.
  Detail + kenapa profil ini mendorong desain sidebar/interrupt:
  [multi-agent §Bagian F](../multi-agent/design.md).
  Urutan terkunci: **tool dulu, profil menyusul** — tanpa tool, sub-agent tak
  punya tangan.
- ⏸ **Video / audio gen** — sengaja out of scope; interface `ImageGen`
  jangan digeneralisasi jadi `MediaGen` sebelum ada use-case kedua yang nyata
  (lihat memory: simple params over options pattern).

---

## Kenapa tools, bukan module sendiri

| Pertanyaan | Jawaban |
|---|---|
| Module `internal/tools/text-to-image/`? | **Tidak** untuk fase 1. Module tools di wick = fitur UI-facing (punya `view.templ`, config card, route). T2I fase 1 dipakai **agent**, bukan manusia klik form. |
| Provider baru (`TypeImage`)? | **Tidak.** `provider.Type` mengatur "siapa yang menjalankan percakapan". Image gen bukan percakapan — dia satu tool call. |
| Kalau jadi module sendiri, apa ruginya? | Duplikat key management + model list + enkripsi yang sudah ada di `WickModel`. User harus isi API key dua kali untuk vendor yang sama. |
| Kapan module jadi wajar? | Fase 2, kalau memang mau halaman manual. Dan itu tetap **consumer** dari `wick.ImageGen` — dua permukaan, satu core. |

---

## Arsitektur

```
                    ┌──────────────────────────────────────┐
   agent (any       │  tool  generate_image                │
   provider) ──────▶│  internal/mcp/handlers/image.go      │
                    └──────────────┬───────────────────────┘
                                   │ resolve model (Modality=image)
                                   ▼
                    ┌──────────────────────────────────────┐
                    │  wick.ImageGen  (imagegen.go)        │
                    │    Generate(ctx, ImageRequest)       │
                    └───┬───────────────────────┬──────────┘
                        │                       │
             imagegen_openai.go      imagegen_gemini.go
             POST /v1/images/…       genai GenerateImages
                        │                       │
                        └───────────┬───────────┘
                                    ▼
                     tulis <cwd>/generated/*.png
                                    │
                    ┌───────────────┴───────────────┐
                    ▼                               ▼
         store.Artifact (Kind=image)      ```imagecard fence
         derive read-time, preview        galeri + carousel
```

Key insight: **dua jalur output, dua tujuan.** `Artifact` = "file ini
dihasilkan turn ini" (otomatis, tak perlu model kooperatif). `imagecard` =
"tampilkan sebagai galeri" (model menulis fence-nya sendiri, butuh URL).
Fase 1 kerjakan Artifact dulu; imagecard sebagai instruksi system-prompt.

---

## File yang disentuh

### Fase 1

| File | Perubahan |
|---|---|
| `internal/agents/provider/wick/imagegen.go` | **baru** — `ImageGen` interface, `ImageRequest`/`ImageResult`, `newImageGen(WickModel)` factory per-`Kind` |
| `internal/agents/provider/wick/imagegen_openai.go` | **baru** — `POST {base}/images/generations`; covers openai + openrouter + other |
| `internal/agents/provider/wick/imagegen_gemini.go` | **baru** — genai `GenerateImages` / model image Gemini |
| `internal/agents/provider/provider.go` | `WickModel.Modality` (`""`/`text` = chat, `image` = T2I); `WickConfig.ImageModel` (id model default) |
| `internal/mcp/handlers/image.go` | **baru** — handler `generate_image`: validasi, resolve model, simpan file, format hasil |
| `internal/mcp/handlers/tools.go` | daftarkan descriptor `generate_image` |
| `internal/tools/agents/view_*.templ` | kolom Modality di form model + dropdown "Default image model" |
| `internal/agents/system-prompt/render_formats.md` | catatan: hasil `generate_image` disajikan via `imagecard` fence |

### Model picker yang harus difilter

Setiap tempat yang menampilkan daftar model chat harus mengecualikan
`Modality == "image"`, kalau tidak model image bocor ke picker percakapan
(dan spawn-nya akan gagal). Titik-titik ini perlu diaudit sebelum implement —
`modelcatalog.go`, `switcher.go`, `discover.go` (live set), plus picker di
`internal/tools/agents/`.

---

## Kontrak tool

```jsonc
// generate_image
{
  "prompt": "a red fox in snow, cinematic",   // wajib
  "n": 1,                                      // default 1, cap 4
  "size": "1024x1024",                         // opsional, divalidasi per-vendor
  "model": "img-gpt"                           // opsional; default WickConfig.ImageModel
}
```

Tool result (teks, dibaca model):

```
Generated 2 images:
generated/20260729-101500-1.png
generated/20260729-101500-2.png

Show them to the user with an imagecard fence using the URLs above.
```

Error dibuat eksplisit supaya model tak retry buta: `no image model
configured` (arahkan ke settings), `model X is not an image model`,
`size not supported by vendor`, `content rejected by vendor` (jangan retry).

---

## Pertanyaan terbuka (butuh keputusan)

1. **Nama tool** — `generate_image` vs `image` vs `wick_generate_image`. Tool
   MCP lain pakai prefix `wick_` untuk aksi khusus wick (`wick_set_title`)
   tapi nama polos untuk yang generik (`todo`, `ask_user`). T2I generik →
   usul: **`generate_image`**.
2. **Lokasi file** — `<cwd>/generated/` (kelihatan user, ikut git kalau repo)
   vs `<SessionDir>/generated/` (bersih, tapi tak kelihatan sebagai file
   workspace). Usul: **SessionDir**, karena gambar bukan source code dan
   `Artifact.URL` sudah punya jalur serve session-scoped.
3. **`Modality` di `WickModel` vs field terpisah** — apakah satu model row
   bisa dua-duanya (Gemini punya model yang chat + image)? Kalau ya,
   `Modality` harus jadi list, bukan skalar. Perlu dicek terhadap
   model Gemini terkini sebelum kunci skema.
4. **Cost visibility** — image gen tak balikin token usage seperti text.
   Apakah spawn-log perlu kolom biaya sendiri, atau cukup hitung jumlah
   gambar?

---

## Yang sengaja ditolak

- **Menumpang `LLM.GenerateContent`** dengan part khusus — memaksa T2I ke
  bentuk percakapan bikin adapter text jadi kotor, dan streaming semantics
  tak berlaku.
- **Provider `TypeImage` baru** — provider = runner percakapan; image gen tak
  punya turn/history/tools.
- **Balikin base64 di tool result** — membanjiri context (satu PNG 1024²
  ≈ ratusan KB base64) dan `imagecard` tak bisa render.
- **Generalisasi ke `MediaGen`** sekarang — satu use-case, satu knob.
