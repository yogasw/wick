# Command Gate — Fix Kompatibilitas Claude 2.1.x + Bypass Path & Socket

Status: **shipped** 2026-05-10 (bug asli) · **updated** 2026-05-16 (bypass path & socket)
Stage: hotfix pasca-Stage-9, biar gate tetap jalan di Claude Code ≥ 2.1.138.
Pendamping [command-gate-architecture.md](command-gate-architecture.md).

Doc ini nyimpen *kenapa* kontrak PreToolUse + flag spawn diubah Mei 2026,
biar orang berikutnya yang baca gate ngga ngulang rangkaian dead-end yang
aku jalanin.

## Ringkasan singkat

Tiga bug independen kombinasi bikin gate keliatan rusak di sesi nyata,
walaupun unit test + tombol "Test gate" semua hijau:

1. **Kontrak block salah.** Gate exit kode 2 + stderr saja. Claude
   2.1.138 ngabaikan stdout JSON kalau exit code bukan 0, jadi deny
   envelope ngga pernah nyampe ke permission system → tool tetap jalan.
2. **`--permission-mode bypassPermissions` nge-skip hook.** Wick paksa
   flag ini setiap kali gate aktif, asumsinya gate ngegantiin prompt UI.
   Dari 2.1.138, `bypassPermissions` skip PreToolUse hook juga — jadi
   gate hook ngga pernah dipanggil sama sekali.
3. **Ngga ada allow envelope di happy path.** Setelah fix 1 + 2, tool
   yang udah di-approve tetap di-block sandbox baru Claude: gate exit 0
   tapi stdout kosong, claude jatuh ke permission flow sendiri,
   headless `claude -p` tanpa UI ke-sandbox-block ("Blocked by sandbox.
   Need approval. Retry with permission?").

Fix-nya wire gate ke kontrak JSON yang ke-dokumentasi di
<https://code.claude.com/docs/en/permissions> dan
<https://code.claude.com/docs/en/hooks>:

- Block path → exit 0 + stdout `{"hookSpecificOutput": {"permissionDecision": "deny", ...}}`
- Allow path → exit 0 + stdout `{"hookSpecificOutput": {"permissionDecision": "allow", ...}}`
- Wick stop kirim `--permission-mode bypassPermissions` saat gate aktif.

## Kronologi sampai ketahuan

Urutan diagnosa:

1. User report: `mkdir 123 test` jalan walaupun modal approval bilang
   "blocked". `commands.jsonl` confirm socket round-trip kembaliin
   `decision=block`, tapi folder ada di disk.
2. Hipotesis pertama: ada tool Claude lain yang lolos matcher kita
   (`Bash` / `Read` / `Write` / `Edit` / `Glob`). Grep `commands.jsonl`
   nampilin cuma 5 tool itu — berarti hook *fire*, tapi deny ngga
   dihormati.
3. Coba reference `claude_hook_integration_test.py` (Python) dari user.
   Block sukses. Hook Python print `{"continue": false, "stopReason":
   "..."}` ke stdout *baru* `sys.exit(2)`. Gate kita cuma `exit 2` +
   stderr.
4. Baca docs confirm: **JSON output cuma diproses pas exit 0**. Exit 2
   bikin claude abaikan stdout. Block path di-rewrite jadi exit 0 +
   deny envelope.
5. Walau gitu, sesi wick beneran tetap jalanin command yang di-block.
   Tapi handler probe (`POST /providers/probe-gate/...`) report gate
   honored. Probe spawn claude cuma pake `--settings <tempdir>/settings
   .json` — ngga ada flag `--permission-mode`. Sesi nyata pake
   `--permission-mode bypassPermissions` karena `pool/factory.go`
   set `cs.BypassPermissions = true` setiap gate attached. Hapus flag
   itu = fix kedua.
6. Setelah 5, tool ngga jalan tanpa approve, tapi model report
   "Blocked by sandbox. Need approval. Retry with permission?" —
   sandbox layer baru claude kick-in karena hook *allow* tanpa kasih
   tahu claude buat skip permission prompt. Tambah helper `emitAllow`,
   wire ke tiap branch approved.

## Kontrak block (sekarang)

```go
// emitBlock kirim PreToolUse deny envelope ke stdout + reason
// human-readable ke stderr.
func emitBlock(reason string) {
    payload := map[string]any{
        "continue":   false,
        "stopReason": reason,
        "hookSpecificOutput": map[string]any{
            "hookEventName":            "PreToolUse",
            "permissionDecision":       "deny",
            "permissionDecisionReason": reason,
        },
    }
    if data, err := json.Marshal(payload); err == nil {
        fmt.Fprintln(os.Stdout, string(data))
    }
    fmt.Fprintf(os.Stderr, "gate: blocked — %s\n", reason)
}
```

Pasangan dgn `return 0`. Tiap block site di `cmd/gate/main.go` panggil
`emitBlock` sebelum return.

## Kontrak allow (sekarang)

```go
// emitAllow kirim PreToolUse allow envelope, suruh claude skip
// permission prompt + langsung run tool.
func emitAllow(reason string) {
    payload := map[string]any{
        "hookSpecificOutput": map[string]any{
            "hookEventName":            "PreToolUse",
            "permissionDecision":       "allow",
            "permissionDecisionReason": reason,
        },
    }
    if data, err := json.Marshal(payload); err == nil {
        fmt.Fprintln(os.Stdout, string(data))
    }
}
```

Kenapa wajib: kalau gate exit 0 stdout kosong, claude fallback ke
permission flow sendiri. Headless `claude -p` ngga ada UI buat
prompt → tool hang atau ke-sandbox-block walau gate udah approve.
Allow envelope short-circuit jalur itu.

Allow path yg di-cover:

| Jalur                             | Reason field   |
| --------------------------------- | -------------- |
| Whitelist match                   | `whitelist`    |
| Auto-approved (always-allow)      | `auto_approved`|
| Daemon socket ngga ke-reach       | `no_socket`    |
| User klik Approve                 | the decision   |
| Path masih dalam DefaultScope     | `scope`         |
| Path kosong (no-op tool call)     | `no_path`       |
| Path relatif → resolve CWD → dalam scope | `scope`  |

`relative_path` reason lama **dihapus** — sebelumnya semua path relatif
langsung di-allow tanpa scope check. Sekarang path relatif di-resolve
terhadap `in.CWD` lebih dulu, lalu masuk scope check normal (lihat
"Fix 2026-05-16" di bawah).

## Yg dihapus: `--permission-mode bypassPermissions`

`pool/factory.go` lama:

```go
if activeGate != nil {
    s, err := f.attachGateConfig(opt, spawner, activeGate)
    ...
    if cs, ok := spawner.(claude.Spawner); ok {
        cs.BypassPermissions = true   // ← dihapus
        spawner = cs
    }
}
```

Kenapa dulu *dikira* perlu: gate jadi authority permission tunggal
setelah attached, jadi pengen suppress prompt UI claude biar ngga
tanya dua kali. Empiris di claude < 2.1.138 flag itu emang gitu: hook
tetap fire, prompt ke-skip.

Kenapa sekarang ngga boleh di-set:

> "`bypassPermissions` mode skips all permission prompts ..."
> — claude permissions docs

Di build sekarang flag itu skip **hook** juga. Dengan flag set,
PreToolUse hook ngga pernah dipanggil → gate ngga bisa block. Allow
envelope hook sendiri (`emitAllow`) udah suppress prompt clean tanpa
butuh flag — itu cara bener bikin gate jadi authority tunggal.

`BypassPermissions` di struct `claude.Spawner` ngga dihapus — channel
non-interactive (Slack/HTTP, ngga ada UI buat approve) tetap perlu
kalau gate ngga di-config. Doc comment sekarang spell out aturan
"jangan pernah pas gate aktif".

## Sumber referensi

Dokumentasi yg drive redesign:

- [Hooks contract](https://code.claude.com/docs/en/hooks) — schema
  output JSON, semantik exit-code, deprecation note buat field
  top-level `decision` / `reason`.
- [Permissions](https://code.claude.com/docs/en/permissions) — mode
  permission, warning `bypassPermissions`, section "extend permissions
  with hooks".
- Integration test user `claude_hook_integration_test.py` (ngga di
  repo) — ground-truth referensi kontrak block sebelum aku ketemu
  docs-nya.

Touch point code (urutan dependency):

- [`cmd/gate/main.go`](../../cmd/gate/main.go) — `emitBlock`,
  `emitAllow`, subcommand `--probe-deny`, semua block + allow site
  switched ke exit-0 + JSON.
- [`internal/agents/gate/claude_hook.go`](../agents/gate/claude_hook.go)
  — `ProbeGateSupport(ctx, claudeBin, gateBin)`: harness end-to-end
  buat tombol "Test gate" baru. Spawn claude dgn force-deny hook,
  suruh touch sentinel, report sentinel muncul / engga.
- [`internal/agents/pool/factory.go`](../agents/pool/factory.go) —
  hapus `BypassPermissions = true` saat gate attached.
- [`internal/agents/provider/claude/spawn.go`](../agents/provider/claude/spawn.go)
  — doc comment field `BypassPermissions` di-update biar jelas
  inkompatibel sama gate.
- [`internal/tools/agents/providers.go`](../tools/agents/providers.go)
  — handler HTTP `probeProviderGate` yg dipanggil tombol UI.
- [`internal/tools/agents/handler.go`](../tools/agents/handler.go) —
  registrasi route `POST /providers/probe-gate/{type}/{name}`.
- [`internal/tools/agents/view/providers.templ`](../tools/agents/view/providers.templ)
  — tombol "Test gate" per-card (claude only).
- [`internal/tools/agents/js/agents.js`](../tools/agents/js/agents.js)
  — handler click delegated yg manggil endpoint probe + render hasil
  inline.
- [`internal/agents/gate/integration_test.go`](../agents/gate/integration_test.go)
  — `TestGate_MalformedStdin` & `TestGate_TimeoutOnHangingStdin`
  di-rewrite biar assert exit-0 + stdout deny envelope (test lama
  assert exit 2).

## Cara verifikasi fix lokal

End-to-end via tombol UI baru:

1. Buka halaman Providers.
2. Cari card `claude/claude`. Klik **Test gate**.
3. Tombol harusnya jadi hijau dalam 5–30s dgn pesan
   `✓ gate honored — claude honored the deny envelope ...`.

Kalau merah:

- Cek `~/.<app>/agents/gate/commands.jsonl` entry terakhir — probe
  nulis pasangan `socket_dial` / `socket_recv` tagged sama settings
  file per-spawn. Kalau hilang, claude ngga loading hook sama sekali
  (path quoting, settings precedence).
- Re-run dgn env `--debug` atau cek `gate-YYYY-MM-DD.log` cari entry
  `gate invoked`.

Cek manual di sesi nyata:

1. Spawn sesi di UI wick.
2. Kirim `mkdir testfix`.
3. Modal approval harus muncul; pilih `Reject`.
4. Setelah modal close, `ls` di workspace dir atau cek
   `~/.<app>/agents/sessions/<id>/cwd/` — folder HARUS ngga ada.

Pre-fix, folder muncul ngga peduli pilihan user.

## Sinyal kalau contract berubah lagi

Kontrak PreToolUse udah berubah dua kali (top-level `decision` →
`hookSpecificOutput.permissionDecision`, exit-2 → exit-0+JSON). Dua
sinyal kalau berubah lagi:

- Probe `Test gate` mulai return `supported=false` di claude yg baru
  install.
- Sesi baru stop fire hook (ngga ada entry `gate invoked` di daily
  log).

Kalau gitu kejadian, fetch ulang <https://code.claude.com/docs/en/hooks>
dan diff schema vs `emitBlock` / `emitAllow`. Tambah field shape baru
+ ship binary; sisa plumbing wick udah decouple dari schema.

---

## Fix 2026-05-16: Gate bypass — socket guard, relative path scope, quoted split

### #1 — GateLoader tidak cek apakah socket benar-benar running

**File:** `internal/pkg/api/server.go`

**Masalah:** `GateLoader` hanya cek `gate_enabled` + `resolvedGateBin != ""`. Kalau
`approvalMgr.Start()` gagal (socket bind error — stale file, permission issue), `gateStatus.Enabled`
di-set false tapi GateLoader tetap return `GateConfig` valid. Akibatnya:
- Hook tetap ditulis ke workspace `.claude/settings.local.json`
- Claude spawn dengan hook aktif
- Gate binary jalan, dial socket, gagal → `emitAllow("no_socket")` ← **bypass total**

**Fix:** Tambah flag `gateSocketOK bool` yang di-set `true` hanya setelah
`approvalMgr.Start()` sukses. GateLoader cek flag ini sebelum return config.

### #2 — Path relatif di `runPathGate` langsung di-allow tanpa scope check

**File:** `cmd/gate/main.go` — fungsi `runPathGate`

**Masalah:** Tool non-Bash (`Read`, `Write`, `Edit`, `Glob`) dengan path relatif
langsung dapat `emitAllow("relative_path")` tanpa melewati scope check.
Contoh bypass nyata:
```
scope = /home/user/.wick/agents/workspaces/default/files
agent call: Edit("../../../../../../etc/passwd")
→ path relatif → emitAllow langsung  ← bypass scope
```

**Fix:** Resolve path relatif terhadap `in.CWD` via `filepath.Join` + `filepath.Clean`
sebelum scope check. `..` traversal tertangkap konsisten dengan Bash tool.

Note: upstream sudah tambah `knownFileTool` guard untuk MCP tools. Fix ini hanya
menyentuh bagian `knownFileTool == true` (known file tools), bukan MCP block.

### #3 — `splitCommand` pecah quoted path ber-spasi jadi token salah

**File:** `internal/agents/gate/rule.go`

**Masalah:** `strings.Fields` split pada whitespace tanpa peduli quotes.
`cat "/workspace/my file.txt"` → `["/workspace/my", "file.txt"]` — scope check
evaluasi path yang salah.

**Fix:** Quote-aware tokenizer yang hormati `"..."` dan `'...'` sebagai satu token.

### Touch point code (fix 2026-05-16)

- [`internal/pkg/api/server.go`](../pkg/api/server.go) — `gateSocketOK` flag + guard di `GateLoader`
- [`cmd/gate/main.go`](../../cmd/gate/main.go) — `runPathGate`: resolve relative path vs CWD sebelum scope check
- [`internal/agents/gate/rule.go`](../agents/gate/rule.go) — `splitCommand`: quote-aware tokenizer
