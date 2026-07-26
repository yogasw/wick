# Plan: Post-update retest fixes (schedule list, codex skills, playwright status)

Retest report (`retest-postupdate-summary.html`, server commit `558d919`, build 2026-07-24) mendata beberapa FAIL/QUIRK setelah long-tool + job API masuk. Doc ini **memisahkan** mana yang bug nyata vs artefak build lama, lalu rencanakan fix untuk yang nyata. Root cause tiap item sudah diverifikasi via pembacaan kode + repro test (bukan tebakan).

---

## 0. TODO (urut prioritas)

- [x] **F1** — ✅ Schedule list `[]`: `scheduleScope` sekarang `|| user.IsAdmin()` ([schedule_message.go:286](../../mcp/handlers/schedule_message.go#L286)), match gate create/cancel. Test `TestScheduleScope` di-update (admin → all owners).
- [x] **F2** — ✅ Playwright `browser_status`: `connector.Op` → `connector.OpConfigOnly` ([playwright_browser/connector.go:340](../../../plugins/connector/playwright_browser/connector.go#L340)) — hilang dari MCP surface, widget manager tetap jalan. Guard `TestBrowserStatusConfigOnly`.
- [x] **F3** — ✅ Codex skills: `codex/skilldir.go` baru + `--add-dir ~/.codex/skills` di spawn (seam `homeDir` buat test deterministik). Test `TestSkillAddDirArgs` + `TestSpawnerArgv_SkillsDir`.
- [x] **V1** — ✅ Verified `job_log`/`[job-done]`/`$var` benar di kode terkini (repro + E2E). Report dari build lama.
- [ ] **Build server dari kode terkini** biar retest berikutnya nangkep fix (report dipakai build `558d919` TANPA perubahan uncommitted)

---

## 1. Triage — bug nyata vs artefak build lama

| Item report | Verdict | Bukti |
|---|---|---|
| **Schedule list `[]`** | ✅ BUG NYATA | scope mismatch, confirmed by code read (§2 F1) |
| **Playwright browser_status HTML** | ✅ BUG NYATA | op salah kategori, confirmed (§2 F2) |
| **Skills folder sync (codex)** | ✅ BUG NYATA | codex spawn nggak grant skills dir, confirmed (§2 F3) |
| **job_log `(no output yet)`** | ⚠️ BUKAN bug kode terkini | Repro `TestReproJobLog`: output ter-capture (`tick 0/1/2`, `ALLDONE`) |
| **`[job-done]` inject UNVERIFIED** | ⚠️ BUKAN bug kode terkini | E2E `TestE2EJobInject`: `[job-done id=… ok=true exit=0]` nyampe `p.msgs`, race-clean |
| **Job `$var` expansion (`$i` kosong)** | ⚠️ BUKAN bug kode terkini | Repro `TestRepro2` CASE_A: `while [ $i -lt 10 ]` → `iter 0..9` benar, exit 0 |

**Kenapa report nunjukin bug yang nggak ada di kode terkini:** report di-generate dari server yang di-build dari commit `558d919` **tanpa** perubahan job API terbaru (yang masih uncommitted di working tree). Job runner yang salah `$var`/`job_log` itu implementasi lama/berbeda. Kode terkini pakai `safeexec.Command(bin, "-c", cmdline)` (satu argumen -c, no re-quoting) + `jobWriter` yang nulis combined output ke buffer ber-lock — dua-duanya sudah kebukti benar.

**Catatan `$var`:** kode terkini benar KARENA command dikirim utuh sebagai satu `-c` string ke bash (sama seperti shell tool foreground yang lolos semua test heredoc/quote). Bug `$i` kosong di report = tanda build lama masih nge-mangle command (re-quote/split) — persis regresi yang shell provider spec larang.

---

## 2. Root cause + fix (yang nyata)

### F1 — Schedule list `[]` ← scope asimetris
**Root:** MCP call dari in-process wick provider pakai synthetic user `internalSystemUser()` = `RoleAdmin` tapi **`IsOwner=false`** ([auth.go:69](../../mcp/auth.go#L69)).
- create/cancel gate lewat `canManageSession` = `CanSeeAllSessions() || IsAdmin()` → **lolos** (IsAdmin true). create stamp `OwnerUserID = sess.Meta.UserID` (owner sesi asli) di [schedule_message.go:116](../../mcp/handlers/schedule_message.go#L116).
- list gate lewat `scheduleScope` = **`CanSeeAllSessions()` only** ([schedule_message.go:286](../../mcp/handlers/schedule_message.go#L286)) = `IsOwner` only ([user.go:42](../../entity/user.go#L42)) → internal user **gagal**, query di-scope ke `owner_user_id="wick-agent-internal"` yang nggak pernah match row manapun → `[]`.

**Fix (1 baris):** samakan scope list dengan gate lain.
```go
// schedule_message.go:287
if user == nil || user.CanSeeAllSessions() || user.IsAdmin() {
    return "", true
}
```
**Bukan:** ubah `CanSeeAllSessions()` (broad, nyentuh semua visibility app-wide) atau stamp `OwnerUserID=user.ID` di create (rusakin dashboard + ownership). Fix lokal di `scheduleScope` = intent "siapa yang bisa manage sesi, bisa list schedule-nya".

### F2 — Playwright `browser_status` balikin HTML ← op salah kategori
**Root:** `browser_status` itu **renderer widget config-form** (`html=browser_status` di [connector.go:55](../../../plugins/connector/playwright_browser/connector.go#L55)), sengaja balikin `{"html": "<div>…"}` ([maintenance.go:37](../../../plugins/connector/playwright_browser/maintenance.go#L37)). Tapi di-register pakai `connector.Op` ([connector.go:340](../../../plugins/connector/playwright_browser/connector.go#L340)) → `ConfigOnly=false` → **ke-expose ke MCP surface** sebagai tool biasa. Agent manggil → dapat HTML.
Mekanisme yang bener udah ada: `connector.OpConfigOnly` ([connector.go:199](../../../pkg/connector/connector.go#L199)) yang set `ConfigOnly=true` → disembunyiin dari wick_list/search/get, tapi tetap jalan dari manager UI. `AdminOnly` seeding **tidak** cukup (nggak sembunyiin dari MCP list).

**Fix (1 kata):** `connector.Op(` → `connector.OpConfigOnly(` di registrasi browser_status. Handler + HTML builder nggak berubah (HTML benar untuk widget). Rebuild plugin.

### F3 — Codex nggak bisa baca skills ← spawn nggak grant dir
**Root:** `skillsync.Sync()` mirror skill ke semua provider dir termasuk `~/.codex/skills` ([sync.go:103](../../agents/skillsync/sync.go#L103)) — **file-nya ada**. Tapi claude spawn `--add-dir ~/.claude/skills` ([claude/spawn.go:141](../../agents/provider/claude/spawn.go#L141) via [claude/skilldir.go](../../agents/provider/claude/skilldir.go)), sedangkan **codex spawn nggak ada equivalent** ([codex/spawn.go:118-136](../../agents/provider/codex/spawn.go#L118-L136)). Sandbox `workspace-write`/`read-only` blok akses di luar cwd → codex nggak bisa baca `~/.codex/skills` walau file-nya di-sync. Flag `--add-dir` didukung codex ([codex/catalog.go:39](../../agents/provider/codex/catalog.go#L39)).

**Fix:**
1. Tambah `internal/agents/provider/codex/skilldir.go` — mirror claude's, `.claude`→`.codex`, return `["--add-dir", <home>/.codex/skills]` kalau dir ada.
2. Di `codex/spawn.go` setelah `--sandbox` append (~line 127): `if home, err := os.UserHomeDir(); err == nil { args = append(args, skillAddDirArgs(home, dirExists)...) }`.
3. Tambah `spawn_test.go` case assert `--add-dir …/.codex/skills` muncul (codex argv test hard-assert exact argv, bakal break tanpa update).

**Wick provider TIDAK kena** — baca skills lewat `skillsync.ListSkills()` ke system prompt + `skill` tool, nggak pakai `--add-dir`.

**Soal hint "app_name/skills":** lokasi kanonik = per-provider home dir (`~/.codex/skills`), BUKAN `<app_config>/<app_name>/skills`. `internal/appname` govern `~/.<app>/` buat DB/logs/gate; skills sengaja di home dir vendor CLI biar CLI native discover. Yang user lihat = folder ada di codex dir (sync jalan) tapi proses codex nggak di-grant akses saat spawn.

---

## 3. Out of scope / tunda
- Job runner non-shell (wick_execute/playwright) — extension point, belum diminta.
- Retensi log job ke disk (sekarang in-memory tail-capped) — P4 hardening.
- UI job list, metrics — P4.

---

## 4. Verifikasi (test yang membuktikan V1)
Repro (dijalankan, semua PASS, kode terkini):
- `TestReproJobLog` — loop `$i` + progress echo → output ter-capture, inject fired.
- `TestRepro2` CASE_A — `while [ $i -lt 10 ]` → `iter 0..9` benar exit 0; CASE_B — script error → output capture.
- `TestE2EJobInject` — job selesai → `[job-done id=… ok=true exit=0]` nyampe `p.msgs` (race-clean).

---

## 5. Definition of Done
- [ ] F1: `schedule list` balikin schedule yang barusan dibuat (owner internal admin bisa list)
- [ ] F2: `browser_status` hilang dari wick_list/search/get; widget manager tetap render
- [ ] F3: codex argv punya `--add-dir …/.codex/skills`; skill claude-authored kebaca di codex
- [ ] Server di-rebuild dari kode terkini; retest ulang report → F1/F2/F3 hijau, V1 tetap hijau
- [ ] `graphify update .` setelah edit
