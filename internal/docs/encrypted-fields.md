# Encrypted Fields — Desain

Status: proposal / in-design.
Update terakhir: 2026-05-03.

---

## 1. Masalah

Connector kadang perlu menyertakan credential atau nilai sensitif sebagai bagian
dari output ke LLM — supaya LLM bisa pass nilai itu ke operasi berikutnya.
Kalau dikirim plain-text:

- Nilai muncul di context window LLM (model provider lihat isinya).
- Nilai muncul di `connector_runs.response_json` (audit log).
- Kalau LLM cache response, value bisa durable lebih lama dari intended.

Tujuan: **LLM tetap bisa membawa credential antar tool call, tapi tidak pernah
tahu isinya. Connector code tidak perlu berubah.**

---

## 2. Prinsip desain

1. **Dua jalur encrypt.** Middleware auto-mask nilai yang ada di `Configs`
   bertag `encrypt`/`secret` — connector tidak perlu tahu. Untuk data sensitif
   dinamis di response yang tidak ada di Configs (email, data DB, response API
   eksternal), connector panggil `enc.MaskSensitive(data, values)` sebelum return.
2. **Auto encrypt pada output.** Setelah `ExecuteFunc` return, response
   di-marshal ke string lalu `enc.MaskSensitive` replace semua nilai yang
   match field bertag `encrypt`/`secret` dengan `wick_enc_<ciphertext>`
   sebelum dikirim ke LLM.
3. **Auto decrypt pada input.** Sebelum `ExecuteFunc` dipanggil, wick scan
   params dari LLM — string dengan prefix `wick_enc_` otomatis di-decrypt ke
   plaintext. Connector menerima plaintext biasa.
4. **User bisa pilih sendiri field mana yang di-encrypt.** Selain auto dari
   struct tag, user bisa search value tertentu dari sumber tertentu dan
   encrypt manual lewat UI.
5. **Encrypt/decrypt UI terpisah dari LLM.** MCP tool `wick_encrypt` dan
   `wick_decrypt` hanya return URL ke Wick UI — LLM tidak pernah lihat
   hasilnya. User harus login ke UI untuk proses.
6. **Per-user key.** Key di-derive dari master key + user UUID — `wick_enc_`
   token dari user A tidak bisa di-decrypt oleh user B.
7. **Selalu aktif.** Master key di-bootstrap otomatis ke DB pada first boot
   (pola yang sama dengan `session_secret`). Untuk disable, set
   `WICK_ENC_DISABLE=true` secara eksplisit.

---

## 3. Mekanisme

### 3.1 Format

```
wick_enc_<base64url(nonce‖AES-256-GCM(plaintext, nonce, derived_key))>
```

- Prefix `wick_enc_` — distinct, tidak overlap format wick lain atau format
  eksternal umum.
- Nonce 12 bytes random di-embed di depan ciphertext.
- `derived_key = HKDF(master_key, salt=user_uuid, info="wick-enc")` —
  per-user, 32 bytes. `info` parameter memastikan derived key tidak overlap
  dengan penggunaan HKDF lain di masa depan.

Karena key di-derive dari user UUID, `wick_enc_` token hanya valid untuk user
yang sama. SSO/OAuth dan PAT keduanya resolve ke `user_uuid` lewat middleware
auth yang sudah ada.

### 3.2 Auto-encrypt field: marking via struct tag

Developer mark field sensitif di Configs atau Input struct:

```go
type MyConfigs struct {
    Host     string `wick:"host,required"`
    Username string `wick:"username,required"`
    Password string `wick:"password,encrypt,required"` // auto-encrypt di output
}
```

Tag `encrypt` (baru) atau `secret` (sudah ada) — treated sama untuk tujuan
ini. Wick baca tag ini saat middleware setup.

### 3.3 Output path (encrypt)

```
ExecuteFunc return → marshal ke JSON string
        ↓
Middleware kumpulkan sensitive values dari Configs row aktif
(hanya field bertag encrypt/secret — nilai yang disimpan di DB)
        ↓
enc.MaskSensitive(data string, values []string) string
        ↓
String hasil dikirim ke LLM (sudah ter-mask)
```

```go
func MaskSensitive(data string, values []string) string {
    result := data
    for _, plain := range values {
        if len(plain) < 8 {
            continue // skip nilai pendek, mitigasi false-positive
        }
        result = strings.ReplaceAll(result, plain, encrypt(plain))
    }
    return result
}
```

Data sensitif dinamis (email, data DB, response API eksternal) tidak ter-cover
di jalur ini — connector panggil `enc.MaskSensitive` sendiri sebelum return.

**Encrypt cache** di-scope per-request: nilai identik dalam satu response
dapat token yang sama, sehingga LLM tidak mengira dua kemunculan credential
yang sama adalah dua credential berbeda. Cache di-destroy setelah response
dikirim.

**Package:** `internal/enc/` — dua fungsi utama yang di-export:
- `MaskSensitive(data string, values []string) string` — encrypt, dipakai middleware + connector
- `UnmaskSensitive(data string) (string, error)` — decrypt, dipakai middleware input path

### 3.4 Input path (decrypt)

```
LLM kirim params (JSON atau plain text)
        ↓
enc.UnmaskSensitive(data string) (string, error)
  Scan semua wick_enc_ token → decrypt ke plaintext
  (pakai derived_key user saat ini; gagal decrypt → error 400)
        ↓
ExecuteFunc menerima params plaintext
```

Mekanisme ini juga berlaku untuk **retry dari history**: `connector_runs.request_json`
menyimpan `wick_enc_` token. Saat retry dieksekusi, middleware decrypt scan
berjalan seperti biasa — token di-replace ke plaintext sebelum connector
dipanggil. Caveat: kalau master key di-rotate sejak run awal, token lama akan
gagal decrypt.

### 3.5 Bootstrap key otomatis (DB-backed)

Master key di-manage lewat `configs` service — pola yang sama dengan
`session_secret`:

1. Saat `Bootstrap()`, `configs.Service` reconcile key `encryption_key` ke DB.
2. Kalau row belum ada → auto-generate 32 bytes random, simpan ke DB.
3. Kalau row sudah ada → pakai nilai dari DB.
4. **Override dari environment:** `wick.EncryptionKey()` cek `WICK_ENC_KEY`
   env var lebih dulu; kalau di-set (mis. via vault injection), nilai DB
   diabaikan.

```go
func (s *Service) EncryptionKey() string {
    if v := os.Getenv("WICK_ENC_KEY"); v != "" {
        return v // vault / OS env override
    }
    return s.Get(KeyEncryptionKey)
}
```

Pola ini solve beberapa concern sekaligus:
- **Container restart** → key persist di DB, tidak regenerate.
- **Production / vault** → inject via env var, DB value tidak terpakai.
- **Dev / first boot** → auto-generate ke DB, tidak perlu setup manual.
- **Tidak ada file `.env` dependency** — tidak ada risiko key ter-commit ke git.

Fitur **selalu aktif**. Untuk disable, operator set `WICK_ENC_DISABLE=true`
di environment — tidak ada cara disable tanpa eksplisit opt-out.

---

## 4. Manual encrypt/decrypt: internal tool (UI)

Bukan lewat LLM — harus login ke Wick UI. Dibangun sebagai modul baru di
`internal/tools/encfields/` (atau serupa), bukan di MCP surface.

### 4.1 Encrypt tool

User bisa search value tertentu dari sumber tertentu dan encrypt manual.

**Input:**
- `value` — plaintext yang mau di-encrypt.
- `source` — dari mana value ini (bebas: label deskriptif, mis. "connector
  Loki Prod / field token").

**Output:**
- `wick_enc_<ciphertext>` ditampilkan di UI, bisa di-copy.
- Disimpan ke log per-user (opsional) dengan `source` sebagai label.

Use case: developer mau pre-generate `wick_enc_` value sebelum connector
jalan, atau debug apakah enkripsi berjalan benar.

### 4.2 Decrypt tool

User input `wick_enc_` value, UI tampilkan plaintext — **hanya untuk user yang
sama yang mengenkripsi** (karena key per-user).

- Butuh login, sesi aktif.
- Admin tidak bisa decrypt `wick_enc_` milik user lain (key berbeda — hasil
  decrypt akan error).
- Tidak ada API endpoint publik — hanya via web UI form.

---

## 5. MCP tools: redirect ke UI (bukan execute)

`wick_encrypt` dan `wick_decrypt` tetap ada di meta-tool surface tapi
**tidak menjalankan enkripsi/dekripsi langsung** — hanya return link ke UI.

```json
// wick_encrypt response
{
  "message": "Encryption must be done via the Wick UI.",
  "url": "https://<wick-host>/tools/encrypt"
}

// wick_decrypt response
{
  "message": "Decryption must be done via the Wick UI.",
  "url": "https://<wick-host>/tools/decrypt"
}
```

LLM tidak pernah melihat plaintext atau ciphertext hasil proses. User harus
buka URL, login, lalu proses manual. Ini intentional — memastikan decryption
tidak bisa di-trigger dari context LLM.

---

## 6. Context: CLI vs MCP HTTP

Cara AI bisa bertindak ketika credential ter-expose berbeda tergantung dari mana
Wick diakses.

### 6.1 CLI (Claude Code atau shell langsung)

AI punya akses langsung ke filesystem lokal di mana Wick berjalan. Kalau
credential ter-expose atau ada `wick_enc_` token yang perlu di-rotate:

- AI bisa baca, edit, dan simpan file config connector langsung di direktori.
- Tidak perlu lewat UI — update native di tempat.
- Alur tipikal: user lapor credential bocor → AI cari file connector yang relevan
  → replace value → save. Tidak ada langkah manual tambahan.

Implikasi: lewat CLI, AI bisa melakukan operasi credential update end-to-end
tanpa interaksi UI. Ini **hanya aman kalau session CLI ada akses ke direktori
project yang tepat** — jangan jalankan Claude Code CLI dengan akses root/global
kalau tidak diperlukan.

### 6.2 MCP via HTTP connector

Connector HTTP tidak punya akses ke filesystem Wick. Kalau ada credential yang
perlu di-update atau di-rotate:

- AI **tidak bisa** edit file config secara langsung.
- Satu-satunya jalur: AI instruksikan user untuk buka Wick UI → update value
  manual lewat form connector config.
- Untuk pre-generate `wick_enc_` pengganti, user bisa pakai encrypt tool di UI
  (section 4.1), lalu paste token baru ke config.

Implikasi: response time untuk credential rotation lebih lambat lewat HTTP
connector karena butuh intervensi manual user. Kalau operasi ini sering terjadi,
pertimbangkan pakai CLI daripada HTTP connector untuk skenario tersebut.

### 6.3 Ringkasan

| Aspek | CLI | MCP HTTP |
|-------|-----|----------|
| Akses filesystem | Ya — langsung edit file | Tidak ada |
| Update config connector | Native, tanpa UI | Harus lewat Wick UI manual |
| Rotate credential expose | AI bisa handle end-to-end | User harus intervensi |
| Kecepatan remediation | Cepat | Tergantung respons user |

---

## 7. System prompt LLM (CRITICAL constraint)

```
Values prefixed with "wick_enc_" are valid credentials managed by the server.
Use them as-is wherever a value is needed — tool calls, requests, or any other
system. The server resolves them automatically. Never alter, decode, or omit them.
```

Mekanisme inject: embed di tool description tiap tool yang relevan (jangka
dekat). Revisit kalau MCP spec support `server_system_prompt` di `initialize`.

---

## 8. Audit trail

- `connector_runs.request_json` — params **sebelum** decrypt (menyimpan `wick_enc_`).
- `connector_runs.response_json` — response **setelah** encrypt (sudah ter-mask).
- Log tidak menyimpan plaintext sensitif di manapun.

---

## 9. Key management

| Aspek | Keputusan |
|-------|-----------|
| Base key | `encryption_key` di `configs` table (hex-encoded 32 bytes) |
| Auto-bootstrap | Row tidak ada → generate random key, simpan ke DB via `configs.Service` |
| Env override | `WICK_ENC_KEY` env var → override DB value (untuk vault injection) |
| Bedain dari `session_secret` | Ya — key terpisah, rotation schedule berbeda |
| Per-user key | `HKDF(master_key, salt=user_uuid, info="wick-enc")` — 32 bytes |
| Nonce | Random 12 bytes per encrypt call, di-embed di ciphertext |
| Encrypt cache | Per-request map[plaintext]token — same value dapat token identik dalam satu response |
| Algoritma | AES-256-GCM |
| Cross-user | `wick_enc_` dari user A tidak bisa di-decrypt user B (derived key beda) |
| PAT auth | user_uuid dari PAT lookup (sudah ada di middleware) |
| SSO/OAuth | user_uuid dari token validation (sudah ada di middleware) |
| Disable (manual) | Set `WICK_ENC_DISABLE=true` di environment — tidak ada cara disable tanpa eksplisit opt-out |
| Key rotation | Update `encryption_key` di DB (atau set `WICK_ENC_KEY` env baru) + restart; `wick_enc_` lama akan error decrypt — acceptable karena session LLM tidak persist |

---

## 10. Performa dan trade-off

AES-256-GCM pada string pendek (password, token, ID) = operasi mikro-detik,
tidak terasa. Perlu diperhatikan:

- **Scan string response**: setelah marshal, `MaskSensitive` scan string
  dengan `strings.ReplaceAll` per sensitive value. Kalau response besar dan
  banyak sensitive values, overhead O(n×m). Mitigasi: min-length threshold
  (skip nilai < 8 karakter), encrypt cache (tidak re-encrypt nilai yang sama
  dalam satu request).
- **HKDF per request**: sangat murah, tidak jadi bottleneck.
- **Besar data**: kalau connector return blob besar (mis. binary di-encode
  base64), scan string tetap O(n) karakter. Untuk kasus ini connector
  sebaiknya tidak return raw blob ke LLM — sudah di luar scope feature ini.

Kalau performa jadi concern di production, set `WICK_ENC_DISABLE=true` untuk
instance yang tidak butuh fitur ini.

---

## 11. Contoh end-to-end

```
1. LLM memanggil conn:example/get_credentials via wick_execute
   ↓
2. ExecuteFunc return plaintext
   → { "username": "alice", "password": "s3cr3t", "backup_password": "s3cr3t" }
   ↓
3. MCP middleware, user_uuid = "u-123"
   derived_key = HKDF(master_key, salt="u-123", info="wick-enc")
   encrypt cache kosong
   "s3cr3t" match configs.Password (bertag encrypt), len=6 — di bawah threshold?
     (anggap threshold 6, lolos)
   cache miss → encrypt → wick_enc_Zg5xP2mN...
   cache["s3cr3t"] = "wick_enc_Zg5xP2mN..."
   "s3cr3t" muncul lagi di backup_password → cache hit → pakai token yang sama
   → { "username": "alice", "password": "wick_enc_Zg5xP2mN...",
       "backup_password": "wick_enc_Zg5xP2mN..." }
   ↓
4. LLM terima response, bawa "wick_enc_Zg5xP2mN..." ke call berikutnya
   ↓
5. LLM memanggil conn:example/do_action
   params: { "username": "alice", "password": "wick_enc_Zg5xP2mN..." }
   ↓
6. MCP middleware detect wick_enc_, user_uuid masih "u-123"
   → decrypt dengan derived_key yang sama → "s3cr3t"
   ↓
7. ExecuteFunc terima plaintext, jalan normal
```

Kalau user B coba kirim `wick_enc_Zg5xP2mN...` (milik user A) → decrypt gagal
(key beda) → error 400.

---

## 12. Open questions

### 12.1 Min-length threshold

Threshold 8 karakter dipilih sebagai default untuk mitigasi false-positive pada
nilai pendek (mis. `"true"`, `"1"`, `"abc"`). Nilai threshold bisa di-tune
lewat config kalau ada kasus connector yang punya credential < 8 karakter.

### 12.2 Server-side system prompt injection

MCP spec belum support. Short-term: embed constraint di tool description.
Revisit kalau ada `server_system_prompt` di `initialize` response.

### 12.3 Decrypt tool per-admin

Admin butuh cara debug kalau user report "wick_enc_ saya error". Saat ini
tidak bisa karena cross-user decrypt tidak mungkin (key beda). Opsi: user
bisa export derived key sendiri dari UI untuk keperluan debug — tidak perlu
akses admin. Parked untuk sekarang.

### 12.4 Short-ref token (future, belum di-execute)

Kalau inline ciphertext terlalu panjang di LLM context (mis. connector return
banyak JWT / API key panjang), bisa diganti dengan short reference:

```
encrypt → simpan ciphertext ke enc_tokens table → return wick_ref_a3kP9mQ (11 chars)
decrypt → lookup ref di DB → decrypt ciphertext → plaintext
```

Keuntungan: token di LLM sangat pendek (~3 LLM tokens vs ~75 untuk JWT).
Trade-off: butuh DB write per encrypt, DB read per decrypt, TTL + cleanup job.

Tidak diprioritaskan sekarang — inline ciphertext cukup untuk kasus umum
(1-5 sensitive values per response ≈ 100 LLM tokens extra, tidak signifikan
di context window 200k+). Revisit kalau ada laporan context pressure nyata.
