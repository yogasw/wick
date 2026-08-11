# Sub-agent continue: satu session dipakai terus (design)

Status: **terimplementasi** (2026-08-08). Rencana eksekusi + hasil verifikasi ada
di [plan.md](plan.md).

Satu keluhan yang ditutup dokumen ini: **sub-agent selalu dapat session baru**,
sehingga pola manager→developer (main agent mengawasi sub-agent yang mengerjakan
tugas panjang, mengoreksi kalau melenceng, menyuruh lanjut kalau belum selesai)
tidak bisa dijalankan tanpa merusak context si developer.

Sesi sub-agent sebenarnya **sudah** persisten — `childSessionIDFor(parent,
delegationID)` deterministik dan `EnsureChildSession` no-op kalau folder sudah
ada. Yang bikin "selalu session baru" adalah: jalur untuk **melanjutkan** kerja
sebuah delegation tertutup, jadi satu-satunya cara maju adalah `delegate` lagi —
dan `delegate` selalu bikin UUID baru, yang berarti session baru.

---

## TODO

### Akan dikerjakan

- ✓ **Op baru `continue`** di connector `sub-agents`. Melanjutkan delegation
      yang sudah ada: session, transcript, dan handle-nya dipertahankan.
      `delegate` tidak berubah semantiknya.
- ✓ **Param `continue_id` di `delegate`** sebagai jalan pintas yang me-rute ke
      logika yang sama. Ada dua permukaan karena keduanya diminta; `continue`
      adalah yang didokumentasikan, `continue_id` yang ditoleransi.
- ✓ **Buka `message` untuk semua status terminal**, bukan cuma `done`.
      `stopped_max_turns` justru yang paling perlu — itu artinya kerja kepotong,
      bukan gagal.
- ✓ **Handle terminal tetap muncul di roster** dengan flag `live`, tidak
      dicoret. Agent yang tidak terlihat tidak bisa dilanjutkan.
- ✓ **Budget ditambah, bukan diwarisi.** Continue pada delegation yang mati
      kena `stopped_max_turns` harus menaikkan plafon, kalau tidak ia langsung
      mati lagi di turn pertama.
- ✓ **Jujur saat resume gagal.** Kalau `CLISessionID` kosong, hasilnya harus
      bilang eksplisit bahwa agent mulai dari nol — jangan diam-diam.
- ✓ **`collect` di tengah jalan memuat progress** (PULL). Delegation yang masih
      berjalan mengembalikan pekerjaan sejauh ini, bukan `Pending: true` kosong.
      Tanpa ini pengawas hanya bisa menilai setelah terlambat.
- ✓ **Op `progress` untuk sub-agent** (PUSH). Sub-agent melapor sendiri di
      tengah kerja — "sudah sampai sini, lanjut ke X" — tanpa menunggu ditanya,
      dan laporan itu **membangunkan** main agent.
- ✓ **Kapan melapor diputuskan sub-agent**, bukan hitungan turn. Sebuah turn
      adalah satu panggilan, bukan satu satuan kemajuan — "tiap 5 turn" bisa
      berarti lima kali baca file atau satu fitur selesai.

### Di luar scope

- Tidak ada supervisor loop otomatis di Go. Pengawasan per-menit dilakukan main
  agent lewat `wick_schedule_message` (`every: "1m"`) yang sudah ada. Kalau
  nanti terbukti main agent tidak disiplin, itu dokumen terpisah.
- `checkerloop` tidak disentuh. Ia tetap hanya nyala untuk incident.
- Tidak ada lock "satu worker slot". Continue sudah cukup untuk menjaga satu
  session; lock mutual-exclusion adalah masalah lain.

---

## §1 Kenapa session selalu baru

Tiga tembok, urut dari yang paling sering kena.

### 1.1 Roster mencoret agent terminal

[internal/agents/delegation/roster.go](../../../agents/delegation/roster.go):

```go
if d.Handle == "" || entity.IsTerminalDelegationStatus(d.Status) {
    continue
}
```

Developer selesai → status `done` → handle-nya lenyap dari resolver. `list_agents`
tidak menampilkannya lagi, jadi main agent tidak punya alamat untuk menyuruhnya
lanjut. Komentar di atasnya menjelaskan alasannya: pesan ke agent mati akan
mengantre selamanya pada pembaca yang tidak akan pernah datang.

Alasan itu **valid saat ditulis**, tapi tidak lagi begitu setelah §2: kalau
pengiriman pesan merespawn child di session yang sama, pembacanya datang.

### 1.2 Mailbox menolak terminal kecuali `done`

[internal/agents/delegation/mailbox.go](../../../agents/delegation/mailbox.go):

```go
if entity.IsTerminalDelegationStatus(target.Status) && target.Status != entity.DelegationDone {
    return nil, fmt.Errorf("@%s stopped (%s) and cannot take messages", ...)
}
```

`stopped_max_turns`, `stopped_budget`, `interrupted`, `failed` — semua ditolak.
Persis kasus "developer mati sebelum kelar" yang paling butuh dilanjutkan.

### 1.3 Resume bisa gagal diam-diam

[internal/pkg/api/delegation_wiring.go](../../../pkg/api/delegation_wiring.go)
`WakeChild` memeriksa `CLISessionID`. Kalau kosong → `ErrContextLost` → agent
tetap dibangunkan tapi **amnesia**, dan penelepon hanya diberi catatan.

Mesin resume-nya sendiri sehat: [pool.go](../../../agents/pool/pool.go) `spawn`
membaca `CLISessionID` jadi `--resume`, dan `store.go` `persistCLISessionID`
menulisnya dari event provider. Komentar "CLISessionID is never written" di
pool.go adalah deskripsi bug yang **sudah** ditutup (jalur delegasi memanggil
`AddAgent` di runner.go), bukan gap aktif.

---

## §2 Op `continue`

### 2.1 Bentuk

```go
type continueInput struct {
    DelegationID string `wick:"required;desc=The delegation to continue (from delegate or collect)."`
    Task         string `wick:"required;textarea;desc=The next instruction. The sub-agent still
                          remembers its earlier work, so say what to do NEXT — do not restate the
                          original brief."`
    MaxTurns  int `wick:"desc=Extra turns to grant for this continuation. Clamped to the system ceiling."`
    MaxTokens int `wick:"desc=Extra tokens to grant. Clamped to the system ceiling."`
    Mode      string `wick:"desc=background (default) or foreground."`
}
```

Sengaja **tidak** ada `profile`, `workspace`, `memory_mode`: semuanya sudah
ditetapkan saat delegation dibuat, dan mengubahnya di tengah jalan berarti
melanjutkan agent yang berbeda dari yang dulu bekerja.

### 2.2 Alur

`continue` tidak memanggil `Run` — ia memanggil `execute`, yang memang sudah
dirancang untuk row yang sudah ada (komentarnya: *"spawns and drives a delegation
whose row already exists"*). Semua state dibaca dari row, jadi tidak ada asumsi
row-baru yang perlu dibongkar.

```
continue(id, task)
  → Repo.Get(id)
  → CanInterrupt(row, actor, isAdmin)     // otoritas yang sama dengan collect/stop
  → tolak kalau status == running/queued  // sudah jalan, pakai message
  → row.Task = task
  → row.MaxTurns += extra; row.MaxTokens += extra
  → Repo.MarkRunning(id)
  → execute(ctx, row, profile, effTags)
      → EnsureChildSession → no-op, folder sudah ada
      → StartAgent → pool.Send → spawn baca CLISessionID → --resume
```

`ChildSessionID` tidak pernah dihitung ulang. Itu inti seluruh perubahan.

### 2.3 Penolakan

| Status row | Perlakuan |
|---|---|
| `running`, `queued` | ditolak — "sudah jalan; pakai `message` untuk menyetirnya" |
| `done` | diterima — lanjutan normal |
| `stopped_max_turns`, `stopped_budget` | diterima — kasus utama; wajib menambah budget |
| `interrupted` | diterima, dengan catatan bahwa seorang manusia yang menghentikannya |
| `failed` | diterima, dengan catatan bahwa run sebelumnya error |

### 2.4 Kejujuran resume

Sebelum spawn, periksa `CLISessionID` lewat mekanisme yang sama dengan
`WakeChild`. Kalau kosong, hasil `continue` membawa:

> `resumed: false` — "Transcript sebelumnya tidak bisa di-resume, jadi agent ini
> memulai dari nol. Task yang kamu kirim harus berdiri sendiri."

Ini bukan kegagalan — continue tetap jalan di session yang sama. Yang dijaga
adalah penelepon tidak menyangka agent ingat sesuatu yang sebenarnya tidak.

---

## §3 Param `continue_id` di `delegate`

Field opsional di `delegateInput`. Kalau diisi, `delegate` mendelegasikan ke
handler `continue` dan mengabaikan `profile` (dengan catatan kalau `profile`
yang dikirim berbeda dari milik row).

Ada dua permukaan untuk hal yang sama karena keduanya diminta. Yang perlu
dijaga: deskripsi `delegate` **tidak** boleh gemuk menjelaskan mode kedua —
cukup satu kalimat yang menunjuk ke `continue`. Deskripsi op di connector ini
mengarahkan perilaku model, dan `delegate` yang ambigu adalah persis penyebab
model memilih spawn-baru padahal maksudnya lanjut.

---

## §4 Membuka `message` untuk terminal

Aturan baru di mailbox.go: yang ditolak hanya status yang benar-benar tidak
bisa dilanjutkan. Karena `pool.Send` merespawn di session yang sama, tidak ada
lagi "antre pada pembaca yang tidak akan datang".

```go
// Terminal is no longer a refusal: pool.Send respawns the child in its
// own session, so a message to a finished agent is delivered to an agent
// that remembers finishing. What IS refused is a row whose session can
// no longer be resolved at all.
```

`failed` tetap diberi catatan, bukan ditolak — run sebelumnya error, tapi
transcript-nya mungkin masih berguna.

---

## §5 Roster mempertahankan handle terminal

`Resolver` menyimpan handle terminal dengan flag, bukan mencoretnya.
`roleItem`/roster block bertambah:

```go
Live   bool   `json:"live"`             // false = selesai, masih bisa di-continue
Status string `json:"status,omitempty"` // status terminalnya
```

Presedensi mention di [roster.go](../../../agents/delegation/roster.go) tidak
berubah: agent hidup tetap menang atas role. Yang berubah, agent **mati** juga
menang atas role — karena itulah maksudnya: `@developer lanjutin` harus
menyambung developer yang tadi, bukan memulai yang baru dengan amnesia. Ini
justru memperkuat argumen yang sudah ditulis di komentar file itu.

---

## §6 Efek ke pola manager→developer

Yang jadi mungkin setelah dokumen ini:

```
main: delegate(profile=developer, task="<brief lengkap>")     → D1, session S1
main: schedule_message(session=self, every="1m",
        message="Supervisor tick: collect D1. Bandingkan dengan
                 definition-of-done. Melenceng → message @developer.
                 Belum kelar → continue D1. Kelar & lolos → cancel jadwal ini.")

[tiap menit]
main: collect(D1)
  ├─ pending          → biarkan
  ├─ done + belum     → continue(D1, "tes auth masih gagal di X. Lanjutkan.")
  ├─ stopped_max_turns→ continue(D1, "lanjutkan dari tempat kamu berhenti.", max_turns=30)
  └─ done + lolos     → cancel jadwal, lapor user
```

Satu developer, satu session, hidup dari awal sampai akhir.

Rem-nya ada pada main agent, bukan pada Go: ia menghitung tick sendiri dan
harus eskalasi ke manusia setelah batas tertentu. Ini berbeda dari `checkerloop`
yang punya `DryRounds` — dan itu memang trade-off yang dipilih di sini, supaya
tidak menyentuh logika incident yang sudah settle.

---

## §7 Progress di tengah jalan

Melanjutkan pekerjaan saja tidak cukup. Kalau pengawas baru bisa menilai setelah
sub-agent berhenti, ia menilai hal yang sudah terlanjur — persis "keblabasan"
yang mau dihindari. Jadi butuh dua arah, dan keduanya **opt-in**: sebuah tugas
pendek tidak perlu membayar laporan berkala.

### 7.1 PULL — `collect` pada delegation berjalan

Sekarang [collect.go](../../../agents/delegation/collect.go) memulangkan
`Pending: true` dengan `Result` kosong:

```go
if !entity.IsTerminalDelegationStatus(d.Status) {
    out.Pending = true
    out.Note = "Still running. Carry on with other work and collect again later; do not block on this."
    return out, nil
}
```

`PartialText` sudah tersedia di `Runner` tapi tidak pernah sampai ke MCP.
`CollectResult` bertambah:

```go
// Progress is what the sub-agent has produced so far on a delegation
// that is STILL RUNNING. Empty on a terminal row, where Result is the
// real answer. It is a peek at work in flight, not a verdict: the agent
// may still contradict it in the same turn.
Progress string `json:"progress,omitempty"`
// LastReport is the sub-agent's own most recent progress note, when it
// filed one (see §7.2). Preferred over Progress for judging direction —
// it is the agent stating where it is, not a scrape of its prose.
LastReport string `json:"last_report,omitempty"`
```

Note untuk baris pending harus diganti. Yang sekarang bilang *"do not block on
this"* — benar untuk pemanggil yang menunggu jawaban, tapi menyesatkan bagi
pengawas yang memang berniat mengintip. Teks baru harus memisahkan keduanya:
mengintip boleh, menunggu tidak.

**Penting: `collect` pada row berjalan tidak boleh `MarkCollected`.** Guard
"diserahkan sekali" hanya berlaku untuk hasil akhir. Kalau intip di tengah
menandai row terkoleksi, hasil akhirnya nanti hilang — persis bug yang paling
mahal di seluruh dokumen ini.

### 7.2 PUSH — op `progress`

Sub-agent melapor sendiri, tanpa ditanya:

```go
type progressInput struct {
    Note string `wick:"required;textarea;desc=Where you are right now, in one or two
                  sentences. What you just finished and what you are moving to next."`
    Done string `wick:"desc=Optional: what is finished so far, one item per line."`
    Next string `wick:"desc=Optional: what you are about to do next."`
    Blocked string `wick:"desc=Optional: what is stopping you, if anything. Say it here
                     rather than pushing on — the agent supervising you can unblock it."`
}
```

Ditulis ke row (kolom baru `LastReport` + `LastReportAt`) **dan** membangunkan
main agent lewat `DeliverToSession` — jalur yang sama dengan hasil async, dengan
source `subagent`, jadi main tahu ini bukan manusia yang bicara.

Membangunkan, bukan senyap, karena laporan yang tidak dibaca sampai tick
berikutnya tidak menyelesaikan apa pun yang tidak sudah diselesaikan §7.1: kalau
toh pengawas harus bertanya, ia bisa langsung mengintip `progress` mentah tanpa
si sub-agent repot menulis laporan. Nilai sebuah laporan justru ada pada
kedatangannya yang tidak perlu ditunggu.

Bedanya dengan `message(to=leader)` tinggal maksud, bukan mekanisme:

| Surface | Untuk |
|---|---|
| `progress` | "aku sudah sampai sini, lanjut ke X" — sepihak, tidak minta jawaban |
| `message(to=leader, kind=ask)` | "aku butuh keputusan sebelum lanjut" — menunggu balasan |

### 7.3 Kapan sub-agent melapor

**Sub-agent yang memutuskan.** Tidak ada `progress_every`, tidak ada hitungan
turn. Sebuah turn adalah satu panggilan bolak-balik, bukan satu satuan kemajuan:
lima turn bisa berarti lima kali membaca file, atau satu fitur selesai. Mengikat
laporan ke hitungan turn menghasilkan laporan yang tiba pada saat yang salah dan
berisi hal yang tidak layak dilaporkan.

Yang diberikan adalah kriteria, bukan jadwal — di spawn preamble
(`spawnPreamble` di run.go sudah jadi tempat komposisi hal semacam ini):

> "File a progress note with the `progress` op when you reach a point another
> agent would want to know about: a milestone finished, a plan that changed, or
> something blocking you. It wakes the agent supervising you, so report meaning,
> not activity — 'auth handler works, writing tests now', never 'read three
> files'. Say it when it happens; you are supervised, and a supervisor who
> cannot see a wrong turn early will stop you to ask about it late."

Kalimat terakhir load-bearing, alasannya sama dengan `interruptNote` yang sudah
ada: model mengabaikan instruksi pelaporan kecuali diberi tahu konsekuensinya.

Tetap opt-in per delegation lewat `supervised bool` di `delegate` — sebuah tugas
pendek tidak perlu membayar laporan sama sekali. Default false: kalimat di atas
tidak muncul di preamble, dan op `progress` tetap ada tapi tidak pernah diminta.

Penegakannya lunak: tidak ada yang memaksa sub-agent memanggil `progress`. Yang
menutup celah itu §7.1 — kalau ia lalai melapor, pengawas masih bisa mengintip.

### 7.4 Alur lengkap

Jalur utamanya PUSH — main bangun karena dilapori, bukan karena jadwal:

```
main: delegate(profile=developer, task="...", supervised=true)   → D1, session S1

developer: progress("skema DB selesai, lanjut handler auth")
  → main bangun. Sesuai rencana, diam. Turn main berakhir.

developer: progress("auth ternyata nyangkut di storage, aku refactor layer itu dulu")
  → main bangun. MELENCENG.
  → message(@developer, "storage jangan disentuh. Fokus auth, pakai yang ada.")
  → masuk session S1, antre sampai batas turn, developer belum berhenti

developer: progress("oke, auth handler jalan tanpa nyentuh storage. Nulis tes.")
  → main bangun. Diam.

developer: [selesai] → sink=session → main bangun dengan hasil
  → needs_followup=true
  → continue(D1, "tes auth masih gagal di X. Lanjutkan.")
  → session S1, --resume, ingat semuanya
```

Yang ditutup: koreksi terjadi **saat kerja masih berlangsung**, sebelum developer
terlanjur membongkar storage — dan tanpa main perlu bangun tiap menit untuk
memeriksa hal yang belum tentu berubah.

PULL (§7.1) tetap ada sebagai jaring pengaman untuk kasus sub-agent yang diam
terlalu lama. Kalau main mau memasangkannya dengan `wick_schedule_message`
(`every: "5m"`) sebagai detak cadangan, itu keputusan main — bukan sesuatu yang
dipaksakan mekanisme ini.

---

## §8 Gap yang disadari

- **Tidak ada mutual exclusion.** Dua `continue` bersamaan pada delegation yang
  sama akan berlomba. `MarkRunning` guarded, jadi yang kedua gagal — cukup untuk
  sekarang, tapi pesan errornya perlu menjelaskan itu.
- **Pelaporan tidak ditegakkan.** Sub-agent yang tidak pernah memanggil
  `progress` tidak dihukum apa-apa. Lihat §7.3 — pilihan sadar, dengan §7.1
  sebagai jaring pengaman.
- **Sub-agent cerewet membakar turn leader.** Karena setiap laporan
  membangunkan main, sub-agent yang melapor tiap langkah kecil menjadi mahal.
  Yang menahannya sekarang hanya kalimat preamble ("laporkan makna, bukan
  aktivitas"). Kalau terbukti tidak cukup, rem yang tepat adalah jeda minimum
  antar laporan per delegation — bukan hitungan turn.
- **Rem loop ada di prompt, bukan di Go.** Main agent yang memutuskan kapan
  berhenti mengawasi. Kalau terbukti tidak disiplin, itu dokumen terpisah.
