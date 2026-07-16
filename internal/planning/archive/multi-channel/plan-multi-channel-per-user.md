# Multi-Channel Per-User (di Profile) — Implementation Plan

> Goal: tiap user atur channel-nya sendiri (Slack, Telegram, REST) dari halaman **Profile**, bukan satu config global. Semua channel jadi **keyed-instance per-user** seragam. Konsep "App Owner global channel" (`user_id = NULL`) **dibuang** — murni per-user.

## TODO (ringkas, urut kerjakan)

- [ ] **T1** — Telegram: tambah `NewWithOwner()` + field `ownerUserID` + `NewConfigSourceKeyed()` (mirror Slack)
- [ ] **T2** — REST: tambah `NewWithOwner()` + field `ownerUserID` + `NewConfigSourceKeyed()` (mirror Slack); endpoint tetap satu, instance di-key per user, request di-route via PAT→userID
- [ ] **T3** — `setup.Telegram()` & `setup.Rest()`: ganti single `reg.Add` jadi loop `ListChannelOwners` + `AddKeyed` (copy `setup.Slack()`)
- [ ] **T4** — `syncChannelInstance`: tambah `case "telegram"` + `case "rest"` (sekarang cuma `case "slack"`)
- [ ] **T5** — owner-stamping: telegram chatID→wick user; REST udah punya userID dari PAT
- [ ] **T6** — UI: pindahin channel form ke tab **Profile → Channels** (`ProfileTabChannels`), scope ke user login
- [ ] **T7** — buang fallback `user_id = NULL` di setup/store path yang masih nyangkut "App Owner" global; owner = user biasa yang punya row sendiri
- [ ] **T8** — migrasi: row lama `user_id = NULL` → assign ke owner user, atau seed ulang
- [ ] **T9** — test: per-user isolation (user A gak liat config user B), keyed reload, REST route-by-PAT
- [ ] **T10** — `templ generate` + `graphify update .` + smoke (kill :8080 setelah)

---

## Konteks: apa yang UDAH ada (jangan ulang)

Plumbing per-user **sebagian besar sudah jadi** — ini bukan greenfield:

- **Tabel** `agent_channels` udah punya kolom `UserID *string` — `internal/entity/agent_channel.go:9`. Satu tabel semua channel, dibedain `Type` + `UserID`. Config = JSON di kolom `Config` (token ke-encrypt `wick_cenc_`).
- **Store helper per-user lengkap** — `internal/agents/channels/store.go`:
  - `LoadSlackForUser` (L310), `LoadTelegramForUser` (L319, **nganggur**), `LoadRestForUser` (L325, **nganggur**)
  - `GetChannelConfigMapForUser` / `SetChannelConfigKeyForUser` / `EnsureChannelForUser` / `ListChannelOwners`
- **Slack udah full per-user** — `NewWithOwner` (slack.go:155), `NewConfigSourceKeyed` (source.go:25), `setup.Slack()` loop owners + `AddKeyed` (setup.go:74-104).
- **UI handler udah per-user** — `internal/tools/agents/channels_handler.go`: `currentUserIDForChannel` (L43), `loadChannelRowsForUser` (L338), `makeChannelSaveHandler` (L305) semua udah pakai `userID`. Yang kurang cuma **lokasinya belum di Profile**.
- **Registry** udah punya `AddKeyed` / `HasKey` / `ChannelByKey` / `RemoveKeyed` / `HasAnyKeyed`.

Artinya: kerjaan inti = **angkat Telegram & REST ke pola Slack**, + **pindahin UI ke Profile**, + **buang jalur NULL/global**.

---

## Gap yang harus ditutup (per channel)

| Hal | Slack | Telegram | REST |
|---|---|---|---|
| `NewWithOwner()` + field `ownerUserID` | ✅ | ❌ **T1** | ❌ **T2** |
| `NewConfigSourceKeyed()` | ✅ | ❌ **T1** | ❌ **T2** |
| `setup.*()` loop owners + `AddKeyed` | ✅ | ❌ **T3** | ❌ **T3** |
| `syncChannelInstance` case | ✅ | ❌ **T4** | ❌ **T4** |
| `Load*ForUser` helper | ✅ pakai | ✅ ada, nganggur | ✅ ada, nganggur |

**REST catatan khusus:** REST = 1 endpoint HTTP global (`/integrations/rest/.../chat/completions`), stateless, auth via PAT per-request (`Authenticator.Authenticate` → `userID`, rest.go:42-46). "Keyed per-user" di REST artinya: endpoint tetap satu, tapi setelah auth dapat `userID`, channel **load config user itu** (`LoadRestForUser`) dan pakai instance/handler yang ter-key ke user tsb. Bukan bikin endpoint baru per user.

---

## File Structure

- `internal/agents/channels/telegram/telegram.go` (modify) — `NewWithOwner`, field `ownerUserID`. [T1]
- `internal/agents/channels/telegram/source.go` (modify/create) — `NewConfigSourceKeyed`. [T1]
- `internal/agents/channels/rest/rest.go` (modify) — `NewWithOwner`, field `ownerUserID`, route-by-PAT→keyed config. [T2]
- `internal/agents/channels/rest/source.go` (modify/create) — `NewConfigSourceKeyed`. [T2]
- `internal/agents/channels/setup/setup.go` (modify) — `Telegram()` & `Rest()` loop owners + `AddKeyed`. [T3]
- `internal/tools/agents/channels_handler.go` (modify) — `syncChannelInstance` add telegram+rest cases. [T4]
- `internal/pkg/ui/profile_layout.templ` (modify) — add `ProfileTabChannels` + tab link. [T6]
- `internal/login/view/` atau `internal/tools/agents/view/` (modify) — render channel form di dalam `ProfileLayout`. [T6]
- Migration (boot) — `user_id = NULL` rows → owner user. [T8]
- Tests: per-channel `*_test.go` + isolation test. [T9]

---

## Task 1: Telegram per-user (mirror Slack)

**Files:** `telegram/telegram.go`, `telegram/source.go`

- [ ] `NewWithOwner(cfg, ownerUserID)` → set `ch.ownerUserID = ownerUserID`, panggil `New(cfg)`.
- [ ] field `ownerUserID string` di struct `Channel`.
- [ ] `NewConfigSourceKeyed(store, ch, userID)` pakai `LoadTelegramForUser(userID)` (helper udah ada).
- [ ] owner-stamping: di handler message, map telegram user → wick user, panggil `ownerFn` (lihat slack.go:897-904 sbg referensi).

## Task 2: REST per-user

**Files:** `rest/rest.go`, `rest/source.go`

- [ ] `NewWithOwner` + `ownerUserID` (buat keyed registration & config source).
- [ ] handler: setelah `auth.Authenticate(...)` dapat `userID`, load config via `LoadRestForUser(userID)` (jangan `LoadRest` global).
- [ ] `NewConfigSourceKeyed` pakai `LoadRestForUser`.
- [ ] **keputusan**: config per-user REST isinya apa? (allowed models / default project / enabled). Konfirmasi sebelum implement — ini surface produk, bukan mekanis.

## Task 3: setup composer

**Files:** `setup/setup.go`

- [ ] `Telegram()` & `Rest()`: copy struktur `Slack()` — `ListChannelOwners(type)` → loop → `LoadXForUser(uid)` → `NewWithOwner` → `AddKeyed(instanceKey(type, ownerID), ...)` + `NewConfigSourceKeyed`.

## Task 4: live sync

**Files:** `channels_handler.go`

- [ ] `syncChannelInstance`: tambah `case "telegram"` & `case "rest"` mirror `case "slack"` (L396-425) — load per-user, `HasKey`/`Reload`/`AddKeyed`/`RemoveKeyed`.

## Task 5: owner stamping
- [ ] telegram: chatID → wick user mapping → `ownerFn`.
- [ ] rest: userID udah dari PAT, stamp ke session.

## Task 6: UI ke Profile
- [ ] `ProfileTabChannels` di `profile_layout.templ` + tab link "Channels".
- [ ] render Slack/Telegram/REST form di dalam `ProfileLayout`, scope `currentUserIDForChannel`.
- [ ] UI copy English (label/placeholder/helper), sample pakai nama generik (abc.com)
- [ ] design system: Inter, 8px grid, green/navy token, dark/light.

## Task 7-8: buang jalur global + migrasi
- [ ] hapus fallback `user_id IS NULL` sebagai "App Owner global" di setup path.
- [ ] migrasi boot: row `user_id = NULL` lama → assign ke owner user (atau seed ulang). Idempotent.

## Task 9-10: test + finalize
- [ ] isolation test: user A save token, user B gak keliatan.
- [ ] keyed reload + REST route-by-PAT test.
- [ ] `templ generate`, edit `.templ` source (jangan edit `_templ.go`), `graphify update .`, smoke, kill :8080.

---

## Risk / open questions

1. **REST config per-user isinya apa** — perlu keputusan produk (T2).
2. **Migrasi NULL row** — kalau ada deploy existing yang udah pakai owner-global Slack, harus dipetakan ke user owner; jangan sampai config ilang.
3. **Telegram long-poll per-user** — tiap user = satu koneksi long-poll (beda bot token). Pastikan gak ada batasan jumlah koneksi.
4. **"Owner" sbg user** — kalau App Owner gak lagi punya row NULL, pastikan owner tetap bisa punya channel via row user_id = <owner id>.
