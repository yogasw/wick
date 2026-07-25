# Plan: Long-running tool calls — foreground long-call + background spawn + async job API

**Konteks user:**
> "ini ngak cuma shell tapi semua tool call"
> "buat kayak claude atau codex yg bisa jalanin perintah yg lama pakai bg"

Mau support kerja **lama** (install, monitoring, scrape berat, batch connector, npm install, dsb) — persis kayak claude/codex: perintah lama jalan tanpa mati, dan yang **>menit** bisa di-*background* lalu di-poll, bukan nge-block turn.

**Server (last read):** commit `8430bd3c` · provider `wick` (in-process engine).
**Scope keputusan:** **full async job API** (bukan foreground-only) — lihat §7 Layer 2.

---

## 0. TODO

- [x] **P0** — ✅ shell `timeout_ms` param + cap naik (default **10m**, clamp **30m**) + **kill-tree** (`pkg/proctree`). Bug Windows "timeout on paper tapi proses jalan terus" ikut ke-fix — dulu bash ke-kill tapi child (`sleep.exe`) tetap hidup & nge-hold pipe. Test: `TestShell_TimeoutMsHonored` (5s→1s), `TestResolveShellTimeout`.
- [x] **P0b** — ✅ Idle-kill wick TIDAK bunuh long foreground tool. Engine emit `tool_use` **sebelum** dispatch ([engine.go:204](../../agents/provider/wick/engine.go#L204)) → parser `ToolUse` → reader `toolInFlight=true` → idle timer paused sepanjang tool jalan. Test bukti: `TestEngineToolUseEmittedBeforeDispatch` (lock ordering).
- [x] **P1** — ✅ Angka batch "~120s" **diverifikasi**: batch default = **3m** ([connectors.go:675](../../mcp/handlers/connectors.go#L675)), honor `timeout_ms`, clamp 5m. Klaim lama "di-cap 120s diam-diam" **tidak terbukti** — ~120014ms itu upstream/Playwright, bukan cap wick. Error-shape: shell timeout kasih pesan work-timeout eksplisit (P0); align paksa lintas-transport (text vs JSON field) sengaja TIDAK dikejar — over-engineering, transport beda.
- [x] **P2** — ✅ Background shell (`run_in_background` + `shell_output` + `shell_kill`) — per-spawn `bgRegistry`, cap `maxBgProcs=16`, output tail-capped, `killAll` on teardown, kill-tree via proctree. Test: `TestBg_*` (8 kasus, race-clean).
- [x] **P3** — ✅ Async job API generik (`job_start`/`job_status`/`job_log`/`job_cancel`) + **on_complete inject turn**. Job manager per-spawn ([job.go](../../agents/provider/wick/job.go)), runner shell ([job_tools.go](../../agents/provider/wick/job_tools.go)), cap `maxJobs=16`. Completion → `injectTurn` push `[job-done id=… ok=… exit=…]` ke `p.msgs` → engine auto turn baru. **Keepalive heartbeat** selama job aktif biar idle-kill nggak reap session sebelum job selesai. Fix bonus: data race laten `sendMsg`↔`closeMsgs` (RWMutex+flag, ganti sync.Once). Test: `TestJob_*` (10) + `TestInjectTurn_*` (3, race-clean). Runner wick_execute/playwright = extension point (belum).
- [ ] **P4** — Prompt guidance (kapan sync vs bg vs job vs schedule) + hardening (quota, retensi log, UI job list, metrics)

---

## 1. Cara claude/codex jalanin perintah lama (referensi desain)

Ternyata BUKAN satu async job model. Ada **dua** mekanisme, dan wick perlu keduanya:

### (A) Foreground long tool call — proses tidak mati selama tool jalan
Waktu tool CLI (claude/codex) jalanin `sleep 300`, prosesnya **tidak** kena idle-kill karena idle-timer di-*pause* selama tool in-flight:
- [agent.go:874-896](../../agents/provider/agent.go#L874-L896): event `ToolUse` → `toolInFlight = true` → `idle.Stop()`. `ToolResult` → restart timer.
- Dites nyata di [real_e2e_longtool_test.go](../../agents/provider/claude/real_e2e_longtool_test.go): `sleep 300`, `sleep 305 & wait`, loop 5m — semua PASS, `ResumeID` tetap valid, session tetap hidup buat turn berikutnya.

Artinya: **satu tool call boleh block selama-lamanya**, asal (1) tool-nya sendiri nggak punya deadline internal, dan (2) idle-timer di-pause. Claude bebas dari deadline internal karena bash tool-nya CLI, bukan Go handler.

### (B) `run_in_background` — spawn, balik `bash_id` cepat, poll
Buat kerja yang mau **paralel** atau **>>menit** (install 20m sambil agent kerja lain):
- `Bash(command, run_in_background: true)` → balik `bash_id` segera, proses jalan detached.
- `BashOutput(bash_id)` → poll stdout/stderr/exit tanpa block.
- `KillShell(bash_id)` → stop.

Ini **beda kelas** dari (A): (A) block turn, (B) nggak. User bilang "pakai bg" → yang dia mau utamanya (B).

### Bukti empiris di environment ini (2026-07-25)
| Cara | Hasil |
|---|---|
| Foreground satu call | ❌ selalu kena cap tool (Bash tool max 10m; wick shell 120s) |
| **Background (`nohup … &` + detach)** | ✅ `sleep.exe` pid survive parent exit, log ke-tulis, poll kapan aja, `sleep 900` selesai normal |

→ Kesimpulan: kerja >beberapa menit = **detach + poll**, bukan foreground yang di-block. Sama persis dengan arah desain wick.

---

## 2. Ground truth — timeout yang benar-benar ada di kode wick

Klaim plan lama ("satu global tool-call layer memotong ~120s") **salah**. Empat mekanisme terpisah:

| Layer | Nilai | Sumber | Honor `timeout_ms`? | Pause saat tool jalan? |
|---|---|---|---|---|
| **`shell` tool (in-process)** | **120s hard** | [tool_shell.go:16](../../agents/provider/wick/tool_shell.go#L16), applied [53](../../agents/provider/wick/tool_shell.go#L53) | ❌ tidak ada param | — (ini deadline internal handler, motong dari DALAM) |
| **pool idle-kill** | **120s** default | [pool.go:320-321](../../agents/pool/pool.go#L320-L321) | ❌ | ✅ paused via `toolInFlight` ([agent.go:878](../../agents/provider/agent.go#L878)) |
| **`wick_execute` batch (per-call)** | default **3m**, clamp **5m** | [connectors.go:533-534](../../mcp/handlers/connectors.go#L533-L534) | ✅ | n/a (HTTP path, bukan engine tool) |
| **SSE per-request (outer)** | **5m** | [sse.go:41](../../mcp/sse.go#L41) | ❌ (const) | n/a — cuma jalur MCP HTTP eksternal, BUKAN engine in-process |
| **command gate approval** | **25s** | (gate socket) | ❌ — ini wait persetujuan, bukan work-timeout | n/a |

### Konsekuensi kritikal (koreksi dari plan lama)
1. **Blocker foreground wick = HANYA shell 120s internal.** Bukan idle-kill, bukan SSE.
2. **Idle-kill BUKAN blocker.** Wick engine emit `toolUseLine` **sebelum** `dispatch` ([engine.go:204-206](../../agents/provider/wick/engine.go#L204-L206)) → parser set `toolInFlight` → idle timer paused. Jadi mekanisme (A) claude **sudah** berlaku di wick. Ini yang plan lama nggak sadar; harus di-test buat pastikan (P0b).
3. **SSE 5m TIDAK relevan buat engine in-process.** Engine wick jalan lewat io.Pipe → stream-json ([process.go](../../agents/provider/wick/process.go), [spawn.go](../../agents/provider/wick/spawn.go)), bukan lewat handler SSE. SSE 5m cuma cap buat MCP client eksternal (CLI provider yang manggil wick_execute over HTTP). Naikin shell cap **nggak** ketabrak SSE.
4. **`wick_execute` sudah honor `timeout_ms`.** Suspicion plan lama "di-cap diam-diam 120s" → tidak terbukti.

---

## 3. Root cause (dikoreksi)

### A. Shell hard-cap 120s tanpa override ← PRIMARY (foreground)
- [tool_shell.go:53](../../agents/provider/wick/tool_shell.go#L53): `context.WithTimeout(ctx, shellDefaultTimeout)` dengan `shellDefaultTimeout = 120s` const, no param.
- Schema shell ([32-41](../../agents/provider/wick/tool_shell.go#L32-L41)) cuma punya `command`.
- Komentar kode sendiri: "overridable later via instance config" — belum diimplement.

### B. Tidak ada background/detach shell ← PRIMARY (kerja paralel / >menit)
- Semua tool wick = sync: engine `runTurn` block di `dispatch` ([engine.go:206](../../agents/provider/wick/engine.go#L206)) sampai handler return.
- Nggak ada `run_in_background` → agent nggak bisa "start install, lanjut kerja lain, cek nanti".

### C. Tidak ada async job model + completion inject ← untuk kerja lintas-turn / on-complete
- Nggak ada start→poll→**on_complete inject turn**. Buat "cek sampai process selesai" (bukan "cek jam X"), butuh cara nge-inject user message ke engine yang lagi idle.
- **Kabar baik:** engine goroutine wick **hidup terus** di antara turn ([spawn.go:111-121](../../agents/provider/wick/spawn.go#L111-L121) `for { select <-p.msgs }`). Jadi inject turn = push string ke `p.msgs`. Ini fondasi murah buat `on_complete`.

### D. Error surface tidak seragam
- Shell: `(timed out after 2m0s)` · batch: `timed_out:true` · gate: `command gate: timeout` (approval, beda kelas). Agent susah bedain work-timeout vs approval vs exit≠0.

---

## 4. Kenapa fix sebelumnya belum kena

| Asumsi lama | Realita kode | Gap |
|---|---|---|
| "Global 120s untuk semua tool" | Cuma shell handler yang 120s internal | Diagnosis kelewat lebar |
| "idle-kill / SSE bakal bunuh long call" | idle-kill paused via toolInFlight; SSE nggak kena engine in-process | Effort nyasar |
| "wick_execute di-cap 120s diam-diam" | Honor timeout_ms 3m/5m | Verifikasi salah arah |
| "long-work = butuh job API dulu" | Foreground long-call cukup buat ≤ beberapa menit; bg buat sisanya | Over-engineer di awal |

**Yang sebenarnya perlu, berurutan:** (1) shell `timeout_ms` + cap naik → foreground long-call jalan; (2) `run_in_background` → paralel/>menit; (3) job API + `on_complete` → kerja lintas-turn.

---

## 5. Simulasi / repro (jadi test suite)

### Sim G1 — Foreground shell wall clock (butuh P0)
| Step | Call | Hari ini | Target |
|---|---|---|---|
| G1a | shell `sleep 60` | OK | OK |
| G1b | shell `sleep 180` | FAIL `timed out after 2m0s` | OK setelah cap naik (default cukup, atau `timeout_ms>180000`) |
| G1c | shell `sleep 900` (15m) foreground | FAIL 2m | OK kalau cap ≥ 15m **dan** idle-kill paused; ATAU arahkan ke bg (P2) |

### Sim G2 — `timeout_ms` shell dihormati (setelah P0)
| Step | Aksi | Target |
|---|---|---|
| G2a | shell `timeout_ms=180000` + sleep 150 | sukses, bukan mati 120s |
| G2b | shell `timeout_ms=5000` + sleep 30 | gagal ~5s |
| G2c | shell `timeout_ms=900000` + sleep 800 | sukses **atau** explicit max-cap error — dokumentasikan angkanya |

### Sim G3 — Idle-kill tidak bunuh long foreground (P0b, regression guard)
Foreground `sleep 300` (di atas idle 120s) → proses TIDAK di-idle-kill (karena `toolUse` sudah ke-emit → timer paused). Bukti: turn selesai + emit `tool_result` + `done`.

### Sim G4 — Background shell (P2)
| Step | Aksi | Target |
|---|---|---|
| G4a | shell `run_in_background=true` + `npm i` | balik `{bash_id}` < 2s, turn lanjut |
| G4b | `shell_output bash_id` (poll) | stdout parsial + `running` |
| G4c | `shell_output bash_id` setelah selesai | full output + `exit_code` |
| G4d | `shell_kill bash_id` | proses mati, status `killed` |
| G4e | short tool (`pwd`, `read_file`) saat bg jalan | tetap responsif |

### Sim G5 — Async job + completion inject (P3)
1. `job_start(tool=shell, params={command:"sleep 900"})` → `job_id` segera
2. agent **tidak** sleep-chain, boleh selesai turn
3. saat job selesai → satu message masuk session (`[job-done id=… ok=… exit=…]` + log tail) → turn baru auto
4. `job_cancel` → status cancelled, no false success

### Sim G6 — wick_execute (regression, harusnya OK)
`wick_execute timeout_ms=180000` op 150s → sukses. Batch satu op 200s → entry `timed_out` hanya kalau >3m. Re-verify angka smoke ~120s.

### Sim G7 — Error taxonomy
work-timeout (shell/batch) vs gate approval-timeout vs upstream 5xx vs exit≠0 → **field terpisah**.

---

## 6. Scope: apa yang berubah

| Surface | Status sekarang | Butuh |
|---|---|---|
| **shell (engine)** | 120s hard, no param, sync only | P0 `timeout_ms`+cap; P2 `run_in_background`+`shell_output`+`shell_kill`; P3 lewat job runner |
| **wick_execute** | ✅ honor timeout_ms 3m/5m | tidak ada fix timeout; opsional jadi job runner (P3) |
| **engine idle-kill** | paused saat tool in-flight | verify (P0b), jangan regres |
| **SSE outer 5m** | cap MCP HTTP eksternal | biarkan; tidak menyentuh engine in-process |
| **command gate 25s** | approval | terpisah — hanya masuk error taxonomy |
| **schedule** | time-wake only | tetap; jangan dipakai ganti process-complete |

---

## 7. Target arsitektur (berlapis)

### Layer 0 — Konsistensi timeout foreground
| Item | Target |
|---|---|
| Shell default | Naikkan dari 120s; align default 3m (ikut wick_execute) |
| Shell override | Tambah `timeout_ms` di schema |
| Shell cap | Clamp ke ceiling wajar (mis. 15–30m untuk in-process; SSE 5m TIDAK berlaku di engine) |
| Hierarki | `min(caller timeout_ms, tool clamp)` |
| Idle-kill | Pastikan tetap paused via `toolInFlight` selama shell jalan (P0b) |

### Layer 1 — Foreground long-call (kerja ≤ beberapa menit)
- Sudah viable setelah Layer 0. `sleep 300` jalan sebagai satu tool call, engine tetap hidup.

### Layer 2 — Background shell (kerja paralel / >menit) — pola claude
Tetap **dalam satu spawn** (engine goroutine hidup terus), tambah 3 tool:

| Tool | Fungsi |
|---|---|
| `shell{ command, run_in_background:true }` | spawn detached via safeexec (tanpa `WithTimeout`), simpan handle di registry per-session, balik `{bash_id}` segera |
| `shell_output{ bash_id }` | baca buffer stdout/stderr + status (`running`/`exited`/`killed`) + `exit_code` |
| `shell_kill{ bash_id }` | kill proses |

Detail:
- Registry proses per-session (map `bash_id → *bgProc`), lifetime ikut spawn; di-cleanup saat `Kill()`/session reap.
- Buffer output ring-capped (reuse `shellMaxOutput` 30k) biar `yes` nggak blow memory.
- Ini yang jawab "pakai bg" langsung — agent start bg, lanjut turn, poll manual.

### Layer 3 — Async job API generik + on_complete ★ (SCOPE PILIHAN USER)
Di atas Layer 2, tapi **generik** (bukan shell-only) dan **auto-notify** (bukan poll manual):

| Tool | Fungsi |
|---|---|
| `job_start{ tool, params, timeout_ms? }` | jalankan tool APAPUN (shell / wick_execute / playwright) di goroutine, balik `job_id` segera |
| `job_status{ job_id }` / `job_log{ job_id }` | poll |
| `job_cancel{ job_id }` | stop (cancel ctx runner) |
| **on_complete** | saat job selesai → **inject turn** ke engine: push `[job-done id=… tool=… ok=… exit=…] <log-tail>` ke `p.msgs` ([spawn.go:115](../../agents/provider/wick/spawn.go#L115)) → engine jalanin turn baru otomatis |

Mekanik on_complete (fondasi sudah ada):
- Engine goroutine idle nunggu `p.msgs`. Runner job, saat selesai, panggil callback yang push message ke channel itu.
- Butuh: seam dari job manager → `wickProcess.msgs` (lewat `toolContext`/scope, sejalan pola `externalToolProvider`).
- Job manager: state file per-run di `SessionDir` (ikut pola `pkg/job` yang sudah ada tapi TERPISAH — `pkg/job` itu scheduled maintenance, bukan ini). Simpan `{id, tool, params, status, exit, started, ended, log_ref}`.
- Runner registry: `shell` dulu (reuse Layer 2 bg proc), lalu `wick_execute` (bungkus Execute), lalu playwright.

### Layer 4 — Agent policy (prompt) + observability
| Durasi | Strategi |
|---|---|
| < 60s | sync biasa |
| 1–beberapa menit | sync + `timeout_ms` eksplisit |
| paralel / lama tapi mau poll manual | `run_in_background` (Layer 2) |
| lama + mau auto-notify saat selesai | `job_start` + on_complete (Layer 3) |
| "Cek jam X" | schedule |
| "Cek sampai process selesai" | job on_complete, BUKAN schedule |

Observability: metrics `tool_name/duration_ms/timed_out`; satu error shape `{error_class: work_timeout|approval_timeout|upstream|exit, …}`.

---

## 8. Definition of Done

- [ ] **G-root:** root cause benar — foreground blocker = shell 120s internal; idle-kill paused; SSE tak relevan engine (plan ini)
- [ ] **P0/G1/G2:** shell punya `timeout_ms` yang menggeser deadline; cap naik
- [ ] **P0b/G3:** long foreground call TIDAK di-idle-kill (test bukti)
- [ ] **P2/G4:** `run_in_background`+`shell_output`+`shell_kill` jalan; short tools tetap hidup saat bg aktif
- [ ] **P3/G5:** minimal satu path selesai lewat `job_start` + completion inject turn; cancel work
- [ ] **G6:** wick_execute timeout_ms regression; angka smoke ~120s diverifikasi
- [ ] **G7:** error taxonomy — work-timeout ≠ approval-timeout ≠ upstream ≠ exit

**Bukan DoD:** "sleep 60 OK" / "retest short matrix all green".

---

## 9. Phased delivery

| Phase | Isi | Outcome |
|---|---|---|
| **P0 — Shell param + cap** | `timeout_ms` di schema; naikin `shellDefaultTimeout`; clamp; verify idle-pause. | Foreground long-call viable |
| **P1 — Align** | Samakan default/error shape shell ↔ wick_execute; re-verify batch ~120s. | Sync path konsisten & jujur |
| **P2 — Background shell** | `run_in_background`+`shell_output`+`shell_kill`; per-session proc registry. | "Pakai bg" langsung dipenuhi |
| **P3 — Job API + on_complete** ★ | start/status/log/cancel + inject turn saat selesai; runner shell → wick_execute → playwright. | Kerja lintas-turn, auto-notify |
| **P4 — Guidance + hardening** | Prompt policy; quota, retensi log, UI job list, metrics, allowlists. | Production safe |

---

## 10. Engineer repro (copy-paste)

```text
# A. Foreground shell wall (the real 120s)
shell: sleep 600
→ today: ...(timed out after 2m0s)          [tool_shell.go:53]
→ after P0: OK (cap naik / timeout_ms)

# B. Idle-kill must NOT fire on long foreground (P0b)
shell: sleep 300
→ toolUse emitted → idle paused [engine.go:204 / agent.go:878]
→ turn completes, no idle-kill

# C. Background shell (P2)
shell: {command:"npm i", run_in_background:true} → {bash_id} fast
shell_output: {bash_id} → running / done + exit
shell_kill: {bash_id} → killed

# D. Async job + inject (P3)
job_start(tool=shell, params={command:"sleep 900"}) → job_id
… later, no sleep-chain …
session receives: [job-done id=… ok=true exit=0] <tail>  (turn baru auto)

# E. wick_execute honors timeout_ms (regression)
wick_execute: op ~150s, timeout_ms=180000 → succeeds  [connectors.go:533]
```

---

## 11. Out of scope (sengaja)
- Schedule menggantikan process-complete
- Chain `sleep 60` × N sebagai "fix"
- Longgarin semua security gate tanpa allowlist
- Nge-fix command gate 25s approval (UX terpisah — kecuali error taxonomy)
- Nyentuh SSE 5m buat engine in-process (nggak relevan)

---

## 12. Ringkas satu kalimat

**Yang salah (dikoreksi):** BUKAN global 120s, BUKAN idle-kill, BUKAN SSE — blocker foreground cuma **shell handler cap 120s** ([tool_shell.go:53](../../agents/provider/wick/tool_shell.go#L53)); idle-kill **sudah** di-pause saat tool jalan ([agent.go:878](../../agents/provider/agent.go#L878)) persis kayak claude.
**Target (kayak claude/codex):** (1) shell `timeout_ms`+cap naik → foreground long-call; (2) `run_in_background`+poll → "pakai bg"; (3) job API + on_complete inject turn → kerja lintas-turn auto-notify. Fondasi inject sudah ada (engine goroutine hidup nunggu `p.msgs`).

---

## 13. Mapping ke bug list session

| Bug ID | Item | Relasi plan ini |
|---|---|---|
| T-SHELL-CAP | shell hard 120s, no param | **Primary** — P0 |
| T-SHELL-IDLE | idle-kill vs long foreground | P0b verify (sudah aman) |
| T-SHELL-BG | shell no background | **P2** (jawaban "pakai bg") |
| T-EXEC-PARAM | wick_execute timeout_ms | ✅ sudah benar; P1 regression |
| T-JOB | no async job + on_complete | **P3** |
| T-NOTIFY | no completion inject | P3 (fondasi: engine goroutine idle nunggu p.msgs) |
| T-GATE-APPROVAL | `command gate: timeout` 25s | terpisah; hanya error taxonomy G7 |

---

*Plan revisi 3: dikoreksi dari kode nyata di commit 8430bd3c + referensi mekanisme claude/codex (foreground idle-pause + run_in_background) + bukti empiris bg 15m. Scope naik ke full async job API sesuai keputusan user. Ganti revisi 2.*
