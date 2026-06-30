# Slack "Sent using @bot" — session-owner resolution

> Footer "Sent using @xxx" harus selalu nunjuk bot milik **owner sesi** — bukan bot connector yang kebetulan dipakai kirim, dan bukan nama user/SSO si pengirim. Prinsip: **pilih connector apapun buat kirim, hasil "Sent using" tetap sama** = bot owner sesi. Doc ini nyatet desain final yang udah diimplementasi (status: DONE).

## TODO

- [x] **T1** — Resolver session → bot owner: framework resolve bot dari instance channel slack yang nge-create sesi, di-stamp ke connector Ctx.
- [x] **T2** — Tier 2 (reply di thread): footer pakai `s.botUserID` instance-local. Udah bener dari awal — gak diubah.
- [x] **T3** — Tier 1 (connector send): footer pakai `c.OwnerBotID()` (owner sesi), fallback app name buat sesi non-channel.
- [x] **T4** — `update_message` ikut re-append footer (chat.update REPLACES block set).
- [x] **T5** — Test: `footer_test.go` (OwnerBot, NoOwner→appname, EmptyOwner→appname).
- [x] **T6** — **session_id nyampe ke connector** (2 jalur, dua-duanya opsional):
  - Input `session_id` di `send_message`/`update_message` — LLM isi dari konteks sesi (utama).
  - Header `X-Wick-Session-Id` auto-inject pas agent spawn — fallback kalau LLM gak isi.
  - Prioritas resolve di `Service.Execute`: `input.session_id` > `ExecuteParams.SessionID` (header). Kosong dua-duanya → app name.
  - TANPA ini resolver gak pernah jalan → footer selalu app name.
- [x] **T7** — Buang `__owner__` dari session_id App Owner: prefix jadi `slack-<ts>` (bukan `slack-__owner__-<ts>`). Registry key tetap `slack:__owner__` (internal). `setup.SessionPrefix` di-export + dipakai hot-reload path biar konsisten.

> **Akar masalah sebenarnya (ditemukan saat verify live)**: footer fix (T1-T5) bener, tapi `ExecuteParams.SessionID` selalu kosong karena (a) MCP config cuma kirim header `Authorization`, gak ada session identity; (b) LLM gak pass `session_id` argumen ke `send_message`. Akibatnya `OwnerBotID` selalu kosong → footer app name. T6 nutup gap ini lewat header per-spawn — server tau sesi tanpa ngarep LLM.

---

## Dua jalur "Sent using @"

| Jalur | Kapan | Footer dibangun di | Sumber bot ID | Status |
|---|---|---|---|---|
| **Tier 2 — Thread reply** | Agent bales pesan masuk dari Slack | `signedContextBlock` ([slack/send.go](../../../agents/channels/slack/send.go)) | `s.botUserID` instance-local | ✅ bener dari awal |
| **Tier 1 — Connector send/update** | Agent kirim KE Slack via connector `send_message`/`update_message` | `signedFooterBlock(c)` ([connectors/slack/service.go](../../../connectors/slack/service.go)) | `c.OwnerBotID()` → fallback app name | ✅ done |

### Kenapa Tier 2 udah bener
- Pesan masuk lewat instance tertentu → `s.botUserID` = bot instance penerima = bot owner.
- `DispatchAgentEvent` fan-out, tapi tiap instance filter `s.turns[sessionKey]` → cuma instance yang nerima yang react. `s.botUserID` dijamin bener tanpa ubah apa-apa.

### Bug yang diperbaiki di Tier 1
1. **Footer nunjuk @user** (mis. @Yoga) pas `auth_mode=user_token` — footer dulu resolve via `pickToken` yang ngikut auth_mode → token xoxp → `auth.test` balikin user manusia.
2. **Footer gak konsisten** antar connector — tiap connector punya bot beda, footer ikut bot pengirim, bukan owner sesi.

Dua-duanya hilang karena footer sekarang resolve dari **owner sesi**, bukan token connector.

---

## Desain final (Tier 1)

**Temuan kunci**: session_id slack formatnya `slack-<owner>-<threadTS>` (`sessionPrefix + threadTS`, [setup.go:86](../../../agents/channels/setup/setup.go#L86)). Dari string itu langsung ketauan: ini sesi slack + instance mana yang create. Bot-nya udah ke-resolve di `s.botUserID` pas channel connect — gak perlu auth.test baru, gak ada DB, gak ada stale.

**Alur:**

```
send_message / update_message (connector apapun)
  → MCP bawa session_id ke ExecuteParams.SessionID
  → Service.sessionOwnerBot(sessionID) resolve bot owner
       (iterate channelReg.Channels() → *slack.Channel.OwnsSession(sid) → BotUserID())
  → di-stamp ke Ctx via cctx.SetSession(sid, ownerBot)
  → signedFooterBlock(c): c.OwnerBotID() dulu, fallback botUserIDForToken(c), fallback app name
```

**Komponen (file:line saat ditulis):**

1. **Accessor bot owner** — `slack.Channel.BotUserID()` + `OwnsSession(sessionID)` ([slack.go](../../../agents/channels/slack/slack.go)). Prefix-match (bukan parse) karena owner = UUID yang ngandung `-`.
2. **Ctx bawa session** — `connector.Ctx.SetSession(sessionID, ownerBotID)` + `SessionID()` + `OwnerBotID()` ([pkg/connector/ctx.go](../../../../pkg/connector/ctx.go)).
3. **ExecuteParams.SessionID** + resolve sekali di `Service.Execute` ([connectors/service.go](../../../connectors/service.go)). Resolver di-inject via `SetSessionOwnerBotResolver`.
4. **Wiring** — `server.go` panggil `connectorsSvc.SetSessionOwnerBotResolver(...)` (satu-satunya tempat connector ketemu channel registry → layering aman, gak ada import langsung) ([pkg/api/server.go](../../../pkg/api/server.go)).
5. **Footer resolver-first** — `signedFooterBlock` ([connectors/slack/service.go](../../../connectors/slack/service.go)).

**Fallback chain footer (sengaja simpel):**
1. `c.OwnerBotID()` — bot owner sesi (sesi channel-backed).
2. `*<appname>*` — sesi non-channel (cron/UI/REST) atau owner gak ke-resolve.

> Sengaja **gak** resolve bot dari token connector buat sesi non-slack. Itu nambah auth.test call + cache token + risiko @user (kalau auth_mode=user_token). Buat sesi non-slack, app name udah cukup. Footer logic tinggal `OwnerBotID → app name`, gak ada token resolution sama sekali.

---

## Yang sengaja TIDAK diubah / non-goal
- AI tetap bebas milih connector buat kirim — gak dipaksa. Footer independen dari pilihan itu.
- Tier 2 thread-reply — udah bener, gak disentuh.
- Proxy DM `/integrations/slack/send` — sengaja act-as-user (xoxp), bukan jalur "Sent using @bot". Follow-up kalau perlu multi-instance.
