# Slack "Sent using @bot" — multi-instance resolution tiers

> With per-user Slack instances (each user's own bot), "Sent using @xxx" must show the bot belonging to the **session owner's** Slack instance — not a process-global last-writer-wins value. This design covers both directions: agent replies in a Slack thread (Tier 2) and agent sends to Slack via the connector from a non-Slack session (Tier 1).

## TODO (urut kerjakan)

- [ ] **T1** — Owner-stamping: pastikan tiap session dari channel Slack nge-record owner wick-user + originating channel jelas. Slack channel udah panggil `ownerFn`; tambah fallback set kalau belum ke-set + format per-channel.
- [ ] **T2** — Tier 2 (reply di thread): VERIFY footer udah bener per-instance (`s.botUserID` instance-local). Kemungkinan udah benar — tinggal test, jangan ubah.
- [ ] **T3** — Tier 1 (connector send): ganti global `botUserID` → resolve bot dari **session owner**. Footer pakai bot instance milik owner. Kalau owner gak punya Slack instance → fallback app name.
- [ ] **T4** — Buang/pensiunkan global `slackconnector.SetBotUserID/BotUserID` last-writer-wins (atau jadiin per-owner map).
- [ ] **T5** — Test: 2 instance Slack beda bot, session owner A → footer @botA, owner B → @botB.

---

## Peta sekarang (hasil trace)

Dua jalur "Sent using @":

| Jalur | Kapan | Footer dibangun di | Sumber bot ID | Multi-instance? |
|---|---|---|---|---|
| **Thread reply** | Agent bales pesan yg masuk dari Slack | `signedContextBlock` ([slack/send.go:286](../../../agents/channels/slack/send.go#L286)) | `s.botUserID` **instance-local** | ✅ udah bener |
| **Connector send_message** | Agent kirim KE Slack via connector (session non-Slack / proaktif) | `signedFooterBlock` ([connectors/slack/service.go:505](../../../connectors/slack/service.go#L505)) | **global** `botUserID` atomic ([service.go:480](../../../connectors/slack/service.go#L480)) | ❌ last-writer-wins |

### Kenapa thread-reply udah bener
- Pesan masuk lewat socket/webhook instance tertentu → `handleMessage` instance `s` ([slack.go:793](../../../agents/channels/slack/slack.go#L793)).
- `DispatchAgentEvent` fan-out ke semua instance, tapi tiap instance filter `s.turns[sessionKey]` → cuma instance yang nerima yg react ([slack.go:948](../../../agents/channels/slack/slack.go#L948), `t==nil`→return).
- Jadi `s.botUserID` = bot instance yg nerima = bot owner. **Benar tanpa ubah apa-apa** — cuma butuh test (T2).

### Kenapa connector-send salah
- `slackconnector.SetBotUserID` ([service.go:485](../../../connectors/slack/service.go#L485)) dipanggil tiap instance `applyConfig` → **satu nilai global**, instance terakhir connect/reload menang ([slack.go:261,445](../../../agents/channels/slack/slack.go#L261)).
- `send_message` connector → `signedFooterBlock` baca global itu → footer bisa nunjuk bot user lain.
- Connector gak tau session owner siapa → gak bisa pilih instance yg bener.

---

## Data yang udah ada (dipakai, jangan bikin baru)

- **Session owner**: `session.Meta.UserID` (wick user yg create) + `Meta.Origin` ("slack"/"telegram"/"rest"/"ui") + `Meta.ChannelID` ([session.go:54-92](../../../agents/session/session.go#L54)).
- **Owner stamping**: `pool.EnsureSessionOwner` ([pool.go:985](../../../agents/pool/pool.go#L985)) — set `Meta.UserID` kalau kosong. Slack manggil via `ownerFn` ([slack.go:899-906](../../../agents/channels/slack/slack.go#L899)).
- **Slack user → wick user**: `wickUserIDFn` ([server.go:1108](../../../pkg/api/server.go#L1108)) scan connector account rows.
- **Instance per owner**: registry keyed `slack:<userID>` / `slack:__owner__`. `ChannelByKey` bisa ambil instance.
- **Bot ID per instance**: `slack.Channel.botUserID` (instance-local, di-set `applyConfig`).

---

## Desain Tier 1 (connector send → resolve bot dari token instance) ✅ REVISI

**Temuan kunci**: connector `Ctx` GAK bawa session owner (cuma `InstanceID()`, `Cfg`, `Input` — [pkg/connector/ctx.go](../../../../pkg/connector/ctx.go)). TAPI connector slack instance **punya `BotToken` sendiri** di `c.Cfg("bot_token")` — "one row = one Slack identity" ([connectors/slack/connector.go:28-35](../../../connectors/slack/connector.go#L28)). Connector slack udah per-user by design.

Jadi gak perlu session owner sama sekali. Footer cukup resolve bot dari **token instance connector itu sendiri**:

1. `signedFooterBlock` jadi method yang terima `c *connector.Ctx` (bukan global).
2. Ambil `c.Cfg("bot_token")` → resolve bot user ID via `auth.test` (cache per-token, jangan call tiap send).
3. Footer `Sent using <@botID>`. Token kosong / auth gagal → fallback `Sent using *<appname>*`.

Ini ngilangin global last-writer-wins total — tiap connector instance pakai bot-nya sendiri, otomatis bener buat multi-user. **Buang** `SetBotUserID`/`BotUserID` global (T4) — gak ada yg butuh lagi selain footer (grep confirm: cuma `signedFooterBlock` + di-set dari slack channel applyConfig).

Cache: map `botToken → botUserID` (atau pakai instance config yg udah ada). auth.test sekali per token, simpan.

---

## Desain owner-stamping (T1)

Sekarang `ownerFn` di-stamp pas Slack user ke-map ke wick user, atau fallback `s.ownerUserID`. Gap:
- Kalau Slack user gak ke-map DAN `s.ownerUserID` kosong (App Owner instance, anon trigger) → owner kosong → Tier 1 gak bisa resolve.

Tambahan:
- Pastikan instance per-user (`s.ownerUserID != ""`) selalu stamp `s.ownerUserID` sebagai owner — instance ini emang punya user jelas.
- Format per-channel extensible: pertimbangkan `Meta.Origin` + simpan instance key (mis. `Meta.ChannelID = "slack:<owner>"`) biar kedepan tau persis instance mana yg create. (Saat ini `ChannelID` ada tapi belum diisi konsisten.)

---

## Non-goals (sekarang)
- Refactor `/integrations/slack/send` jadi routing per-instance — itu proxy DM, bukan jalur "Sent using @" utama. Tier 1 lewat connector, bukan proxy ini. (Catat sebagai follow-up kalau proxy DM juga perlu multi-instance.)
- Pindah UI ke Profile (udah diputusin: halaman channels tetap).

## Risk
- Connector mungkin gak bawa session owner di ctx → kalau gitu, butuh nambah owner ke connector ctx (lebih invasif). Cek di T3 langkah 0 sebelum komit ke opsi.
- Global `BotUserID` mungkin dipakai tempat lain selain footer — grep sebelum hapus.
