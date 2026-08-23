# Per-user MCP identity (semua provider)

Hari ini setiap spawn agent dari web memakai satu secret per-boot
(`mcpInternalToken`), yang di middleware MCP dipetakan ke principal **admin
sintetis** `wick-agent-internal`. Akibatnya user A dan user B yang chat lewat
web tidak bisa dibedakan oleh MCP server, filter tag tidak pernah jalan, dan
tujuan "hanya orang yang dikasih akses connector yang dapat MCP-nya" tidak
tercapai di jalur chat normal.

Dokumen ini menutup itu dengan **memakai ulang `ScopedTokens`** yang sudah
dipakai sub-agent, bukan bikin mesin identitas baru.

---

## TODO

### Fase 1 — token per-user (claude, jalur web) ✅ SELESAI

- [x] `stripAdmin` di `ScopedTokens`: `IssueFor(userID, tagIDs, stripAdmin)`.
      `Issue` lama = wrapper `stripAdmin=true` (sub-agent tak berubah).
      `LookupGrant` mengembalikan flag; middleware hanya demote kalau true.
- [x] `ClaudeFactory.SessionMCPToken func(sessionID) (string, bool)` +
      `mcpTokenFor()`; spawner claude memakai `f.mcpTokenFor(opt.SessionID)`.
- [x] Fallback owner-less → `internalToken` (termasuk kasus minter balik
      token kosong — tidak boleh mengosongkan kredensial).
- [x] Wiring di server: mint dari `sess.Meta.UserID` + tag user, `stripAdmin=false`.
- [x] **Revoke saat proses mati** — `onAgentExit` revoke token per-session lewat
      `PoolConfig.RevokeMCPToken`. Hanya token per-session; token internal
      per-boot tidak pernah dilaporkan ke entry jadi tak bisa ke-revoke.

### Fase 2 — wick provider (in-process) ✅ SELESAI

- [x] `AgentIdentity{User, TagIDs}` + `AgentToolDescriptorsAs` /
      `CallAgentToolAs`; zero value = admin sintetis (perilaku lama).
      `dispatchTool` sekarang menerima `tagIDs`, bukan `nil` hardcode.
- [x] Owner diresolve host-side dari SessionID (`agentToolIdentity`), bukan
      lewat field baru di `ToolScope` — field itu cuma bisa berbeda pendapat
      dengan session asalnya.
- [x] Fallback ke admin sintetis kalau session tanpa owner.

### Fase 3 — codex & gemini: GAP, sengaja tidak diwire

- [x] Dikonfirmasi **nol MCP wiring** di dua-duanya; ditambah test penjaga
      (`TestCodexHasNoMCPWiring`, `TestGeminiHasNoMCPWiring`) yang **gagal**
      begitu ada yang menambah MCP, sebagai pengingat bikin per-user sejak awal.
- [x] codex-cli 0.133.0 **mendukung** MCP HTTP + bearer dari env var
      (`codex mcp add --url ... --bearer-token-env-var`, atau
      `-c mcp_servers.<n>.{url,bearer_token_env_var}`). Jadi per-user nanti =
      mint token per-session lalu export sebagai env var itu.
- [ ] Wiring aktualnya belum dikerjakan — di luar scope perubahan ini.

### Fase 4 — yang bergantung admin sintetis ✅ SELESAI

- [x] `schedule_message.go` **tidak perlu diubah.** `scheduleScope` bertumpu pada
      `CanSeeAllSessions()/IsAdmin()`, bukan pada id principal sintetis — jadi
      admin yang chat (token `stripAdmin=false`) tetap bisa enumerasi
      lintas-owner, dan fallback sintetis juga. Dipagari test
      `TestScheduleScope_PerUserIdentity` (4 kasus). Kekhawatiran regresi senyap
      **tidak terbukti**.
- [x] Tool admin-only (`skills`, ops `wickmanager`) sekarang mengikuti role
      sebenarnya. **Ini perubahan perilaku yang kelihatan**: user non-admin
      kehilangan akses yang sebelumnya kebagian gratis karena semua spawn admin.

### Fase 5 — respawn saat pemanggil ganti ✅ SELESAI

Bukan lewat `SendMode` — lewat recycle eksplisit, karena kredensial MCP ada di
argv proses dan tak bisa ditukar in-place.

- [x] `runEntry.callerUserID` + `Pool.callerChanged`: pesan `role=="user"` dari
      user berbeda → `Kill` lalu spawn ulang dengan kredensial pemanggil baru.
- [x] `PoolConfig.CallerUserID` (resolver dari ctx, pool tak perlu impor auth) +
      `RevokeMCPToken`.
- [x] Gagal kill → **tolak pesan** (`return error`), jangan kirim ke proses lama;
      kalau diteruskan, turn user baru jalan dengan identitas user lama.
- [x] Config: `RespawnOnCallerChange` di `agents/config/general.go`, group baru
      **Session Identity**, dibaca live (`respawn_on_caller_change`).
      **Default OFF** — recycle mengorbankan konteks in-memory proses; salah
      trade untuk session single-user yang jadi mayoritas.
- [x] Tidak recycle kalau: pemanggil baru tak dikenal, spawn tanpa owner
      (cron/system/legacy), atau resolver belum diwire.

### Fase 6 — `wick_me` (user info) ✅ SELESAI

- [x] Tool `wick_me`: `user_id`, `name`, `email`, `role`, `is_admin`,
      `is_owner`, `approved`, `filter_tag_ids`, `is_system`, `is_local_cli`,
      `identity_source`. Jawaban diresolve **server-side** dari kredensial —
      bukan dari klaim caller.
- [x] `filter_tag_ids` ikut dikembalikan supaya agent bisa menjelaskan **kenapa**
      sebuah connector tak muncul di `wick_list`, bukan ngotot harusnya ada.
- [x] Jalan di dua transport: HTTP (`dispatchTool`) dan in-process wick provider
      (`CallAgentToolAs` → dispatch yang sama).
- [x] **stdio (`wick mcp serve`) tetap admin — by design, tidak diubah.** Yang
      ditambah cuma dua field informatif (`is_local_cli`,
      `identity_source=local-cli-fallback`) supaya `wick_me` tidak mengaku
      "human terverifikasi" padahal itu fallback mesin. Akses & perilaku sama.
      Alasan stdio harus admin: token `wick_enc_` di-key
      `HKDF(masterKey, salt=user.ID)`; id sintetis → token tak bisa didekripsi.

---

## Yang berubah (kode)

| File | Perubahan |
|---|---|
| `internal/mcp/scopedtoken.go` | `stripAdmin` per-grant; `IssueFor` + `LookupGrant` |
| `internal/mcp/auth.go` | demote role hanya kalau `stripAdmin` |
| `internal/mcp/agent_tools.go` | `AgentIdentity`; `*As` varian; `tagIDs` diteruskan ke dispatch |
| `internal/agents/pool/factory.go` | `SessionMCPToken` + `mcpTokenFor()`; spawner claude per-session |
| `internal/pkg/api/server.go` | mint token per-session; wick provider pakai identitas owner |
| `internal/pkg/api/agent_tool_identity.go` | **baru** — resolve owner → `AgentIdentity` |
| `internal/mcp/handlers/me.go` | **baru** — tool `wick_me` |
| `internal/mcp/handlers/tools.go` + `internal/mcp/handler.go` | descriptor + dispatch `wick_me` |
| `internal/agents/pool/pool.go` | `callerUserID`/`mcpToken` di entry; `callerChanged`; revoke on exit; `CallerUserID`/`RevokeMCPToken`/`RespawnOnCallerChange` |
| `internal/agents/config/general.go` | config `respawn_on_caller_change` (group Session Identity) |
| `internal/pkg/api/server_mcp.go` | tandai principal stdio (informatif saja) |

Test baru: `internal/mcp/agent_identity_internal_test.go`,
`internal/agents/pool/peruser_token_internal_test.go`, gap-test codex + gemini,
plus `peruser_feasibility_internal_test.go` & `peruser_argv_internal_test.go`.

Semua jalur baru **backward-compatible**: minter nil / owner tak ada → persis
perilaku lama.

## Hasil verifikasi (dijalankan, bukan dugaan)

### Unit test `internal/mcp/peruser_feasibility_internal_test.go` — 6/6 PASS

| Test | Yang dibuktikan |
|---|---|
| `PerUserTokenSwapsIdentity` | **token beda → user beda ke-resolve**, tag ikut token |
| `ScopedStripsAdminToday` | scoped token memaksa `RoleUser` → anchor untuk `stripAdmin` |
| `InternalTokenIsAdminForEveryone` | reproduksi masalah: semua spawn → satu admin, tag kosong |
| `UnauthUserIDHeaderIsSpoofable` | header user-id tanpa token → **401**; header tak bisa override token |
| `RevokeAndExpiryCutAccess` | `Revoke` memutus akses → layak untuk session reap |
| `UnknownAndUnapprovedRejected` | token stale/forged ditolak |

### Unit test `internal/agents/provider/claude/peruser_argv_internal_test.go` — 3/3 PASS

- `ArgvCarriesPerUserToken` — token mendarat di `Authorization` dalam `--mcp-config`
- `StrictFlagIsNotACredential` — `WICK_STRICT_MCP` cuma toggle isolasi, **bukan** kredensial
- `NoTokenNoMCP` — token kosong → nol argv MCP; tidak ada jalur MCP tanpa auth

### E2E CLI `claude` asli (v2.1.233)

Fake MCP server mengembalikan tool berbeda per identitas; dua spawn `claude -p`
dengan argv persis seperti wick:

```
SNIFF method=tools/list auth="Bearer wick_sub_tokenA" session="sess-A"
SNIFF method=tools/list auth="Bearer wick_sub_tokenB" session="sess-B"
```

- spawn A → hanya `mcp__wick__whoami_user_a`
- spawn B → hanya `mcp__wick__whoami_user_b`

**Berhasil**: CLI mengirim token per-spawn apa adanya, visibility tool ter-scope
per user.

---

## Kenapa token, bukan alternatif lain

**`WICK_STRICT_MCP` sebagai generator password — tidak bisa.** Satu nilai per
**proses server**, sama untuk semua user; kredensial harus beda per-spawn.
Isinya cuma flag on/off ([spawn.go:137](../../../agents/provider/claude/spawn.go#L137)).
Dites: `StrictFlagIsNotACredential`.

**Tanpa auth karena loopback — jangan.** Loopback membatasi *mesin*, bukan
*proses*. Proses lokal apa pun bisa `curl -H "X-Wick-User-Id: <admin>"` dan jadi
admin tanpa privilege khusus — justru **menghapus** proteksi yang sekarang ada.
User id yang bisa dipalsukan bukan access control, cuma saran. Dites:
`UnauthUserIDHeaderIsSpoofable`.

**JWT — tidak perlu.** Issuer dan verifier **proses yang sama** lewat loopback,
jadi keuntungan stateless JWT tak berlaku; dan stateless = tak bisa revoke,
padahal revoke dibutuhkan saat session reap. `ScopedTokens` sudah opaque
32-byte + TTL + bawa userID/tagIDs + revoke.

---

## Catatan penting

**`--allowedTools mcp__wick` bukan security boundary** — komentarnya sendiri
bilang begitu ([mcp_config.go:36](../../../agents/provider/claude/mcp_config.go#L36)).
Penegakan harus server-side; strict-MCP dan allowedTools tidak menahan apa pun.

**`strict_mcp` per-profile masih inert** — tersimpan tapi tidak dibaca jalur
spawn. Lihat [subagent-lock-and-mcp-config](../subagent-lock-and-mcp-config/design.md).

**Akses lintas-user itu nyata** — `allowSession`
([handler.go:673-685](../../../tools/agents/handler.go#L673-L685)): session
ber-project bisa diakses siapa pun yang punya akses project itu, bukan cuma
pemiliknya. Jadi Fase 5 punya alasan.

**Codex/gemini nol MCP** — claude-only hari ini. Fase 3 = wiring dari nol.

**`SetSessionOwnerUserResolver`** ([server.go:1286](../../../pkg/api/server.go#L1286))
adalah tambalan untuk masalah yang sama (data table per-owner); kandidat
disederhanakan setelah Fase 1-2.
