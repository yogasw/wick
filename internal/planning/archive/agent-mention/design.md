# Agent Mention — percakapan dua arah antar agent (design)

Status: **mendarat di branch `feat/agent-mention` (belum di-commit).** Semua
bagian desain terkunci dan terimplementasi; rencana eksekusi + hasil verifikasi
ada di [plan.md](plan.md).
Update terakhir: 2026-08-02.

**Yang belum:** mockup.html, dan angka `ask_timeout` / `inbox_cap` belum diuji
beban. Fan-out worker (§7) tetap butuh UI board yang sampai sekarang nol
komponen — mention ngak menutup itu.

Delegasi hari ini **satu arah dan sekali jalan**: leader kasih task, sub-agent
kerja dengan konteks bersih, balikin hasil, bubar. Ngak ada cara sub-agent nanya
balik, ngak ada cara dua agent koordinasi, dan ngak ada cara manggil agent yang
sama dua kali sambil dia masih inget obrolan sebelumnya. Dokumen ini menambah
**alamat + kotak surat** di atas mesin delegasi yang sudah mendarat.

---

## TODO

### Terkunci

- ✓ **Target `@` = instance, bukan role** — resolve otomatis: ada instance hidup
  dengan handle itu di pohon ini → pakai; ngak ada → spawn dari roster project.
- ✓ **Batas = satu pohon delegasi + roster project.** Bukan lintas conversation.
- ✓ **Alamat hidup, proses bangun pas dipanggil** — idle = proses mati, mention
  masuk → `--resume` pakai `CLISessionID` yang sudah tercatat di `agents.json`.
- ✓ **Satu antrean FIFO per handle**, dua cara kirim: `ask` (nunggu balesan) dan
  `tell` (lanjut jalan).
- ✓ **Rem = budget root (existing) + hop cap (baru)**, dan **sisa jatah selalu
  kelihatan agent** lewat footer tiap pesan.
- ✓ **Kena cap ≠ mati** — yang ditolak cuma pengiriman baru; instance tetap
  beralamat dan bisa dilanjut.
- ✓ **Leader boleh stop peserta pohonnya** (sejalan doktrin `takeover.go`:
  stopping ≠ steering).
- ✓ **Advisor = sub-agent, bukan sesi utama** (§4).

- ✓ **Batch drain** — semua pesan antre jadi satu turn, bukan satu turn per
  pesan (§5.2).
- ✓ **`ask` ngak bisa deadlock** — lupa `reply` → teks akhir turn jadi reply
  otomatis (§5.3).
- ✓ **Deteksi `@` ketat** — awal baris, harus ada di roster, di luar code fence;
  ragu = diam (§5.4).
- ✓ **Hop cuma manusia yang boleh nambah** (§6.1).
- ✓ **Fan-out task besar lewat board, bukan mention** (§7).

### Belum dibahas

- ☐ Sinkron mockup (belum ada; ikut pola `*-mockup.html` + `*-design.md`).
- ☐ Angka `sub_agents_ask_timeout_min` (usul 10) & `sub_agents_inbox_cap`
  (usul 20) belum diuji beban.

### Prasyarat sebelum ini kepakai

- ☐ `sub_agents_enabled` masih **default false** — delegasi (dan otomatis
  mention) ditolak governor sampai dinyalain. Bukan bug, opt-in disengaja.
- ☐ Tab rail **Sub-agents** disembunyikan sampai session pernah delegasi
  (`subAgents.length > 0`, `DetailView.svelte:1156`) — jadi selama switch off,
  panelnya ngak pernah muncul. Dua gejala, satu sebab.

### Temuan penemuan-jalan (bukan scope mention, jangan hilang)

Semuanya sudah dicatat juga di [multi-agent/design.md](../multi-agent/design.md)
tabel "❌ Belum ada sama sekali" supaya ngak nyangkut di percakapan doang:

- ☐ **Discoverability**: entri sidebar kiri "Sub-agents" (halaman roster profil)
  ada tapi terkubur di grup collapse **More**, `view/layout.templ:149`. Rail
  kanan itu barang lain (panel intip sub-agent per-session) — gampang dikira
  sama, dan dua-duanya ngak kelihatan waktu switch off.
- ☐ Kill-switch ngak sesuai deskripsinya — connector tetap kelihatan saat off,
  cuma `delegate` yang ditolak, jadi kebacanya "diblokir admin".
- ☐ Blok prompt Sub-agents ngak kondisional ke `sub_agents_enabled`.
- ☐ Tiga knob mati di editor profil: `can_delegate`, `strict_mcp`,
  `allowed_native_tools`.
- ☐ Empat sambungan setengah jadi Fase 5/7/8 (squad narrowing ngak dipanggil,
  `AutoDelegate` ngak dibaca, `Request.TaskID` ngak diisi, `StartTask` dikirim
  `""`) — sudah tercatat di doc itu sejak awal.

---

## 1. Apa yang sudah ada vs apa yang baru

Cek silang ke [multi-agent/design.md](../multi-agent/design.md) — sebagian besar
pipa yang dibutuhkan **sudah mendarat**, yang belum cuma alamat dan protokolnya.

| Kebutuhan mention | Sudah ada? | Di mana |
|---|---|---|
| Kirim pesan **masuk** ke agent yang lagi jalan | ✅ | `Steerer` (dipakai human take-over) |
| **Re-prompt** session dengan teks | ✅ | `ResultDeliverer.DeliverToSession` (async callback Fase 4) |
| Agent nanya lalu **nunggu** jawaban | ✅ pola | `internal/agents/askuser` (socket, agent → manusia) |
| Bangun ulang agent dengan konteks utuh | ✅ | `CLISessionID` + `--resume` di `agents.json` |
| Model beda per role | ✅ | `AgentProfile.provider` + `.model` |
| Suntik teks ke system prompt anak | ✅ | `Meta.SystemAddon` (dihidupkan di agent-scope) |
| Budget turn/token per pohon | ✅ | `delegation/governor.go`, `cost.go` |
| Stop satu sub-agent | ✅ | `delegation/interrupt.go`, `pool.KillAgent` |
| Panel intip percakapan sub-agent | ✅ | rail tab `subagents` + `SubAgentPanel.svelte` |
| **Alamat stabil per instance** | ❌ | baru — §2 |
| **Kotak surat + antrean** | ❌ | baru — §3 |
| **Roster yang agent tahu** | ❌ | desain lama menjanjikan, implementasinya nol |
| **Hop cap** | ❌ | baru |

Jadi jawabannya: **ngak kelupaan, tapi juga belum sesuai.** Desain multi-agent
menutup delegasi satu-arah sampai tuntas dan sengaja **menolak** percakapan
antar-agent (§15 Rejected: "Chatroom multi-agent (stoa)"). Dokumen ini membuka
sebagian dari yang ditolak itu — bukan chatroom bebas, tapi pesan bertarget
dengan alamat, antrean, dan rem.

### Batas yang sengaja dilanggar

[`takeover.go:32-35`](../../../agents/delegation/takeover.go) menulis eksplisit:

> A leader agent cannot call this — steering is a human act, and letting one
> agent inject turns into another would blur the delegation boundary the whole
> design rests on.

Mention **memang** membuka pintu itu. Bayarannya di §2.3: pengirim ditulis
server, pesan agent ngak pernah menyalakan `UserSteered`, dan hasil tetap jujur
soal siapa yang mempengaruhi. File yang sama menegaskan *stopping* beda urusan
dan "always-allowed" — jadi "leader boleh kill peserta" ngak nabrak apa-apa.

---

## 2. Alamat & roster

### 2.1 Handle

Tiap instance dalam satu pohon punya nama panggilan unik, diturunkan dari role
key dan di-dedup: `reviewer`, `reviewer-2`. Leader dapat handle juga (`main`),
jadi sub-agent bisa `@main aku nemu ini, lanjut ngak?` — dua arah beneran.

- Kolom baru `handle` di `agent_delegations`, unique `(root_id, handle)`.
- Handle ≠ role key. Dua instance dari role yang sama harus bisa dibedakan;
  itu sebabnya `@` menunjuk instance, bukan role.

### 2.2 Roster nempel di pesan, bukan cuma di system prompt

Roster berubah terus — instance lahir, instance kelar. Snapshot pas spawn basi
dalam hitungan menit dan bikin agent menyebut handle yang sudah ngak ada.

Maka: blok roster awal disuntik saat spawn (`Meta.SystemAddon`), lalu **tiap
pesan masuk membawa footer roster + sisa jatah yang segar**:

```
── from @reviewer ─────────────────────────
2 dari 5 file kelar. Yang auth.go mencurigakan,
kamu mau aku lanjut atau stop di sini?

roster: @main (leader) · @reviewer (kerja) · @researcher (nganggur)
sisa: 12/40 turn · 340rb/1jt token · 3/10 hop
```

Nol prompt-rewrite, nol state basi, dan agent selalu lihat sisa jatah sebelum
memutuskan nanya lagi. Ini jawaban langsung atas "AI harus tau budget-nya biar
ngak mati di tengah jalan".

### 2.3 Pengirim ditulis server

`from` diisi dari sesi pemanggil, **bukan** argumen model — alasan yang sama
kenapa `session_id` diambil dari header: kalau model yang mengisi, `@reviewer`
bisa mengaku `main` dan mewarisi otoritasnya. Pesan dari agent **ngak pernah**
menyalakan `UserSteered`; flag itu tetap milik manusia supaya laporan hasil ngak
berbohong soal siapa yang menyetir.

---

## 3. `ask` vs `tell`

Satu antrean, dua cara kirim. FIFO tetap utuh; yang beda cuma pengirimnya
nungguin atau ngak.

| Verb | Pengirim | Untuk | Contoh |
|---|---|---|---|
| **`ask @handle`** | blocking, nunggu balesan itu | konsultasi yang jawabannya dibutuhkan sekarang | pola Advisor (§4) |
| **`tell @handle`** / `@mention` di teks | lanjut jalan | koordinasi, lapor progres, obrolan panjang | dua agent kerja paralel |

`delegate` yang ada **tetap kepakai** dan ngak tumpang-tindih: `delegate` =
serahkan tugas ke role, sekali jalan, hasil akhir. `ask` = tanya ke handle yang
sudah kenal konteksnya.

`ask` justru lebih aman dari `tell`: pengirim ke-blok, jadi ngak bisa nembak 10
pesan sekaligus.

---

## 4. Pola Advisor — kenapa advisor bukan sesi utama

Bentuk yang diminta: **Executor** (model murah) jalan di main loop, **Advisor**
(model mahal) dipanggil on-demand.

| Diagram | Di wick |
|---|---|
| Executor, main loop | Leader `@main` — sesi percakapan |
| Advisor, on-demand | Profil `advisor`, `model=claude-fable-5` |
| Tool call → | `ask @advisor` |
| ← Sends advice | balesan advisor jadi hasil |
| kotak putus-putus | alamat hidup, proses mati pas nganggur; bangun via `--resume` |

**Advisor sengaja bukan sesi utama.** Sesi utama menyimpan seluruh transkrip dan
mengirim ulang tiap turn — menaruh model termahal di situ berarti bayar premium
untuk konteks terbesar, termasuk buat balasan "oke, lanjut". Sesi 40 turn gampang
100rb token per giliran; satu `ask` ke advisor mungkin 3rb. Advisor sebagai
sub-agent dapat konteks bersih: **model mahal di prompt murah.**

Kebalikannya tetap sah kalau nilainya beda — main = planner mahal, executor =
sub-agent murah — dan ngak butuh kode tambahan, cuma beda konfigurasi
project/preset. Pilihannya: mau ngobrol langsung sama model terkuat (mahal tiap
turn) atau manggil dia pas mentok doang (diagram, disarankan).

**Jebakan yang harus diakui:** kualitas nasihat dibatasi bahan yang *executor*
pilih untuk dikirim. Model murah yang lagi bingung sering ngak sadar potongan
mana yang penting → nanya bawa konteks salah → dapat nasihat yang percaya diri
tapi meleset. Rem praktis: `ask` wajib bawa `context` (pola `delegate`), dan
prompt advisor diisi "kalau bahannya kurang, minta yang kurang, jangan nebak".

Memberi advisor akses baca transkrip leader ditolak: membocorkan seluruh
percakapan tiap konsultasi = mahal dan merusak isolasi konteks yang jadi fondasi
delegasi.

---

## 5. Mailbox

### 5.1 Tabel

```sql
agent_messages (
  id           uuid primary key,
  root_id      uuid not null,   -- pohon; sekaligus batas alamat
  from_handle  text not null,   -- DIISI SERVER, bukan model (§2.3)
  to_handle    text not null,
  body         text not null,
  kind         text not null,   -- 'ask' | 'tell' | 'reply'
  reply_to     uuid,            -- kind='reply' → id ask yang dijawab
  auto_reply   boolean not null default false, -- §5.3
  status       text not null,   -- queued | delivered | answered | dropped
  hop          int  not null,   -- posisi di rantai hop saat dikirim (§6.1)
  created_at   timestamptz not null,
  delivered_at timestamptz
)

create index idx_agent_messages_inbox on agent_messages(root_id, to_handle, status);
create index idx_agent_messages_reply on agent_messages(reply_to);
```

Plus satu kolom di `agent_delegations`: `handle text`, unique `(root_id, handle)`.

### 5.2 Aturan antar

**Batch, bukan satu-pesan-satu-turn.** Semua pesan `queued` untuk satu handle
diserahkan sebagai **satu** turn, digabung urut `created_at`. Alasannya biaya:
tiap turn itu satu putaran model penuh, jadi 5 pesan = 5 turn = 5× harga, dan
jawabannya kepotong-potong karena tiap turn cuma lihat satu pesan. Cap batch 10
pesan; sisanya nunggu turn berikutnya.

**Diantar di batas turn, bukan di tengah.** Target lagi `working` → pesan nunggu
`event.Done` turn berjalan, baru di-drain. Nyuntik di tengah turn = adu tulis di
stdin proses yang lagi mikir.

**Target nganggur (proses mati) → `--resume`.** Ini jalur normal, bukan
pengecualian: instance memang sengaja dimatikan saat idle (§TODO).

**Resume gagal → jujur, jangan pura-pura.** Kalau transcript CLI hilang atau
provider-nya ganti, wick spawn instance baru dengan handle yang sama, dan pesan
pertama diberi label eksplisit: *"instance ini kehilangan konteks sebelumnya"*.
Diam-diam mengganti agent yang "inget" dengan agent kosong = pengirim dapat
jawaban percaya diri dari lawan bicara yang sebenarnya amnesia.

**Target sudah terminal** (`interrupted` / `failed`, dihentikan manusia) → pesan
**ditolak** ke pengirim dengan alasannya, bukan ngantre selamanya.

**Inbox penuh** (cap 20 `queued`) → pengirim dapat error "inbox @x penuh, dia
ketinggalan jauh". Rem tekanan balik, biar satu agent cerewet ngak numpuk kerja
yang ngak akan kebaca.

### 5.3 `ask` — nunggu balesan tanpa bisa deadlock

Pengirim blok sampai salah satu: ada `reply` dengan `reply_to` = id ask-nya,
timeout (default 10 menit), atau target mati.

Masalahnya model gampang lupa manggil `reply` — dan kalau lupa, pengirim
menggantung sampai timeout. Maka: **penerima selesai turn tanpa `reply`
eksplisit → teks akhir turn itu otomatis jadi reply**, ditandai
`auto_reply=true`. Jadi jalur normalnya eksplisit, tapi lupa ngak pernah
berujung deadlock.

Timeout habis → pengirim dikasih tahu "belum dijawab", dan pesannya **tetap di
inbox**. Timeout itu berhenti nunggu, bukan membatalkan.

### 5.4 Deteksi `@` di teks

Op eksplisit adalah transport-nya; `@handle` di teks cuma gula yang dikompilasi
ke op yang sama. Aturannya ketat, dan sengaja:

- hanya di **awal baris**,
- handle harus **persis ada di roster pohon itu**,
- **di luar** blok kode (skip fenced code).

Ngak match roster → biarkan jadi teks biasa. Ngak error, ngak kirim.

Ketat karena output agent coding penuh `@` yang bukan mention: `@param`,
`@media`, `@ts-ignore`, scope npm `@scope/pkg`, alamat email. Salah tangkap
bukan cuma berisik — itu **spawn agent dan bakar token gara-gara nulis email**.
Kalau ragu, diam.

---

## 6. Rem

### 6.1 Hop cap

`hop` = jumlah pesan antar-agent berturut-turut **tanpa** turn dari manusia.
Default 10, config baru `sub_agents_max_hops`. Reset tiap manusia kirim turn ke
pohon itu.

Kena cap → pengiriman baru ditolak dengan instruksi "rangkum dan lapor ke user".
**Instance tetap hidup dan beralamat** — yang berhenti cuma pesan baru, bukan
pekerjaannya. Ini jawaban atas "takutnya mati di tengah jalan".

**Yang boleh nambah hop cuma manusia.** Kalau leader bisa reset sendiri, cap-nya
ngak ngerem apa-apa — dan leader yang lagi asyik ping-pong justru yang paling
yakin butuh perpanjangan. UI kasih tombol **+10 hop** di rail panel.

### 6.2 Budget

Turn dari mention makan **budget root yang sudah ada** (turn + token). Ngak ada
akuntansi baru, ngak ada plafon paralel. Habis = perilaku sekarang: yang jalan
dibiarkan selesai, yang baru ditolak.

Sisa jatah ikut di footer tiap pesan (§2.2) — agent ngak boleh kaget kehabisan.

### 6.3 Stop oleh leader

Op `stop @handle`. Pemanggil harus di root yang sama; leader boleh menghentikan
keturunannya. Lewat jalur interrupt yang ada, jadi hasil parsial tetap kembali
sebagai hasil, bukan error.

`CanInterrupt` (`acl.go:85`) diperluas: sekarang manusia-pemicu atau admin;
tambah "agent di root yang sama, terhadap keturunannya". Sejalan doktrin
`takeover.go` — *stopping is not steering*. Take-over tetap manusia doang.

---

## 7. Pola task besar: fan-out worker model kecil

Use-case yang diincar: **scrape/olah ratusan item pakai banyak agent kecil.**
Bentuknya beda dari obrolan dua agent, dan sebagian besar mesinnya **sudah ada
tapi belum tersambung**.

```
@main (Sonnet, leader)
  │  enqueue 500 task ke board
  ├─ @worker-1..N  (Haiku, murah)  ── claim → start → complete
  │       │
  │       └─ mentok (captcha / layout berubah) → ask @advisor
  └─ @advisor (Fable, on-demand)
```

Kenapa board, bukan mention: 500 item ngak boleh jadi 500 pesan. Board Fase 7
(`enqueue/claim/start/complete` + sweeper stale-claim) memang untuk ini —
worker **narik** kerjaan, bukan didorongin, jadi ngak ada yang perlu tahu ada
berapa worker.

Mention tetap kepakai, tapi tipis: worker `@main` pas nemu penghalang, dan
`ask @advisor` pas mentok. Worker **ngak** saling ngobrol — ngak ada gunanya dan
itu jalan tercepat menuju tagihan.

**Prasyarat yang jadi wajib untuk pola ini** (di doc multi-agent masih
terdaftar sebagai knob mati — untuk use-case ini bukan lagi opsional):

- `Request.TaskID` diisi saat delegasi lahir dari task board,
- `StartTask(..., delegationID)` dikirim beneran (sekarang `""`),
- `board.AutoDelegate` dibaca, biar stage `ready` otomatis narik worker,
- `max_parallel` default **4** ketinggian rendah untuk fan-out — jadikan
  per-board/per-root, bukan konstanta global. Ini rem biaya paling tajam di pola
  ini, jadi harus disetel sadar, bukan dinaikin diam-diam.
- **UI board belum ada sama sekali.** Untuk fan-out minimal butuh daftar task +
  progress, kalau ngak, 500 task jalan tanpa siapa pun bisa lihat.

Model per role bikin ekonominya masuk: worker Haiku, leader Sonnet, advisor
Fable dipanggil pas mentok doang.

---

## 8. UI

| Permukaan | Isi |
|---|---|
| Rail panel **Sub-agents** (ada) | Tambah tampilan percakapan: daftar handle + badge jumlah inbox di kiri, thread pesan di kanan dengan chip `from → to`. Tombol **stop** per handle, tombol **+10 hop** saat cap habis |
| Thread leader | Kartu ringkas per `ask` (pola kartu `delegate` yang ada). `tell` **ngak** bikin kartu — cukup di panel, biar thread utama ngak jadi ruang obrolan agent |
| Board (belum ada) | Prasyarat §7 |

### 8.1 Sub-agent selesai harus kelihatan sebagai sub-agent

Sekarang hasil delegasi async dikirim balik lewat
`poolDeliverer.DeliverToSession` dengan `session.OriginUI` + role `"user"`
(`delegation_wiring.go:54`) — jadi di transkrip **ngak bisa dibedain dari
manusia yang ngetik sendiri**. Dua akibatnya: leader dikasih tahu operatornya
bilang sesuatu yang ngak pernah dia bilang, dan pembaca ngak lihat ada
sub-agent yang balik sama sekali.

Yang diinginkan (dan yang bikin "kelihatan agent saling bantu" terasa):

```
● Agent @debugger finished · 28m 15s
● Ketemu: bukan IPv6. Proses launchd ngak punya izin TCC …
```

Perbaikannya kecil tapi bukan kosmetik: kirim dengan source `subagent`
(bukan `OriginUI`), dan header hasil bawa **handle + durasi**
(`@debugger finished · 28m 15s`). Rendering-nya numpang mekanisme badge
`turn.source` yang sudah ada di `ThreadMessage.svelte`.

---

## 9. Test

- FIFO + batching: 5 pesan antre → **satu** turn, urut.
- Resume gagal → instance baru, pesan berlabel "konteks hilang".
- `ask` lupa di-`reply` → auto-reply dari teks akhir turn, `auto_reply=true`.
- `ask` timeout → pengirim lanjut, pesan **masih** di inbox.
- Spoof: model nulis `from` sendiri → diabaikan, server yang isi.
- Lintas-root: `@handle` milik pohon lain → ditolak.
- Hop cap kena → kirim ditolak, instance tetap beralamat; reset saat manusia
  kirim turn.
- Angka footer (turn/token/hop) cocok dengan governor.
- Inbox penuh → pengirim dapat error, bukan diam-diam hilang.
- Stop oleh leader → hasil parsial, bukan error. Take-over oleh agent → ditolak.
- Deteksi `@`: `@media`, `@param`, `@scope/pkg`, email, dan `@handle` di dalam
  code fence **tidak** memicu kirim.

---

## 10. Ringkas perubahan

**Storage:** 1 tabel (`agent_messages`), 1 kolom (`agent_delegations.handle`),
2 index.

**Config baru** (semua ada pembacanya — doc multi-agent menolak knob mati):
`sub_agents_max_hops`, `sub_agents_ask_timeout_min`, `sub_agents_inbox_cap`.

**Op connector `sub-agents`:** `message` (to, body, kind), `reply` (ask_id,
body), `stop` (handle). `list_agents` bertambah handle hidup + status.

**Yang dipakai ulang tanpa diubah:** `Steerer`, `DeliverToSession`, `--resume` +
`CLISessionID`, `Meta.SystemAddon`, governor budget, jalur interrupt, rail panel.
