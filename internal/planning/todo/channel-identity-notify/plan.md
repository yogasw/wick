# Channel identity + admin notifications

Sebuah wick user bisa datang dari beberapa pintu: web, Slack A, Slack B,
Telegram. Hari ini yang tercatat cuma **browser push** (`push_subscriptions`)
dan **OAuth connector account** (`connector_accounts`). Identitas channel yang
dipakai untuk auto-register **tidak disimpan sama sekali** — `resolveWickUser`
mencocokkan email lalu membuang Slack user ID-nya.

Dokumen ini menutup itu: satu tempat mencatat "user ini terhubung lewat mana",
supaya notifikasi bisa dikirim ke tujuan yang jelas dan bisa di-pause per
device/connection.

---

## TODO

### Fase 1 — simpan channel identity (fondasi, wajib duluan)

- [x] Tabel `user_channel_identities`: `user_id`, `channel_type` (slack |
      telegram | …), `instance_key` (bot/workspace mana — Slack A vs Slack B),
      `external_user_id`, `display_name`, `email_at_link`, `verified_at`,
      `disabled_at`, `last_seen_at`.
- [x] `instance_key` **wajib** ada. Tanpa itu "Slack" ambigu begitu ada dua bot
      / dua workspace, dan notif bisa terkirim ke workspace yang salah.
      Channel sudah punya konsep ini (`sessionPrefix`, `instanceKey`).
- [x] Tulis baris ini saat `resolveWickUser` berhasil (upsert by
      `channel_type + instance_key + external_user_id`).
- [x] `last_seen_at` di-update tiap pesan; jangan bikin baris baru.

### Fase 2 — UI: Channel connections

- [x] Section baru di halaman profil (`internal/login/view/profile.templ`),
      **sejajar** dengan "Notification devices", bukan di dalamnya — device
      push itu browser, connection itu akun di layanan lain. Menyatukannya
      bikin "pause" jadi ambigu.
- [x] Per baris: channel + instance (mis. `Slack · acme-workspace`), display
      name, kapan terakhir aktif, tombol **Pause** / **Resume**.
- [x] Web **tidak** dirender sebagai connection (sesuai arahan: web sudah pasti).
- [x] Admin lihat punya user lain lewat **View as**, bukan endpoint terpisah.

### Fase 3 — notifikasi admin (yang diminta eksplisit)

- [x] **Event 1: user baru daftar lewat channel** → notif ke semua admin.
- [x] **Event 2: user di-approve** → notif ke user itu.
- [x] Pengiriman: coba tiap tujuan yang aktif untuk penerima — push device
      (`SendToUser`) + tiap channel connection yang tak di-pause.
- [x] Slack DM lewat `OpenConversationContext` (sudah ada di
      [send.go:206](../../../agents/channels/slack/send.go#L206)); butuh scope
      `im:write`, dan **kegagalan tak boleh membatalkan approve/register**.

---

## Kondisi sekarang (hasil pembacaan kode)

| Yang ada | Di mana | Menyimpan apa |
|---|---|---|
| Browser push | `push_subscriptions` | endpoint, `DeviceLabel`, `DisabledAt`, `LastSeenAt` |
| PN ID | `PushService.UserPushID` | `pn_` + HMAC(userID) — stabil, tak bocorkan user ID |
| OAuth account | `connector_accounts` | `WickUserID` + `ExternalUserID` + token |
| **Identitas channel chat** | **— tidak ada —** | **hilang setelah match email** |

Dua hal yang sudah ada dan harus dipakai ulang, jangan dibuat baru:

- `PushSubscription.DisabledAt` — pola "pause this device" sudah ada.
- `PushService.SendToUser(ctx, userID, title, body, url)` —
  [push_service.go:110](../../../pkg/pwa/push_service.go#L110).

`connector_accounts` **tidak** cocok untuk ini walau punya
`WickUserID`+`ExternalUserID`: barisnya per *instance connector* dan wajib
`AccessToken` (`not null`), sedangkan identitas Slack pengirim tak punya token
— dia hasil `users.info`, bukan OAuth. Memaksakannya berarti nulis token kosong
ke kolom yang artinya kredensial.

---

## Keputusan (sudah dijawab)

1. **Admin lihat connection user lain — BOLEH.** Diberikan lewat "View as":
   admin masuk ke akun user dan lihat panel-nya sendiri, jadi tak perlu
   endpoint admin terpisah yang membocorkan akun chat semua orang sekaligus.
2. **Notif ke semua admin approved.** Toggle per-admin belum dibuat — kalau
   nanti berisik, itu penambahan kecil.
3. **`email_at_link` tetap disimpan.** Alasannya jadi kuat setelah bug
   duplikat ketemu: kolom ini yang menunjukkan sebuah link dulu dicocokkan
   lewat email apa, dan itu satu-satunya jejak kalau email berubah.

### Sisa

- [ ] Toggle notif per-admin (kalau ping ke semua admin terasa berisik).
- [ ] Telegram `SendDirect` belum ada — identity + merge sudah jalan, tapi
      Telegram belum bisa MENERIMA notif (butuh method kirim DM di channel-nya).
- [ ] Wiring Telegram ke `EmaillessResolver` di server: fondasinya sudah ada
      dan tertest, tapi channel Telegram belum punya owner hook sama sekali.

### Telegram: kenapa jadi user terpisah

Telegram **tak melaporkan email** — cuma numeric id + username. Jadi tak ada
field yang bisa mencocokkan orang itu ke wick user; auto-match mustahil, bukan
sekadar belum diimplement.

Konsekuensinya (sesuai arahan user):

- Sender Telegram lahir sebagai **akun terpisah**, email sintetis
  `telegram-<id>@channel.local` (kolom email unik + NOT NULL, jadi harus ada
  nilainya; dibikin jelas-jelas palsu supaya tak pernah tertukar alamat nyata).
- Akun itu **unapproved**, dan admin dinotifikasi — tapi pesannya beda:
  "needs linking", bukan "needs approval". Approve sendirian justru salah,
  orangnya jadi punya dua akun.
- Admin **merge manual** di Admin → Users. Tak ada auto-merge: satu-satunya
  sinyal lain cuma display name, dan dua orang bisa punya nama sama.

---

## Catatan penting

**Notif ke Slack butuh bot bisa DM.** `OpenConversationContext` butuh scope
`im:write`; kalau kurang, Slack balas error dan
[send.go:217](../../../agents/channels/slack/send.go#L217) sudah punya hint-nya.
Jalur notif harus **best-effort**: approve user tak boleh gagal cuma karena
DM-nya gagal.

**Jangan kirim notif "user baru" ke user yang belum di-approve.** Isi notif
menyebut ada pendaftaran; kalau penerimanya salah, itu kebocoran kecil.
Penerima harus admin **approved**.

**Pause harus berlaku saat kirim, bukan cuma tampilan.** Kalau `disabled_at`
cuma bikin UI abu-abu tapi pengiriman tetap jalan, tombolnya bohong.

**PN ID stabil per user**, bukan per device — dia HMAC dari userID
([push_service.go:53](../../../pkg/pwa/push_service.go#L53)). Jadi PN ID **tak
bisa** membedakan "kirim ke browser ini saja". Kalau nanti mau kirim ke satu
device tertentu, itu butuh identifier baru — jangan sampai keliru menganggap PN
ID sudah cukup.
