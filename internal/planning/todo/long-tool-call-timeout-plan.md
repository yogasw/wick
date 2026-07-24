# Plan: Long-running tool calls (shell hard-cap + no async job model)

**Konteks user:**
> "ini ngak cuma shell tapi semua tool call"
Mau support kerja **lama** (install, monitoring, scrape berat, batch connector, dsb). Fix sebelumnya masih kurang karena diagnosis-nya salah sasaran.

**Server (last retest):** wick-lab · commit `d4b607f` · provider `wick` · access=`http`

---

## 0. TODO

- [ ] **P0** — Tambah `timeout_ms` param + naikkan hard-cap di `shell` tool ([tool_shell.go:16](../../agents/provider/wick/tool_shell.go#L16))
- [ ] **P1** — Sejajarkan default & error shape shell dengan `wick_execute` (default 3m, clamp align ke SSE 5m)
- [ ] **P2** — Async job API generik (start/status/log/cancel + on_complete inject) — runner shell dulu
- [ ] **P3** — Prompt guidance: kapan sync vs job vs schedule
- [ ] **P4** — Hardening: quotas, log retention, UI job list, metrics
- [ ] Verifikasi ulang klaim "batch timed_out ~120s" (kode bilang default 3m — smoke test dulu mungkin salah baca)

---

## 1. Ground truth — timeout yang benar-benar ada di kode

Klaim plan lama ("satu global tool-call layer memotong ~120s untuk semua tool") **salah**. Tidak ada satu policy global. Ada **empat** mekanisme terpisah, cuma **satu** yang benar-benar 120s:

| Layer | Nilai | Sumber | Honor `timeout_ms`? | Gejala saat lewat |
|---|---|---|---|---|
| **SSE per-request (outer)** | **5m** | [sse.go:41](../../mcp/sse.go#L41), applied [sse.go:362](../../mcp/sse.go#L362) | ❌ (const) | `tool execution exceeded 5m0s timeout` |
| **`wick_execute` batch (per-call)** | default **3m**, clamp **5m** | [connectors.go:533-534](../../mcp/handlers/connectors.go#L533-L534), applied [744](../../mcp/handlers/connectors.go#L744) | ✅ [676-679](../../mcp/handlers/connectors.go#L676-L679) | entry `timed_out:true`, sisanya lanjut |
| **`shell` tool** | **120s hard** | [tool_shell.go:16](../../agents/provider/wick/tool_shell.go#L16) | ❌ tidak ada param | `...(timed out after 2m0s)` |
| **command gate approval** | **25s** | [socket.go:97](../../agents/gate/socket.go#L97) | ❌ — ini wait persetujuan, **bukan** work timeout | `command gate: timeout` |

**Konsekuensi untuk plan:**
- Satu-satunya surface yang stuck di **120s** = **shell**. Fix utama ada di sini.
- `wick_execute` **sudah** honor `timeout_ms` dengan benar (default 3m, clamp 5m, flag `TimedOut` terpisah dari error asli). Suspicion plan lama "mungkin di-cap diam-diam di 120s" → **terbukti tidak**.
- `command gate: timeout` **bukan** kelas timeout yang sama. Itu approval UI 25s. Jangan dicampur ke "wall clock 120s".
- SSE outer 5m = ceiling nyata untuk semua wick_execute; sejajar dengan batch max (5m), jadi bukan cap tersembunyi.

---

## 2. Bukti smoke test — dikoreksi vs kode

| # | Tool / surface | Call | Gejala lapangan | Cocok dengan kode? |
|---|---|---|---|---|
| 1 | **`shell`** | `sleep 180` / `600` / `900` | `(timed out after 2m0s)` | ✅ 120s hard cap |
| 2 | **`shell`** | `powershell Start-Sleep -Seconds 900` | sama | ✅ sama |
| 3 | **`wick_execute` batch** | 6-op Playwright | 1 entry `timed_out`, `duration_ms` ≈ **120014** | ⚠️ **TIDAK cocok** — batch default 3m. Angka ~120s kemungkinan (a) upstream/connector sendiri yang cap, atau (b) salah baca. **Wajib re-verify.** |
| 4 | **`wick_execute` (sw_ session connector)** | httprest GET | `command gate: timeout` | ✅ tapi ini **gate 25s**, bukan work timeout |
| 5 | **`wick_schedule_message` cancel** | cancel by id | `command gate: timeout` (retry OK) | ✅ gate 25s, flaky approval |
| 6 | **`wick_session_workspace` test** | probe HTTP | OK cepat | ✅ |
| 7 | **context7 / short ops** | resolve_library_id | OK | ✅ |
| 8 | **FS write/read/edit** | kecil | OK | ✅ |

**Kesimpulan bukti (dikoreksi):**
- 120s wall = **shell only**.
- `command gate: timeout` (#4, #5) = approval 25s, masalah UX approval, **bukan** long-work timeout.
- #3 (batch ~120s) **kontradiksi kode** → item verifikasi P0/P1.

---

## 3. Root cause (dikoreksi)

### A. Shell hard-cap 120s, tanpa override ← PRIMARY
- [tool_shell.go:16](../../agents/provider/wick/tool_shell.go#L16) `const shellDefaultTimeout = 120 * time.Second`, di-apply [53](../../agents/provider/wick/tool_shell.go#L53).
- Schema shell ([28-41](../../agents/provider/wick/tool_shell.go#L28-L41)) cuma punya `command` — **tidak ada** `timeout_ms`.
- Komentar kode sendiri bilang "overridable later via instance config" — belum diimplement.

### B. Tidak ada async job model generik ← PRIMARY untuk use case >5m
- Semua long-capable tool = sync request→block→response.
- Ceiling absolut = SSE 5m ([sse.go:41](../../mcp/sse.go#L41)). Kerja 10–30m (install/monitor) **tidak mungkin** lewat jalur sync manapun, sekalipun shell dinaikin.
- Tidak ada start→poll→on_complete-inject.

### C. Error surface tidak seragam
- Shell: `(timed out after 2m0s)`
- Batch: `timed_out:true` + `duration_ms`
- SSE outer: `tool execution exceeded 5m0s timeout`
- Gate: `command gate: timeout` (beda kelas — approval)
- Agent susah bedain: work-timeout vs approval-timeout vs upstream 5xx vs exit≠0.

### D. Secondary
| Item | Efek |
|---|---|
| Shell whitelist / metachar | Install cmd bisa diblok sebelum sempat lama |
| Schedule list always `[]` | Time-wake susah di-manage |
| Schedule ≠ process-complete | "Cek 10m lagi" ≠ "npm install selesai" |
| Playwright multi-step | Bisa nabrak SSE 5m (bukan 120s) |
| External flaky (httpbin 503) | Noise, bukan timeout wick |

---

## 4. Kenapa fix sebelumnya belum kena

| Asumsi lama | Realita kode | Gap |
|---|---|---|
| "Global 120s untuk semua tool" | Cuma shell yang 120s; wick_execute 3–5m; SSE 5m | Diagnosis kelewat lebar → fix nyasar |
| "wick_execute mungkin di-cap 120s diam-diam" | Honor `timeout_ms`, default 3m, clamp 5m | Effort verifikasi salah arah |
| "`command gate: timeout` = wall clock" | Approval wait 25s | Dua masalah beda digabung jadi satu |
| Retest short-matrix hijau | Short call memang selalu <2m | False confidence; long-path shell tetap merah |

**Yang sebenarnya perlu:** (1) shell `timeout_ms` + naikin cap, (2) async job untuk >5m, (3) error taxonomy.

---

## 5. Scope: apa yang perlu berubah

| Surface | Status sekarang | Butuh |
|---|---|---|
| **shell** | 120s hard, no param | `timeout_ms` param + cap naik (align 5m), lalu async job untuk >5m |
| **wick_execute** (single+batch) | ✅ honor `timeout_ms` 3m/5m | tidak ada fix timeout; async op opsional untuk >5m |
| **SSE outer** | 5m const | biarkan sbg sync ceiling; kerja >5m pindah ke job API |
| **session workspace probe/execute** | lewat wick_execute path | ikut wick_execute |
| **Playwright** | bisa nabrak 5m SSE | job API untuk crawl panjang |
| **command gate** | approval 25s | terpisah — UX approval, bukan bagian plan ini kecuali error-taxonomy |
| **schedule** | time-wake only | tetap; jangan dipakai ganti process-complete |

---

## 6. Simulasi / repro (jadi test suite)

### Sim G1 — Shell wall clock
| Step | Call | Hari ini | Target |
|---|---|---|---|
| G1a | shell `sleep 60` | OK | OK |
| G1b | shell `sleep 180` | FAIL `timed out after 2m0s` | OK jika `timeout_ms>180000`, atau pindah job API |
| G1c | shell `sleep 600` | FAIL 2m | job API (>5m SSE ceiling) |

### Sim G2 — `timeout_ms` shell dihormati (setelah P0)
| Step | Aksi | Target |
|---|---|---|
| G2a | shell `timeout_ms=180000` + sleep 150 | sukses (bukan mati 120s) |
| G2b | shell `timeout_ms=5000` + sleep 30 | gagal ~5s, bukan 120s |
| G2c | shell `timeout_ms=600000` + sleep 300 | sukses **atau** explicit max-cap error (SSE 5m) — dokumentasikan mana |

### Sim G3 — wick_execute (regression guard, harusnya sudah OK)
| Step | Aksi | Target |
|---|---|---|
| G3a | `wick_execute timeout_ms=180000` + op 150s | sukses |
| G3b | `wick_execute` default + op 200s | sukses (<3m default) |
| G3c | batch, satu op sengaja 200s | entry itu `timed_out:true` **hanya kalau >3m**; sisanya lanjut. Re-verify angka smoke ~120s. |

### Sim G4 — Long install (shell, butuh P2)
1. `npm init -y && npm i lodash` di worktree
2. Hari ini: FAIL >2m
3. Target: `job_id` → logs → `[job-done exit=0]`

### Sim G5 — Concurrent
Start long job → sambil jalan `pwd`, `wick_list`, `read_file` → short tools tetap hidup.

### Sim G6 — Completion notify
Start long → agent **tidak** sleep-chain → saat selesai satu message masuk session (tool, id, ok/exit, log tail) → turn baru auto.

### Sim G7 — Cancel / kill
Start long → cancel → status cancelled, no false success.

### Sim G8 — Error taxonomy
work-timeout (shell/batch/SSE) vs gate-deny/approval-timeout vs upstream 5xx vs exit≠0 → **field terpisah**.

---

## 7. Target arsitektur

### Layer 0 — Konsistensi timeout sync
| Item | Target |
|---|---|
| Shell default | Naikkan dari 120s; align default 3m (ikut wick_execute) |
| Shell override | Tambah `timeout_ms` di schema |
| Shell/batch cap | Clamp ke SSE ceiling 5m (satu angka) |
| Hierarki | `min(caller timeout_ms, tool clamp 5m, SSE 5m)` |
| SSE 5m | Tetap sbg outer sync ceiling; kerja >5m = job API |

### Layer 1 — Sync path (kerja ≤5m)
- Short ops: default 3m cukup.
- Agent set `timeout_ms` eksplisit untuk 3–5m.
- Progress opsional: stream partial (log tail) kalau transport izinkan (SSE sudah punya progress buffer — [sse.go:31](../../mcp/sse.go#L31)).

### Layer 2 — Async job path (kerja >5m) ★ PRODUCT FIX
Generik, bukan shell-only:

| API | Fungsi |
|---|---|
| `job_start { tool, params, timeout_ms? }` | return `job_id` segera |
| `job_status` / `job_log` | poll |
| `job_cancel` | stop |
| **on_complete** | inject session turn: `[job-done id=… tool=… ok=… exit=…]` + log ref |

Backend = runner plugins (shell subprocess dulu, lalu wick_execute wait / playwright).

### Layer 3 — Agent policy (prompt)
| Durasi | Strategi |
|---|---|
| < 60s | sync biasa |
| 1–5m | sync + `timeout_ms` eksplisit |
| > 5m atau unknown | **wajib** `job_start` |
| "Cek jam X" | schedule |
| "Cek sampai process selesai" | job on_complete, bukan schedule |

### Layer 4 — Observability
- Metrics: tool_name, duration_ms, timed_out rate.
- Satu error shape: `{ error_class: "work_timeout" | "approval_timeout" | "upstream" | "exit", ... }`.

---

## 8. Definition of Done

- [ ] **G1:** Root cause didokumentasikan benar — 120s = **shell only**, bukan global (plan ini)
- [ ] **P0/G2:** shell punya `timeout_ms` yang benar-benar menggeser deadline; cap naik ke 5m
- [ ] **G3:** wick_execute `timeout_ms` di-regression-test; angka smoke ~120s di-verify ulang
- [ ] **P2/G4:** minimal satu path >5m selesai lewat async job + completion inject
- [ ] **G5:** short tools jalan saat long job aktif
- [ ] **G7:** cancel work
- [ ] **G8:** error taxonomy — work-timeout ≠ approval-timeout ≠ upstream ≠ exit
- [ ] Shell whitelist tidak blok install cmds yang diizinkan
- [ ] Schedule list bug (terpisah) tidak blok long-job design

**Bukan DoD:** "sleep 60 OK" / "retest short matrix all green".

---

## 9. Phased delivery

| Phase | Isi | Outcome |
|---|---|---|
| **P0 – Shell param** | Tambah `timeout_ms` di shell schema; naikin `shellDefaultTimeout`; clamp ke 5m. | Shell 3–5m viable |
| **P1 – Align** | Samakan default/error shape shell ↔ wick_execute; re-verify batch ~120s. | Sync path konsisten & jujur |
| **P2 – Job API** ★ | start/status/log/cancel + on_complete inject; runner shell dulu, lalu wick_execute/playwright. | Install/monitor 10–30m |
| **P3 – Guidance** | Prompt: kapan sync vs job vs schedule. | Stop sleep-chain |
| **P4 – Hardening** | Quotas, log retention, UI job list, metrics, allowlists. | Production safe |

---

## 10. Engineer repro (copy-paste)

```text
# A. Shell wall (the real 120s)
shell: sleep 600
→ today: ...(timed out after 2m0s)          [tool_shell.go:16]

# B. wick_execute honors timeout_ms (should already pass)
wick_execute: op ~150s, timeout_ms=180000
→ today: succeeds (default 3m, clamp 5m)     [connectors.go:533]
→ if it dies ~120s: FILE A BUG, contradicts code

# C. Batch per-call
wick_execute batch: include one op ~200s
→ today: that entry timed_out only if >3m; others continue
→ re-verify the smoke-test "~120014ms" number

# D. SSE outer ceiling
any wick_execute > 5m
→ today: "tool execution exceeded 5m0s timeout"  [sse.go:41]

# E. Desired product (>5m)
job_start(tool=shell|wick_execute, …) → job_id
… later …
session receives: [job-done id=… ok=true …]
```

---

## 11. Out of scope (sengaja)
- Schedule menggantikan process-complete
- Chain `sleep 60` × N sebagai "fix"
- Longgarin semua security gate tanpa allowlist
- Nge-fix command gate 25s approval (masalah UX terpisah — kecuali error taxonomy)

---

## 12. Ringkas satu kalimat

**Yang salah (dikoreksi):** BUKAN satu global 120s — yang stuck 120s cuma **shell** ([tool_shell.go:16](../../agents/provider/wick/tool_shell.go#L16)); `wick_execute` sudah honor `timeout_ms` (3m/5m), SSE outer 5m, dan `command gate: timeout` itu approval 25s yang beda kelas. Masalah nyata = shell tanpa `timeout_ms` + tidak ada async job model untuk >5m.
**Target:** shell dapat `timeout_ms` + cap naik ke 5m (sync ≤5m), lalu **async job + completion message** untuk semua kerja >5m.

---

## 13. Mapping ke bug list session

| Bug ID | Item | Relasi plan ini |
|---|---|---|
| T-SHELL-CAP | shell hard 120s, no param | **Primary** — P0 |
| T-EXEC-PARAM | wick_execute timeout_ms | ✅ sudah benar; P1 regression guard |
| T-SHELL-SYNC | shell no async | P2 runner |
| T-NOTIFY | no completion inject | P2 |
| T-GATE-APPROVAL | `command gate: timeout` 25s | terpisah (UX approval), cuma masuk error taxonomy G8 |
| T-SCHED-LIST | list always [] | terpisah, secondary |
| T-PW-STATUS | Playwright bisa nabrak 5m | P2 |

---

*Plan revisi 2: dikoreksi dari kode nyata. Bukan "global 120s" — shell hard-cap + missing async model. Ganti revisi 1 yang salah root cause.*
