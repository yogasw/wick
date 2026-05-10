# Command Gate — Arsitektur & Approval System

Status: **implementasi selesai** (Stage 1–9 done, smoke test pending).
Update terakhir: 2026-05-10 (Stage 9 + post-merge cleanup: GATE_SPEC + GATE_BIN
dihapus, single shared spec + single shared socket, gate session-agnostic, daemon
route by cwd, installer ship sidecar `<app>-gate`, `gateAwareSpawner` + sisa
dead-code dari mekanisme env-var dropped).

Keputusan final yang sudah locked:
- IPC: Unix Domain Socket (raw JSON, bukan HTTP)
- Gate binary: embed ke main binary via `//go:embed` (bukan sidecar/subcommand)
- Source: `cmd/gate/` di repo wick, di-build via `wick build` sebagai bagian dari builder pipeline (bukan step CI manual lagi)
- Branding: binary user-visible jadi `<app>-gate` (mis. `myapp-gate.exe`); embed asset internal generic `gate-<os>-<arch>`; `AppName` di-inject via ldflag
- Resolution: sibling-of-executable (`<app>-gate[.exe]` di samping main, **shipped via installer**) → embedded extract (backup untuk portable .exe / source build) → `<app>-gate` di PATH (last-ditch). Zero env vars.
- Spec channel: **shared spec** di `~/.<app>/agents/gate/spec.json` (zero env vars; gate derive path dari compile-time `gate.AppName` ldflag)
- Socket channel: **shared socket** di `~/.<app>/agents/gate/gate.sock` (single listener, daemon route by cwd dari hook payload → wick session)
- Audit log: **shared** `~/.<app>/agents/gate/commands.jsonl` (UI session-detail filter by workspace cwd prefix)
- Approval style: Gate style (Pola A) — system intercept, bukan Claude Code style
- Decision modes: 4 (`approve_once` / `approve_session` / `approve_always` / `block`)
- AskUser: MCP tool (bukan harness), bridged ke web UI lewat SSE

---

## Checklist Implementasi (Quick Reference)

Urutan **timeline-aware**: tiap stage hanya butuh stage sebelumnya. Bisa di-pause di akhir stage manapun dan tetap shippable. Detail per task + exit criteria di [§12](#12-checklist-implementasi-detail).

```
Stage 1 — Spec & Wiring Foundation                                    ✅ done
[x] S1.1 gate.Spec field SocketPath + AutoApproved
[x] S1.2 WriteSpawnArtifacts: tulis SocketPath = <sessionDir>/gate/gate.sock
[x] S1.3 Wire Gate di pool/factory.go (saat ini Gate field masih nil)
[x] S1.4 Unit test spec marshal + artifact write

Stage 2 — Daemon Socket Listener                                      ✅ done
[x] S2.1 Unix socket listener per session, chmod 0600
[x] S2.2 Cleanup os.Remove saat session stop / daemon shutdown
[x] S2.3 Goroutine per koneksi (raw JSON newline-delimited)
[x] S2.4 Pending state manager: sync.Map[id]chan ApprovalResponse
[x] S2.5 Timeout goroutine 25s → auto-block
[x] S2.6 Test dial socket dari fake gate

Stage 3 — Gate Binary Upgrade                                         ✅ done
[x] S3.1 gate binary dial unix socket dari spec
[x] S3.2 auto_approved short-circuit (zero-latency always-allow)
[x] S3.3 Encode ApprovalRequest → kirim
[x] S3.4 Decode ApprovalResponse → exit 0 atau 2
[x] S3.5 Fail-safe: connect refused / timeout → exit 2
[x] S3.6 Integration test dgn fake socket server

Stage 4 — Embed + Binary Resolution                                   ✅ done
[x] S4.1 //go:embed assets/gate-* (generic, ke-pickup runtime via OS/arch lookup)
[x] S4.2 extractEmbeddedGate(sessionDir) → <session>/gate/gate[.exe], chmod 0755, idempotent
[x] S4.3 resolveGateBin: sibling-of-exe (<app>-gate, installer-shipped) → embed extract → PATH
[x] S4.4 Wire ke factory.go
[x] S4.5 Build gate sidecar inline di internal/builder (drop dari CI, soft-skip pada
         downstream fork tanpa cmd/gate)

Stage 5 — Web UI: Approval Modal + 4 Modes                            ✅ done
[x] S5.1 SSE event types approval_request + approval_resolved
[x] S5.2 Endpoint POST /api/agents/sessions/{id}/approve
[x] S5.3 approve_session in-memory map per session
[x] S5.4 approve_always persist ke spec.AutoApproved
[x] S5.5 Modal templ: 4 tombol + countdown 25s
[x] S5.6 matchKey hash(tool + cmd), exact match MVP
[x] S5.7 "Approved commands" panel + Revoke per item
[ ] S5.8 Smoke test manual (real-claude end-to-end)

Stage 6 — AskUser MCP Tool + Web Card                                 ✅ done
[x] S6.1 MCP tool "ask_user" register
[x] S6.2 Handler: pending channel + broadcast SSE + 5min timeout
[x] S6.3 SSE event types ask_user + ask_user_resolved
[x] S6.4 Endpoint POST /api/agents/sessions/{id}/answer
[x] S6.5 Card templ inline di composer area
[ ] S6.6 Smoke test agent → web → answer roundtrip (real-claude)

Stage 7 — Dev Tooling                                                 ✅ done
[x] S7.1 .vscode/tasks.json: debug:prep build bin/<app>-gate[.exe] sibling
[-] S7.2 task "gate: sync-spec" — dropped (gak diperlukan, lihat D12)
[-] S7.3 launch "wicklab-gate" — dropped (gate gak bisa standalone, lihat D13)
[-] S7.4 .env.example: GATE_BIN entry — DROPPED (env var dihapus, resolution otomatis dari sibling/embed/PATH)
[x] S7.5 ResolveGateBinary tambah sibling-of-exe step (auto-discover bin/)
[x] S7.6 Doc updated dgn flow normal + cara debug via test/logs

Stage 8 — Observability + Status (post-7 follow-ups)                  ✅ done
[x] S8.1 commands.jsonl audit trail multi-stage: received → socket_dial →
         socket_sent → socket_recv → terminal (allowed/blocked). Setiap
         entry tagged dgn RequestID supaya 1 invocation traceable
[x] S8.2 Entry struct extend: Stage, Tool, Decision, RequestID, MatchKey
[x] S8.3 Providers page: GateStatusCard (enabled/disabled + binary path +
         resolution source label + behavior note)
[x] S8.4 Session detail: GateDisabledBanner di top kalau gate gak resolved
[x] S8.5 ResolveGateBinaryWithSource() — return source label utk UI debug

Stage 9 — Spec Resolution Refactor (hapus GATE_SPEC env var)          ✅ done
Catatan 2026-05-10: full removal selesai. Single shared spec + single shared socket;
gate session-agnostic; daemon route approval ke session berdasarkan cwd di hook payload.
[x] S9.1 GATE_SPEC env var dihapus dari gate.LoadSpec() — path derived dari
         gate.AppName ldflag → ~/.<app>/agents/gate/spec.json
[x] S9.2 gate.AppName ldflag di package gate (di-share antara binary lookup +
         shared paths). default kosong → fallback "wick"
[x] S9.3 gate.LoadSpec(appName) resolve path: os.UserHomeDir() + ".<app>" +
         "agents/gate/spec.json". Missing file = empty Spec, no error
[x] S9.4 Builder inject AppName via ldflags (`-X .../gate.AppName=<app>`) — applies
         ke semua wick build invocations termasuk `wick build --all`
[x] S9.5 GATE_SPEC env var dihapus dari: pool/factory.go (drop ExtraEnv inject),
         claude_hook.go (drop HookEnvVar const), cmd/gate/main.go (drop env read)
[x] S9.6 Unit tests refactored: pakai t.Setenv("HOME", ...) + WriteSharedSpec()
         daripada env var setup; integration_test build gate dgn ldflag AppName
[x] S9.7 Fail-safe: missing shared spec.json → LoadSpec returns empty Spec, gate
         falls through to socket dial → no daemon → exit 2 (verified via
         TestGate_MissingSharedSpecIsEmpty)

Stage 9 follow-ups (in same pass):
[x] S9.8  ApprovalManager: single shared listener (drop StartSession/StopSession),
          routeByCWD callback maps hook cwd → session for SSE broadcast routing
[x] S9.9  commands.jsonl: shared global file (~/.<app>/agents/gate/commands.jsonl);
          UI session-detail Commands tab filters by workspace cwd prefix
[x] S9.10 ApprovalManager.Resolve: approve_always rewrites SHARED spec
          (was per-session); RevokeAlways same
[x] S9.11 server.go boot syncSharedSpec — writes Rules from configsSvc on boot
          + every Build invocation, preserves existing AutoApproved

Stage 9 post-merge cleanup pass:
[x] S9.12 Installer ship sidecar — MSI: `<App>-gate.exe` di same folder via WXS
          GateExecutable component; .deb: `/usr/bin/<app>-gate`; .app:
          `Contents/MacOS/<App>-gate`. PackageMSI/PackageDeb/PackageApp signatures
          extended with `gateBinPath` param wired from builder.go
[x] S9.13 Drop GATE_BIN env var — sibling-of-executable jadi resolution path
          pertama (installer ships it), embed extract jadi backup, PATH last.
          envOverride const + SourceEnvOverride label + os.Getenv check semua
          dropped dari embed.go; embed_test.go drop env-override test
[x] S9.14 Dead code cleanup di pool/factory.go — `gateAwareSpawner` struct +
          `Spawn` method + `extraEnv` local di Build + nil-tuple return dari
          `attachGateConfig` semua dropped (artefak dari era env-var injection).
          Signature: `attachGateConfig` jadi `(Spawner, error)`
[x] S9.15 Redundancy cleanup di server.go — `resolveGateBin` local helper
          (duplicate dari `agentgate.ResolveGateBinaryWithSource`) dropped,
          GateLoader pakai `resolvedGateBin` dari boot-time resolve. `gateAppName
          := agentgate.AppName; if "" { = "wick" }` fallback dropped (gate
          package internal udah handle empty di sharedGateDir). `os/exec` import
          dropped. UI copy + comments updated (no env var mentions)
[x] S9.16 Misc surface cleanup — `requestApproval` test-only wrapper di
          cmd/gate/main.go dropped (test pakai requestApprovalWithLog langsung
          dgn requestID=""). `AutoApprovedFor(_)` shim di manager.go dropped
          (caller pakai `AutoApproved()` global). `PendingFor(sessionID)` arg
          unused — keep signature untuk JSON view-model readability tapi underscore
[x] S9.17 UI copy update — providers.go Note + approvals.go error msg + 
          approvals.templ banner: drop "Set GATE_BIN" / "WICK_GATE_BIN" mentions,
          ganti dgn "Run `wick build` to produce the sibling sidecar and embedded
          fallback"
```

| Stage | Hot files |
|---|---|
| 1 | `internal/agents/gate/spec.go`, `claude_hook.go`, `pool/factory.go` |
| 2 | `internal/agents/gate/socket.go` (new) |
| 3 | `cmd/gate/main.go` |
| 4 | `internal/agents/gate/embed.go`, `internal/builder/{builder,gate,ldflags}.go`, `template/.github/workflows/release.yml` |
| 5 | `internal/tools/agents/{handler,stream}.go`, `view/approval.templ`, `js/agents.js`, `internal/agents/gate/matchkey.go` (new) |
| 6 | `internal/tools/agents/mcp_askuser.go` (new), `view/askuser.templ` |
| 7 | `.vscode/{tasks,launch}.json` |

---

## 0. TL;DR

**Command Gate** = mekanisme intercept shell command sebelum Claude mengeksekusinya. User bisa approve atau block command secara real-time tanpa restart session.

Dokumen ini menjelaskan:
- Kenapa gate diperlukan dan bagaimana cara kerjanya
- Perbandingan dua pola approval (Claude Code style vs Gate style)
- Perbandingan empat opsi IPC antara gate dan daemon
- Detail Unix Domain Socket — cara kerja, keamanan, isi file
- Bagaimana Web UI perlu render dua jenis interaksi (gate approval + AskUser)
- Cara release dengan main binary + branded gate sidecar (`<app>-gate`) termasuk MSI
- Cara resolve path gate di tiga environment: VSCode, serve, MSI
- Rekomendasi akhir dengan justifikasi

---

## 1. Latar Belakang: Kenapa Gate Diperlukan?

### 1.1 Masalah Tanpa Gate

Claude berjalan sebagai subprocess long-lived. Begitu user kirim pesan, Claude bisa langsung eksekusi shell command:

```
User: "hapus semua log lama"
Claude: [langsung jalankan: find /var/log -mtime +30 -delete]
        → tidak ada yang bisa stop
```

Tidak ada titik intercept. Command sudah jalan sebelum user sempat berpikir.

### 1.2 Solusi: PreToolUse Hook

Claude CLI menyediakan hook system — sebelum tool (Bash, dll.) dieksekusi, Claude memanggil binary eksternal dan **menunggu exit code-nya**:

```
exit 0  → lanjutkan eksekusi
exit 2  → batalkan, Claude dapat pesan "blocked by user"
```

Gate sidecar (binary user-visible: `<app>-gate`, source: `cmd/gate/`) adalah binary yang dipanggil oleh hook ini. Dia yang memutuskan allow atau block.

### 1.3 Sesi Claude Tidak Di-Respawn Per Pesan

Penting untuk dipahami: **Claude tidak di-spawn ulang setiap pesan**. Satu proses Claude hidup sepanjang sesi, menerima pesan via stdin dan membalas via stdout.

```
[kamu] "hai apa kabar"  →  stdin → [claude PID 1234]
[kamu] "tanya lagi"     →  stdin → [claude PID 1234]  ← PID sama
```

Proses baru hanya di-spawn kalau:
- Idle timeout (120 detik tanpa event) → kill → respawn dengan `--resume`
- Explicit `Stop()` dipanggil

Konsekuensinya: gate bisa block di tengah turn yang sama, Claude tetap menunggu. Tidak ada race condition karena proses mati di tengah jalan.

### 1.4 Built-in vs gate sidecar

Claude Code punya dialog permission bawaan (TUI terminal):

```
Allow this bash command?
  rtk git status
  Show working tree status

  1 Yes
  2 Yes, allow rtk git * for this session
  3 No

  Tell Claude what to do instead
```

**Wick sengaja mematikan dialog ini** dengan set `bypassPermissions = true` di `settings.json`, lalu pasang gate sidecar sebagai penggantinya:

```
Tanpa Wick → dialog TUI terminal muncul
Dengan Wick → bypassPermissions = true → dialog mati → gate sidecar aktif
```

| | Claude Code Built-in | gate sidecar (`<app>-gate`) |
|---|---|---|
| UI | Terminal TUI | Web UI Wick |
| "For this session" | Ada otomatis | Perlu diimplementasi |
| Siapa yang render | Claude Code harness | Daemon Wick via SSE |
| Configurable per rule | Terbatas | Full control via spec.json |

---

## 2. Dua Pola Approval

Ada dua cara fundamental untuk mendapat konfirmasi user sebelum command jalan.

### 2.1 Pola A — Gate Style (System Intercept) ✅ REKOMENDASI

System yang memaksa konfirmasi, bukan Claude.

```
Step 1:  User kirim pesan ke Claude
Step 2:  Claude putuskan untuk jalankan command
         → hook fire → gate dipanggil → gate BLOCK
         → UI muncul di user: "Approve rm -rf /data?"
         → user klik Approve
         → gate exit 0 → command jalan (masih dalam turn yang sama)
Step 3:  Claude selesai, balas ke user
```

**Kelebihan:**
- Jaminan 100% setiap Bash command pasti melewati gate, tidak bisa di-bypass Claude
- Blocking dalam satu turn — tidak perlu turn baru
- Audit log otomatis via `commands.jsonl`

**Kekurangan:**
- Lebih kompleks untuk diimplementasi
- Perlu binary terpisah (`<app>-gate`) + endpoint daemon + socket

### 2.2 Pola B — Claude Code Style (Voluntary Ask)

Claude sendiri yang memutuskan untuk tanya sebelum bertindak. Ini yang dipakai ketika Claude menampilkan pertanyaan dengan pilihan di chat.

```
Turn 1: User: "hapus log lama"
        Claude: "Ini akan hapus /var/log/app.log. Lanjut?" ← turn selesai

Turn 2: User: "iya"
        Claude: [jalankan: rm /var/log/app.log]
        Claude: "Berhasil dihapus"
```

Mekanismenya: Claude output `tool_use` dengan nama `AskUserQuestion` ke stream, frontend render jadi UI interaktif, user jawab masuk sebagai tool result ke turn berikutnya.

**Kelebihan:**
- Tidak perlu gate binary sama sekali
- Lebih natural, conversational

**Kekurangan:**
- Claude bisa "lupa" untuk tanya → command langsung jalan
- Tidak bisa jadi security enforcement
- `AskUserQuestion` adalah tool harness Claude Code — **tidak tersedia** saat Claude jalan sebagai subprocess Wick (`-p` pipe mode)

### 2.3 Perbandingan Lengkap

| Dimensi | Gate Style (Pola A) | Claude Code Style (Pola B) |
|---|---|---|
| **Jumlah step** | 3 (dari perspektif user) | 4 (turn-based) |
| **Yang memutuskan tanya** | System (selalu) | Claude (boleh lupa) |
| **Bisa di-bypass Claude?** | Tidak — system-level | Ya — Claude bisa langsung eksekusi |
| **Jaminan intercept** | 100% setiap command | Bergantung prompt + behavior Claude |
| **Perlu gate binary?** | Ya | Tidak |
| **Perlu backend endpoint?** | Ya | Tidak |
| **Blocking** | Dalam turn yang sama | Butuh turn baru |
| **Channel komunikasi** | IPC (socket/pipe) | stdin/stdout turn-based |
| **Tersedia di Wick subprocess** | Ya (kita yang pasang) | Tidak (harness-only) |
| **Cocok untuk** | Security-critical, audit wajib | UX conversational, low-risk |

### 2.4 Kapan Pakai Yang Mana?

```
Butuh JAMINAN bahwa setiap command pasti di-approve?
├── Ya → Pola A (Gate Style)
└── Tidak
    └── Cukup Claude tanya sendiri untuk action besar? → Pola B (Claude Code Style)
```

**Keputusan**: Wick pakai **Pola A** untuk enforcement. Pola B tidak bisa menjamin intercept dan tidak tersedia di pipe mode.

---

## 3. Opsi IPC: Gate ↔ Daemon

Gate adalah subprocess terpisah dari daemon. Mereka perlu berkomunikasi. Ada empat opsi.

### 3.1 HTTP (TCP)

```go
// gate
resp, _ := http.Post("http://localhost:9425/api/agents/approve",
    "application/json", payload)
```

| | |
|---|---|
| **Kelebihan** | Familiar, tooling lengkap (curl debug), mudah test |
| **Kekurangan** | Port bisa diakses dari network, perlu auth token, overhead HTTP |
| **Performa** | ~1-5ms (TCP handshake + HTTP parsing) |
| **Keamanan** | Harus bind 127.0.0.1 + auth, tetap ada risiko port scanning |

### 3.2 Unix Domain Socket ✅ DIPILIH

```go
// gate
conn, _ := net.Dial("unix", "~/.wick/sessions/<id>/gate.sock")
json.NewEncoder(conn).Encode(request)
json.NewDecoder(conn).Decode(&response)  // blocking sampai daemon balas
```

| | |
|---|---|
| **Kelebihan** | Zero network exposure, akses via file permission, zero port, cepat |
| **Kekurangan** | Hanya lokal, satu machine |
| **Performa** | ~0.1ms, tanpa TCP overhead, tanpa HTTP parsing |
| **Keamanan** | chmod 0600 cukup, tidak bisa diakses dari network sama sekali |
| **OS Support** | Linux ✅, macOS ✅, Windows 10 build 1803+ ✅ |

### 3.3 Named Pipe / FIFO

```bash
mkfifo gate-req.fifo gate-res.fifo
# gate: tulis ke req → baca dari res
# daemon: baca dari req → tulis ke res
```

| | |
|---|---|
| **Kelebihan** | Zero dependency, primitif, ada di semua Unix |
| **Kekurangan** | Perlu dua file per session, tidak bisa concurrent requests |
| **Performa** | ~0.1ms |

### 3.4 File + inotify / Polling

```
gate tulis → ~/.wick/sessions/<id>/gate/pending/abc123.json
daemon watch dir → baca → proses
daemon tulis → ~/.wick/sessions/<id>/gate/decision/abc123.json
gate poll / watch → baca
```

| | |
|---|---|
| **Kelebihan** | Audit trail otomatis, debuggable dengan `cat` |
| **Kekurangan** | Polling = latency, file di disk = risiko leak credential |
| **Performa** | 10-100ms kalau polling, ~1ms kalau inotify |

### 3.5 Perbandingan Empat Opsi

| Dimensi | HTTP | Unix Socket | Named Pipe | File+inotify |
|---|---|---|---|---|
| **Network exposure** | Ya (loopback) | Tidak | Tidak | Tidak |
| **Concurrent requests** | Ya | Ya | Tidak | Ya (per file) |
| **Overhead** | Tinggi | Rendah | Rendah | Tinggi (polling) |
| **Debug** | Mudah (curl) | Sedang | Sulit | Mudah (cat file) |
| **Auth diperlukan** | Ya | Tidak | Tidak | Tidak |
| **Bidirectional** | Ya | Ya | Perlu 2 pipe | Perlu 2 dir |
| **Windows support** | Ya | Build 1803+ | Tidak | Ya |
| **Implementasi Go** | `net/http` | `net.Listen("unix")` | `os.OpenFile` | `os` + poll |

**Keputusan: Unix socket.** Tidak ada network exposure, performa terbaik, implementasi hampir sama dengan HTTP tapi ganti `tcp` → `unix`.

---

## 4. Deep Dive: Unix Domain Socket

### 4.1 Apa Itu File Socket?

Socket file **bukan file biasa**. Tidak ada data di dalamnya.

```bash
$ ls -la gate.sock
srwxr-xr-x 1 user user 0 May 9 10:00 gate.sock
# ^--- "s" = socket, bukan "-" regular file. Ukuran selalu 0 bytes.

$ cat gate.sock
cat: gate.sock: No such device or address  ← tidak bisa dibaca seperti file
```

Socket file adalah **alamat titik temu** — seperti nomor telepon. Data mengalir di kernel memory buffer, tidak pernah menyentuh disk.

```
gate.sock di filesystem
     │
     │  bukan tempat data disimpan
     │  tapi "pintu" yang bisa di-connect
     │
     ├── gate  → connect() → buka koneksi ke daemon
     └── daemon → listen() → terima koneksi dari gate
                   │
                   └── data JSON mengalir di kernel buffer
                       tidak pernah ke disk
```

**Analogi**: colokan listrik di dinding. Tidak ada "isi" di colokan, tapi kalau kamu colok sesuatu, arus mengalir.

### 4.2 Protokol: Raw JSON Newline-Delimited

Tidak ada protokol HTTP. Langsung kirim JSON diakhiri newline:

```
gate → daemon:   {"id":"abc","cmd":"rm -rf /data","agent":"backend"}\n
daemon → gate:   {"decision":"block","reason":"destructive command"}\n
```

Di Go, `json.NewEncoder` otomatis append newline, `json.NewDecoder` blocking sampai ada data:

```go
// Kirim — satu baris JSON + newline otomatis
json.NewEncoder(conn).Encode(req)

// Terima — blocking sampai daemon tulis jawaban
json.NewDecoder(conn).Decode(&resp)
```

### 4.3 Keamanan

```
❌ /tmp/wick.sock        — /tmp world-writable, proses lain bisa connect
✅ ~/.wick/sessions/<id>/gate.sock  — direktori chmod 700, hanya owner
```

```go
ln, _ := net.Listen("unix", socketPath)
os.Chmod(socketPath, 0600)  // hanya owner bisa read/write socket ini
```

Kalau mau lebih ketat, bisa verify peer credentials (`SO_PEERCRED`) untuk pastikan hanya gate sidecar dengan UID yang benar yang bisa connect — tapi untuk Wick, `chmod 0600` di session directory sudah cukup.

### 4.4 Lifecycle Socket File

```
Daemon start:
  1. os.Remove(socketPath)      ← hapus sisa run sebelumnya
  2. net.Listen("unix", path)   ← buat socket baru
  3. os.Chmod(path, 0600)       ← lock permission

Daemon running:
  ← terima koneksi masuk (goroutine per connection)

Daemon crash/stop:
  File socket tetap ada di disk tapi tidak bisa di-connect
  Gate: connect() → "connection refused" → fail-safe exit 2

Daemon restart:
  Step 1 hapus sisa → socket baru, tidak ada konflik
```

---

## 5. Flow Lengkap: Mid-Session Approval

### 5.1 Happy Path — User Approve

```
Claude (PID 1234)        gate sidecar       daemon         User (Web)
      │                      │                 │               │
      │ mau jalankan         │                 │               │
      │ "git clone ABC"      │                 │               │
      ├──fork────────────────►                 │               │
      │  (nunggu exit code)  │                 │               │
      │                      ├──connect────────►               │
      │                      ├──{"id":"x",     │               │
      │                      │   "cmd":"git"}──►               │
      │                      │  (BLOCK di sini)│               │
      │                      │                 ├──SSE event────►
      │                      │                 │               │ render modal:
      │                      │                 │               │ "Approve git clone?"
      │                      │                 │               │ [Approve] [Block]
      │                      │                 │◄──POST /approve┤
      │                      │                 │  {"decision":  │
      │                      │                 │   "approve"}   │
      │                      │◄──{"decision":  │               │
      │                      │    "approve"}───┤               │
      │◄──exit 0─────────────┤                 │               │
      │                      │                 │               │
      │ git clone ABC jalan  │                 │               │
```

### 5.2 User Block

Sama sampai modal muncul, user klik Block:

```
      │◄──{"decision":"block"}──┤
      │◄──exit 2────────────────┤
      │
      │ [tool blocked]
      │ Claude: "Command blocked by user"
```

### 5.3 Timeout (User Tidak Respond)

```
Daemon set deadline 25 detik (< hook timeout 30 detik Claude)
Setelah 25 detik:
  daemon → {"decision":"block","reason":"timeout"}
  gate → exit 2
  Claude: "Command blocked (timeout)"
```

### 5.4 Daemon Tidak Jalan

```
gate: connect() → "no such file" atau "connection refused"
gate: fail-safe → exit 2 (block semua)
Claude: "Command blocked"
```

---

## 6. Web UI: Dua Jenis Interaksi

Web UI Wick perlu handle **dua jenis interaksi yang berbeda** yang keduanya muncul dari SSE stream.

### 6.1 Gate Approval (Baru)

Dipicu saat gate sidecar mengirim request ke daemon. Daemon broadcast SSE event dengan tipe baru.

**SSE event dari daemon:**

```json
{
  "session_id": "sess_xyz",
  "agent_name": "backend",
  "type": "approval_request",
  "data": "{\"id\":\"abc123\",\"cmd\":\"rm -rf /data\",\"tool\":\"Bash\",\"work_dir\":\"/home/user/project\"}"
}
```

**Yang perlu dirender:** modal/card menampilkan command, agent, work dir, countdown timer, plus 4 tombol decision (lihat §6.1.1).

**Response dari UI:** `POST /api/agents/sessions/{id}/approve` dengan `{"id":"abc123","decision":"<mode>"}`.

**Timing:** harus dijawab dalam 25 detik atau otomatis di-block oleh daemon.

#### 6.1.1 Decision Modes

User punya empat pilihan saat modal muncul. Tiga di antaranya = approve, satu block. Mode beda di scope memori-nya.

| Decision | API value | Scope | Future requests yang sama |
|---|---|---|---|
| **Approve once** | `approve_once` | Cuma request ini | Tetap muncul modal |
| **Allow this session** | `approve_session` | Sepanjang session hidup (sampai session deleted/restart) | Auto-approve, tidak muncul modal |
| **Always allow** | `approve_always` | Persistent (tersimpan di workspace/general config) | Auto-approve di semua session sekarang & masa depan |
| **Block** | `block` | Cuma request ini | Tetap muncul modal |

**Match key** (untuk auto-approve di session/always): hash dari `(tool, normalized_cmd_pattern)`. Pattern normalization = strip args yg bersifat data (file paths, URLs) tapi keep root command — supaya `git status` dan `git status -s` di-treat sebagai pattern berbeda kalau user tepat. MVP keep simple: exact-string match dulu, pattern engine ditunda.

**Storage:**
- `approve_session` → in-memory map di daemon, key `sessionID + matchKey`, hilang saat daemon restart
- `approve_always` → `gate/spec.json` field `auto_approved: ["<matchKey>", ...]` → diisi ulang saat gate spec di-rewrite, jadi gate binary sendiri yg auto-allow tanpa round-trip ke daemon (zero latency)

**Revocation:** UI `/tools/agents/sessions/{id}` punya panel "Approved commands" — list semua entry session + always, tombol Revoke per item.

### 6.2 AskUser dari Agent (Web Flow)

Wick sediakan `AskUser` sebagai **MCP tool** (bukan harness tool) sehingga tersedia di pipe mode (`-p`) untuk semua CLI yang attach ke wick MCP. Agent panggil tool ini saat butuh input dari user; tool block sampai user balas via web UI.

#### 6.2.1 Mekanisme

```
Agent panggil MCP tool "ask_user" dengan {question, options[]}
  → MCP handler register pending question di daemon (UUID + channel)
  → broadcast SSE: {type: "ask_user", data: {id, question, options}}
  → Web UI render card di session detail (composer area)
  → User pilih option / ketik free text → POST /sessions/{id}/answer {id, answer}
  → daemon resolve channel → MCP tool return jawaban ke agent
  → agent lanjut turn dengan jawaban sebagai tool result
```

Beda dari gate approval: **tidak ada hook subprocess**, tidak ada exit code. Murni MCP request/response yg di-bridge ke web via SSE.

#### 6.2.2 SSE Event

```json
{
  "session_id": "sess_xyz",
  "agent_name": "backend",
  "type": "ask_user",
  "data": "{\"id\":\"q_abc123\",\"question\":\"Pakai PostgreSQL atau MySQL?\",\"options\":[{\"label\":\"Postgres\",\"value\":\"pg\"},{\"label\":\"MySQL\",\"value\":\"mysql\"}],\"allow_freeform\":true}"
}
```

#### 6.2.3 Response

`POST /api/agents/sessions/{id}/answer` dengan `{"id":"q_abc123","answer":"pg"}` (atau `{"id":"q_abc123","answer_text":"...freeform..."}`).

#### 6.2.4 Timeout

Default 5 menit (config-able). Lewat timeout → MCP tool return error `"user did not respond"` → agent boleh decide retry / abort.

### 6.3 Perbedaan Dua Interaksi di UI

| | Gate Approval | AskUser |
|---|---|---|
| **Trigger** | SSE `type: approval_request` | SSE `type: ask_user` |
| **Sumber** | gate sidecar hook (subprocess) → daemon socket | MCP tool `ask_user` dipanggil agent |
| **Deadline** | 25 detik (sebelum hook timeout 30s) | 5 menit (config-able) |
| **Response ke** | `POST /approve` → daemon → unblock gate (exit 0/2) | `POST /answer` → daemon → unblock MCP tool return |
| **Agent state** | Mid-turn, tool execution di-pause | Mid-turn, tool execution di-pause (MCP tool block) |
| **Visual** | Modal full-screen dgn countdown | Card inline di area composer |
| **Bisa diabaikan?** | Tidak (auto-block setelah timeout) | Tidak (timeout → agent dapat error) |
| **Channel komunikasi** | Unix socket (gate↔daemon) | MCP request/response (agent↔daemon) |

### 6.4 Existing SSE Infrastructure

Wick sudah punya `Broadcaster` di `internal/tools/agents/stream.go` yang fan-out events ke semua SSE subscriber. Event shape yang sudah ada:

```go
type Event struct {
    SessionID string `json:"session_id"`
    AgentName string `json:"agent_name"`
    Type      string `json:"type"`   // existing: "text", "tool_use", "result", dll.
    Data      string `json:"data"`
}
```

Untuk fitur baru, cukup tambah tipe event dan publish via broadcaster yang sama:

| Type | Sumber | Tujuan |
|---|---|---|
| `approval_request` | Daemon (saat gate connect) | UI render modal approval |
| `approval_resolved` | Daemon (saat decision masuk) | UI dismiss modal di semua tab |
| `ask_user` | MCP handler (saat tool dipanggil) | UI render card pertanyaan |
| `ask_user_resolved` | MCP handler (saat answer masuk) | UI dismiss card di semua tab |

Frontend tinggal handle tipe baru ini di SSE listener.

---

## 7. Struktur Data

### 7.1 Request: Gate → Daemon

```go
type ApprovalRequest struct {
    ID        string `json:"id"`         // UUID per request
    SessionID string `json:"session_id"`
    Agent     string `json:"agent"`      // "backend", dll.
    Tool      string `json:"tool"`       // "Bash", "Edit", dll.
    Cmd       string `json:"cmd"`        // command yang mau dieksekusi
    WorkDir   string `json:"work_dir"`   // cwd saat eksekusi
    Timestamp int64  `json:"ts"`         // unix ms
}
```

### 7.2 Response: Daemon → Gate

```go
type ApprovalResponse struct {
    ID       string `json:"id"`       // sama dengan request ID
    Decision string `json:"decision"` // "approve" atau "block"
    Reason   string `json:"reason"`   // opsional
}
```

### 7.3 State Machine di Daemon

```
[idle]
  │
  │ gate connect + send request
  ▼
[pending] ─── 25s timeout ──────────────────────► [auto-block]
  │                                                     │
  │ user klik Approve                                   │
  ▼                                                     │
[approved]                                             │
  │                                                     │
  └──────────────────────────────────────────────────── ┘
                        │
                        ▼
              tulis response ke socket
              broadcast SSE "approval_resolved"
              hapus dari pending map
                        │
                        ▼
                     [idle]
```

**Concurrent requests** — daemon pegang banyak pending sekaligus dengan `sync.Map` + channel per connection:

```go
type pendingApproval struct {
    req ApprovalRequest
    ch  chan ApprovalResponse
}

var pending sync.Map // map[id]pendingApproval

// per goroutine (satu per koneksi gate):
ch := make(chan ApprovalResponse, 1)
pending.Store(req.ID, pendingApproval{req, ch})
defer pending.Delete(req.ID)

select {
case resp := <-ch:
    json.NewEncoder(conn).Encode(resp)
case <-time.After(25 * time.Second):
    json.NewEncoder(conn).Encode(ApprovalResponse{
        Decision: "block", Reason: "timeout",
    })
}
```

---

## 8. Release: Dua Binary

Wick saat ini punya satu binary utama. Untuk mid-session approval, perlu ship **dua binary**:

| Binary | Fungsi |
|---|---|
| `<app>` (mis. `myapp[.exe]`) | Server daemon, web UI, semua logic utama. Embed gate sebagai bytes via `//go:embed` |
| `<app>-gate` (mis. `myapp-gate[.exe]`) | Hook binary kecil, dipanggil Claude sebelum Bash. Branded per project via ldflag |

Dari sisi user: download satu installer (.msi/.deb/.dmg) — main app yang ke-install, gate ke-extract runtime dari embed. `bin/<app>-gate-<os>-<arch>[.exe]` di output `wick build` adalah artifact debug/dev (sibling fallback), bukan kewajiban ship.

### 8.1 Bagaimana Build System Wick Bekerja

Wick pakai `internal/builder` — satu package yang handle compile + packaging per platform:

```
wick build              → compile binary + .dmg/.deb/.exe
wick build --installer  → tambah .msi (Windows) / Applications symlink (macOS)
wick build --all        → semua target (windows/amd64, windows/arm64, linux/*, darwin/*)
```

Flow `builder.Build()`:
1. Generate assets (templ + CSS + go generate)
2. Windows: embed icon + version metadata via `.syso` sebelum compile
3. `go build -ldflags "..."` → raw binary
4. Package per platform: `.app`+`.dmg` (macOS), `.deb` (Linux), `.msi` (Windows, opt-in)

### 8.2 Strategi: Embed gate ke Main Binary ✅ DIPILIH

Gate di-compile dulu untuk platform target, lalu di-embed sebagai bytes di dalam main binary via `//go:embed`. Saat daemon start pertama kali per session, binary di-extract ke session directory.

Asset name di embed sengaja **generic** (`gate-<os>-<arch>`) — branding (`<app>-gate`) cuma untuk file user-visible di `bin/` dan sibling/PATH lookup. Internal extract path juga generic (`<session>/gate/gate[.exe]`).

```go
//go:embed all:assets
var embeddedGateFS embed.FS

// AppName injected via ldflag at build time.
var AppName = ""

func extractEmbeddedGate(sessionDir string) (string, error) {
    name := fmt.Sprintf("assets/gate-%s-%s", runtime.GOOS, runtime.GOARCH)
    if runtime.GOOS == "windows" {
        name += ".exe"
    }
    data, err := embeddedGateFS.ReadFile(name)
    if err != nil {
        return "", fmt.Errorf("embedded gate not found for %s/%s", runtime.GOOS, runtime.GOARCH)
    }
    out := filepath.Join(sessionDir, "gate", "gate")
    if runtime.GOOS == "windows" {
        out += ".exe"
    }
    if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
        return "", err
    }
    if err := os.WriteFile(out, data, 0755); err != nil {
        return "", err
    }
    return out, nil
}
```

Keuntungan:
- User download satu file — tidak ada binary terpisah yang bisa ketinggalan
- Version selalu sinkron (gate di-compile bersama main binary)
- MSI tidak perlu diubah sama sekali — `msi.go` tetap ship satu `.exe`
- `.deb`, `.dmg`, raw binary — semua sama, tidak ada perubahan

Trade-off:
- Main binary sedikit lebih besar (~2-5MB per platform yang di-embed)
- Hanya embed gate untuk platform yang di-build (bukan semua platform sekaligus)

> Opsi yang tidak dipilih: sidecar binary (dua file terpisah di MSI → risiko version mismatch) dan subcommand `wick gate` (load binary besar untuk proses kecil yang dipanggil ratusan kali per session).

### 8.3 Build Pipeline (terintegrasi di builder)

Tidak ada step CI tambahan. `wick build` di [internal/builder/gate.go](../builder/gate.go) handle gate compile inline:

```
wick build [--installer] [--target <os>/<arch>]
  ↓
buildGateBinary(cfg)
  ├── go build -trimpath -ldflags "-s -w" \
  │       -o internal/agents/gate/assets/gate-<os>-<arch>[.exe] \
  │       github.com/yogasw/wick/cmd/gate         (embed asset, picked up by //go:embed)
  └── copy to bin/<app>-gate-<os>-<arch>[.exe]    (user-visible sidecar)
  ↓
runGoBuild(cfg, ldflags)                          (compiles main + embeds gate bytes)
  ↓
package per platform (.dmg/.deb/.msi)
```

Gate adalah pure Go (no CGO) → cross-compile aman dengan `CGO_ENABLED=0`. `wick build --all` iterate semua target, gate di-compile cross sesuai matrix.

**Soft-skip pada downstream fork**: kalau `cmd/gate` tidak resolvable (mis. fork yang prune-nya), `gateModuleAvailable()` return false, builder cetak warning + lanjut compile main tanpa embed. Runtime fallback ke sibling/PATH lookup.

Template release workflow (`template/.github/workflows/release.yml`) sekarang **bersih** dari gate-specific step — cukup `wick build --installer`.

### 8.4 Template Downstream

Proyek downstream yang pakai Wick sebagai framework:
- Tidak perlu `cmd/gate/` di tree mereka — `wick build` resolve dari module cache via import path `github.com/yogasw/wick/cmd/gate`
- CI workflow tidak butuh step build gate — builder handle inline
- `wick build --installer` produces 1 main binary (gate embedded) + 1 sidecar di `bin/`

---

## 9. Resolve Gate Binary per Environment

Ada tiga environment dengan cara berbeda untuk menemukan gate binary:

```
Environment           Gate binary dari mana                  Cara set
─────────────────────────────────────────────────────────────────────────────
VSCode (wicklab)   →  bin/<app>-gate[.exe] (sibling)      →  otomatis (sibling discovery)
Serve (raw binary) →  bin/<app>-gate[.exe] (sibling)      →  otomatis
                  ↳   atau embed extract (kalau sibling absent)
MSI (installer)    →  %LocalAppData%\Programs\<App>\<App>-gate.exe (sibling, shipped) → otomatis
.deb (installer)   →  /usr/bin/<app>-gate (sibling, shipped) →  otomatis
.app (installer)   →  Contents/MacOS/<App>-gate (sibling, shipped) → otomatis
```

### 9.1 Logic Resolve di Daemon

```go
// AppName injected via ldflag (`-X .../gate.AppName=<app>`).
// Empty → fallback "gate" / "<app>-gate" sesuai konteks.

func ResolveGateBinaryWithSource(sessionDir string) (path, source string, err error) {
    // 1. Sibling-of-executable: <app>-gate[.exe] di samping parent
    //    (production path — installer ships this).
    if p := siblingGateBinary(); p != "" {
        return p, "sibling", nil
    }
    // 2. Embedded asset → extract ke session dir (idempotent).
    //    Backup untuk portable .exe / source build.
    if p, err := extractEmbeddedGate(sessionDir); err == nil {
        return p, "embed", nil
    }
    // 3. PATH lookup — last-ditch fallback.
    name := "gate"
    if AppName != "" {
        name = AppName + "-gate"
    }
    if p, err := exec.LookPath(name); err == nil {
        return p, "path", nil
    }
    return "", "", fmt.Errorf("gate binary %q not found", name)
}
```

Urutan prioritas: **sibling-of-executable** (`<app>-gate[.exe]` di folder yang sama dgn parent binary — installer ships this) → **embedded extract** (backup) → `<app>-gate` di PATH. Tidak ada env-var override — Stage 9 hapus `GATE_BIN` setelah installer mulai bundle sidecar (sibling reliable cukup tanpa knob ekstra). Kalau semuanya gak ada → gate tidak aktif, log warning, commands lolos semua (fail-open, logged).

### 9.2 VSCode (wicklab)

**Launch config:** `.vscode/launch.json` → `wicklab` → `preLaunchTask: "debug: prep"`

Cara kerjanya simpel — gak ada launch khusus untuk gate, karena gate selalu di-spawn oleh claude (anak wicklab) saat command perlu di-approve.

#### Setup

`debug: prep` task build dua binary ke `bin/`. Nama gate sidecar mengikuti `<app>-gate` (branded via ldflag waktu compile parent), jadi sibling lookup picks it up tanpa env:

```json
{
  "label": "debug: prep",
  "type": "shell",
  "command": "templ generate ./... && bin/tailwindcss.exe -i web/src/input.css -o web/public/css/app.css && go build -ldflags=\"-X github.com/yogasw/wick/internal/agents/gate.AppName=wicklab\" -o bin/wicklab-gate.exe ./cmd/gate",
  "problemMatcher": []
}
```

Saat F5 `wicklab`:
- VSCode build wicklab dgn ldflag `gate.AppName=wicklab` → `bin/wicklab.exe`
- `debug: prep` udah build dgn ldflag yang sama → `bin/wicklab-gate.exe`
- Hasilnya: keduanya satu folder, brand match

Saat wicklab boot panggil `gate.ResolveGateBinary`, sibling-of-executable check langsung pickup `bin/wicklab-gate.exe` — tanpa env var, tanpa task tambahan.

#### Cara Debug Gate

Gate sidecar **gak bisa di-debug standalone** dgn launch terpisah, karena dia stateless forwarder yg butuh `GATE_SPEC` env (di-inject parent). Pakai salah satu cara berikut:

**1. Debug via test** (paling praktis)

Buka [internal/agents/gate/integration_test.go](../agents/gate/integration_test.go) atau [cmd/gate/main_test.go](../../../cmd/gate/main_test.go), set breakpoint di [main.go:run()](../../../cmd/gate/main.go), lalu right-click test function → "Debug Test". Test sudah set spec + env + stdin secara realistic.

**2. Logs**

Gate tulis decision ke `commands.jsonl` di session dir:

```
~\.wick\agents\sessions\<id>\commands.jsonl
```

Tail file itu sambil F5 wicklab + trigger command via web UI.

**3. Attach to process** (rare)

Gate hidup cuma milidetik per call, susah caught. Hanya berguna untuk kasus stuck (socket timeout dll).

#### Flow normal (no gate debugging)

```
1. F5 → "wicklab"                 → daemon jalan + bin/wicklab-gate.exe ready
2. Buat session di web UI          → wicklab tulis spec.json + start socket listener
3. Kirim pesan ke claude di web UI → claude jalan, command picu gate sidecar
4. Gate gak whitelisted → modal approval muncul di web UI
5. Klik salah satu (approve_once / session / always / block)
```

### 9.3 MSI (Windows Installer)

Dibangun via `wick build --installer`. Flow:

```
wick build --installer (one shot)
  ├── builder.buildGateBinary
  │     ├── go build → internal/agents/gate/assets/gate-windows-amd64.exe   (embed asset)
  │     └── copy to bin/<app>-gate-windows-amd64.exe                        (sidecar)
  ├── go build main (embeds gate via //go:embed)
  └── wixl → .msi (satu binary, gate sudah di dalam main)
```

Di-install ke `%LocalAppData%\Programs\<AppName>\<AppName>.exe` PLUS `<AppName>-gate.exe` di folder yang sama. Sibling lookup picks up the installed gate — no env vars, no per-session extract.

### 9.4 Serve (Raw Binary / Linux / Docker)

Binary dari `wick build` tanpa `--installer`, atau `.deb`, atau Docker image. Sama dengan MSI dari sisi gate: embedded, di-extract ke `~/.<app>/agents/sessions/<id>/gate/gate` saat session start (path internal generic — branding hanya untuk file user-visible).

```
docker run myapp server     → gate di-extract dari embed otomatis
./myapp server              → sama
systemctl start myapp       → sama
```

Tidak ada konfigurasi tambahan yang diperlukan.

### 9.5 Perbandingan Tiga Environment

| | VSCode (wicklab) | Serve / raw binary | MSI |
|---|---|---|---|
| **Gate binary dari** | `bin/<app>-gate[.exe]` (sibling-of-exe) | Embedded → extracted | Embedded → extracted |
| **Cara set** | Otomatis (sibling discovery) | Otomatis (embed extract) | Otomatis (embed extract) |
| **Perlu build manual?** | Ya (via `debug: prep` task) | Tidak | Tidak |
| **Version sync** | Manual (rebuild saat ada perubahan) | Selalu sync (embedded saat compile) | Selalu sync |
| **File yang perlu diedit** | `.vscode/tasks.json` saja | Tidak ada | Tidak ada |

### 9.6 Template Downstream

Proyek yang pakai Wick sebagai framework perlu:

1. **Tidak perlu** `cmd/gate/` di tree — `wick build` resolve dari module cache (import path `github.com/yogasw/wick/cmd/gate`)
2. `.env.example` — tidak butuh entry gate (env var dihapus Stage 9; resolution otomatis)
3. `.vscode/tasks.json` — tambah gate build ke `debug: prep` (kalau mau debug local)
4. CI workflow — **tidak perlu** step gate; `wick build --installer` handle inline

---

## 10. Lokasi File di Filesystem (Runtime)

```
~/.<app>/agents/sessions/<session-id>/
  ├── meta.json                  ← session metadata
  ├── agents.json                ← agent list + CLI session ID
  ├── commands.jsonl             ← audit log semua command
  └── gate/
      ├── spec.json              ← rules whitelist untuk gate
      ├── settings.json          ← Claude hook config (PreToolUse → gate sidecar)
      └── gate.sock              ← Unix domain socket
                                    dibuat saat daemon start, chmod 0600
                                    dihapus saat daemon stop
```

Embed extract (default flow di MSI / serve / docker):

```
~/.<app>/agents/sessions/<session-id>/gate/
  └── gate[.exe]                 ← di-extract dari embedded binary saat start
                                    chmod 0755, idempotent (skip kalau size match)
                                    nama generic — user gak lihat path internal ini
```

Output `wick build` di `bin/`:

```
bin/
  ├── <app>-<os>-<arch>[.exe]            ← main app, gate ke-embed
  └── <app>-gate-<os>-<arch>[.exe]       ← sidecar, branded — debug aid / sibling fallback
```

---

## 11. Keputusan Desain

| # | Keputusan | Alasan |
|---|---|---|
| D1 | Pakai Unix socket, bukan HTTP | Tidak ada network exposure, performa lebih baik, akses dikontrol filesystem |
| D2 | Socket path di session directory | Direktori sudah chmod 700, isolasi per session, tidak perlu auth tambahan |
| D3 | Raw JSON newline-delimited, bukan HTTP | Tidak ada overhead parsing HTTP header, protokol lebih simpel |
| D4 | Timeout 25 detik di daemon (< hook timeout 30 detik) | Pastikan gate sempat exit bersih sebelum Claude timeout |
| D5 | Fail-safe: block kalau daemon tidak respond | Lebih aman default block daripada default allow |
| D6 | Pending state: `sync.Map` + channel per koneksi | Concurrent safe, goroutine per koneksi, no mutex contention |
| D7 | Gate binary tetap stateless | Semua state di daemon. Gate bisa crash/respawn tanpa kehilangan pending |
| D8 | Embed gate ke binary utama (rekomendasi) | User satu file, version selalu sync, tidak perlu installer logic baru |
| D9 | Broadcast approval_request via Broadcaster yang sudah ada | Tidak perlu infrastruktur SSE baru, cukup tambah tipe event |
| D10 | ~~`GATE_BIN` env var override untuk dev~~ → DROPPED | Awalnya ada untuk skenario "dev binary tidak punya embed". Setelah installer mulai ship sidecar (D20), sibling lookup cover semua path penting; embed extract jadi backup untuk portable .exe / source build. Env var jadi noise — dihapus untuk konsistensi "no knobs" Stage 9 |
| D11 | `debug: prep` task build gate otomatis | Developer tidak perlu ingat build gate manual sebelum debug — F5 langsung siap |
| D12 | Drop `gate: sync-spec` task + `envFile`; operator set `GATE_SPEC` manual | Gate cuma baca env var (tidak ada home-dir discovery), jadi sebelumnya pakai task yang tulis path session terbaru ke `bin/.gate-debug.env` + envFile launch. Trade-off: 1 langkah manual vs ~30 baris tooling untuk save 5 detik per debug session. Pilih simpel — debug gate jarang, dan eksplisit lebih mudah di-troubleshoot kalau mismatch path |
| D13 | Drop `wicklab-gate` launch entirely + tambah sibling-of-executable resolution | |
| D14 | Rename `cmd/wick-gate` → `cmd/gate`, env vars `WICK_GATE_*` → `GATE_*`, output user-visible jadi `<app>-gate` (branded) | Brand neutrality di source; per-app brand di runtime — downstream user lihat `myapp-gate.exe` bukan `wick-gate.exe`. Embed asset internal tetap generic (`gate-<os>-<arch>`), branding via ldflag `gate.AppName` |
| D15 | Hapus `GATE_SPEC` env var | DONE Stage 9. Env var rawan tidak ter-set → gate error → fail-safe block semua. Compile-time path lebih reliable. Sekarang gate derive `~/.<app>/agents/gate/spec.json` dari `gate.AppName` ldflag |
| D16 | `AppName` via `-ldflags` waktu build | Baked saat compile, tidak ada runtime guessing. Builder inject otomatis (`-X .../gate.AppName=<app>`) → downstream tidak perlu setup ldflag manual |
| D17 | Path convention: shared `~/.<AppName>/agents/gate/{spec,gate.sock,commands}.json/jsonl` | Pre-Stage 9 per-session paths digabung jadi single shared per-app. UI session-detail Commands tab filter by workspace cwd prefix. Trade-off: per-session always-allow scope hilang (gantinya per-app); multi-session paralel berbagi rules + audit log |
| D18 | Build gate inline di `internal/builder` (drop dari CI) | Lokal `wick build` & CI downstream produce hasil sama (1 main self-contained + 1 sidecar). Template release.yml jadi minus 1 step. Soft-skip pada downstream fork yang prune `cmd/gate` |
| D19 | Stage 9: gate session-agnostic, daemon route by cwd | Gate binary tidak tau wick session. ApprovalRequest carries cwd; daemon scan active sessions (longest workspace-path prefix wins) untuk dapat sessionID buat SSE broadcast. Empty bucket kalau cwd di luar semua workspace — UI render under "unrouted" |
| D20 | Installer ships gate sidecar (MSI + .deb + .app) | Sibling-of-executable jadi primary resolution path. Embedded extract demoted ke backup (portable .exe / source build). Trade: installer ukuran +~5MB per platform; benefit: gate visible di Programs folder, no per-session extract churn, lookup deterministic. Dengan ini D10 (`GATE_BIN` env var) jadi redundant → dihapus |

---

## 12. Checklist Implementasi (Detail)

Versi ringkas (just the boxes) di section paling atas dokumen. Sini detail per task + exit criteria. Urutan **timeline-aware**: tiap stage hanya butuh stage sebelumnya. Bisa di-pause di akhir stage manapun dan tetap shippable (gate fallback ke whitelist-mode kalau socket belum ada).

### Stage 1 — Spec & Wiring Foundation

Tujuan: gate spec siap menampung field baru (socket path, auto-approved). Tidak ada perubahan runtime behavior.

```
[ ] S1.1 Tambah field di gate.Spec: SocketPath string, AutoApproved []string
         → internal/agents/gate/spec.go
[ ] S1.2 gate.WriteSpawnArtifacts: tulis SocketPath = <sessionDir>/gate/gate.sock
         → internal/agents/gate/claude_hook.go
[ ] S1.3 Wire Gate di factory.go (saat ini Gate field masih nil di FactoryOptions)
         inject GateConfig + spawn artifact write per session
         → internal/agents/pool/factory.go
[ ] S1.4 Unit test spec marshal + artifact write pakai t.TempDir()
         → internal/agents/gate/{spec,claude_hook}_test.go
```

**Exit criteria**: gate sidecar baca spec.json yang sudah punya socket_path field; behavior tetap whitelist-only (socket belum dipakai).

### Stage 2 — Daemon Socket Listener

Tujuan: daemon expose socket per session, terima konek tapi belum ada UI — auto-block semua request (smoke test only).

```
[ ] S2.1 Unix socket listener per session, dibuat saat session start, chmod 0600
         path: ~/.wick/agents/sessions/<id>/gate/gate.sock
         → internal/agents/gate/socket.go (paket baru atau extend gate/)
[ ] S2.2 Cleanup: os.Remove socket saat session stop / daemon shutdown
[ ] S2.3 Goroutine per koneksi: read JSON request, send JSON response (raw newline-delimited)
[ ] S2.4 Pending state manager: sync.Map[id]chan ApprovalResponse
[ ] S2.5 Timeout goroutine: 25s → auto-block kalau tidak ada decision
[ ] S2.6 Test: dial socket dari fake gate, kirim ApprovalRequest, expect timeout=block
         → internal/agents/gate/socket_test.go
```

**Exit criteria**: `nc -U gate.sock` bisa konek, kirim JSON dummy, dapat `{"decision":"block","reason":"timeout"}` setelah 25s.

### Stage 3 — Gate Binary Upgrade

Tujuan: gate sidecar konek ke socket sebelum decide. Fallback ke whitelist + block jika socket tidak ada.

```
[ ] S3.1 Gate binary baca SocketPath dari spec, dial unix socket
         → cmd/gate/main.go
[ ] S3.2 Cek auto_approved list di spec → kalau match, langsung exit 0 tanpa round-trip
         (zero-latency path untuk "always allow")
[ ] S3.3 Build ApprovalRequest, encode JSON, kirim ke socket
[ ] S3.4 Decode ApprovalResponse → exit 0 (approve_*) atau 2 (block)
[ ] S3.5 Fail-safe: socket connect refused / timeout → exit 2 (block)
[ ] S3.6 Integration test: spawn gate subprocess dgn fake socket server
         → cmd/gate/main_test.go (extend existing)
```

**Exit criteria**: gate binary cocok dgn socket flow + auto_approved short-circuit; existing whitelist tests masih hijau.

### Stage 4 — Embed + Binary Resolution

Tujuan: production binary ship gate di dalamnya, dev pakai `GATE_BIN` env.

```
[x] S4.1 //go:embed all:assets di package gate (asset name: gate-<os>-<arch>[.exe])
         → internal/agents/gate/embed.go
[x] S4.2 extractEmbeddedGate(sessionDir) — extract ke <session>/gate/gate[.exe],
         chmod 0755, idempotent (skip kalau size match)
[x] S4.3 ResolveGateBinaryWithSource: GATE_BIN env → embed extract →
         sibling-of-exe (<app>-gate, branded via gate.AppName ldflag) → PATH
[x] S4.4 Wire ResolveGateBinary ke server.go (resolved at boot + per-spawn)
[x] S4.5 Builder compile gate inline via internal/builder/gate.go
         (drop step manual dari template release.yml; soft-skip pada downstream fork)
```

**Exit criteria**: raw binary dari `wick build` berhasil spawn agent + extract gate ke session dir tanpa env var apapun.

### Stage 5 — Web UI: Approval Modal + 4 Modes

Tujuan: user lihat modal saat command butuh approval, klik salah satu dari 4 decision.

```
[ ] S5.1 SSE event type "approval_request" + "approval_resolved" via existing Broadcaster
         → internal/tools/agents/stream.go
[ ] S5.2 Backend endpoint POST /api/agents/sessions/{id}/approve
         body: {"id":"...","decision":"approve_once|approve_session|approve_always|block"}
         → resolve pending channel di daemon
         → internal/tools/agents/handler.go
[ ] S5.3 approve_session: store di in-memory sessionApprovals map[sessionID][]matchKey
         next request match → daemon auto-resolve tanpa SSE broadcast
[ ] S5.4 approve_always: append matchKey ke spec.AutoApproved + rewrite spec.json
         → gate binary handle short-circuit dari Stage 3.2
[ ] S5.5 Web UI modal: render dari SSE, countdown timer 25s, 4 tombol decision
         → internal/tools/agents/view/approval.templ + js/agents.js
[ ] S5.6 matchKey hash: simple hash(tool + cmd) untuk MVP, exact match
         → internal/agents/gate/matchkey.go
[ ] S5.7 "Approved commands" panel di session detail: list session+always entries,
         tombol Revoke per item → DELETE /api/agents/sessions/{id}/approve/{matchKey}
[ ] S5.8 Smoke test manual: claude jalanin command non-whitelisted → modal muncul →
         klik "Allow this session" → command kedua yang sama auto-approve
```

**Exit criteria**: user bisa Approve once / session / always / Block dari web; revoke jalan; auto_approved persist setelah daemon restart.

### Stage 6 — AskUser MCP Tool + Web Card

Tujuan: agent bisa tanya user via MCP tool, web UI render card jawaban.

```
[ ] S6.1 MCP tool "ask_user" register di wick MCP server
         input schema: {question: string, options?: [{label, value}], allow_freeform?: bool}
         → internal/tools/agents/mcp_askuser.go
[ ] S6.2 Tool handler: register pending question (UUID + chan), broadcast SSE,
         block sampai POST /answer atau timeout 5 menit
[ ] S6.3 SSE event type "ask_user" + "ask_user_resolved"
[ ] S6.4 Backend endpoint POST /api/agents/sessions/{id}/answer
         body: {"id":"...","answer":"<value>"} atau {"answer_text":"..."}
[ ] S6.5 Web UI card: render inline di composer area, klik option → POST answer
         → internal/tools/agents/view/askuser.templ + js/agents.js
[ ] S6.6 Smoke test: agent panggil ask_user → card muncul di web → user pilih →
         agent terima jawaban di tool result
```

**Exit criteria**: claude bisa pakai `ask_user` MCP tool, jawaban user dari web masuk balik ke turn yang sama.

### Stage 7 — Dev Tooling

Tujuan: developer flow F5 di VSCode jalan tanpa langkah manual.

```
[x] S7.1 .vscode/tasks.json: extend "debug: prep" — tambah go build cmd/gate ke
         bin/<app>-gate[.exe] dgn ldflag gate.AppName=<app>
[-] S7.2 task "gate: sync-spec" — DROPPED (operator set $env:GATE_SPEC manual)
[x] S7.3 .vscode/launch.json: launch "wicklab-gate" (no envFile — lebih simpel)
[x] S7.4 .env.example: GATE_BIN entry sudah ada
[ ] S7.5 .vscode/launch.json: compound "wicklab + gate" (opsional)
[ ] S7.6 Doc snippet: developer flow F5 → wicklab → buat session →
         set $env:GATE_SPEC → F5 wicklab-gate → paste payload → breakpoint
```

**Exit criteria**: F5 wicklab + wicklab-gate jalan, dengan satu langkah manual yang explicit (set `$env:GATE_SPEC` di terminal sebelum F5 wicklab-gate).

### Stage 9 — Spec Resolution Refactor + Cleanup Pass

Tujuan: hapus seluruh runtime knob (env vars) dan konsolidasi state per-app. Gate resolve semua path dari `AppName` compile-time constant; daemon route by cwd.

**Keputusan (2026-05-10):** lihat D15–D20 di §11.

```
Core (S9.1–S9.7) — single shared spec, drop GATE_SPEC env var:
[x] S9.1 Hapus GATE_SPEC env var dari gate.LoadSpec(); path derived dari
         AppName ldflag → ~/.<app>/agents/gate/spec.json
[x] S9.2 gate.AppName ldflag di package gate (di-share antara binary lookup +
         shared paths). Default kosong → fallback "wick" di sharedGateDir
[x] S9.3 gate.LoadSpec(appName) → ~/.<app>/agents/gate/spec.json. Missing
         file = empty Spec, no error
[x] S9.4 Builder inject AppName via ldflags
         (`-X github.com/yogasw/wick/internal/agents/gate.AppName=<app>`)
         → internal/builder/ldflags.go
[x] S9.5 GATE_SPEC env var dihapus dari semua call sites:
         pool/factory.go (drop ExtraEnv inject), claude_hook.go (drop
         HookEnvVar const + LoadSpec env read), cmd/gate/main.go (drop env)
[x] S9.6 Unit tests refactored: pakai t.Setenv("HOME", ...) +
         WriteSharedSpec() daripada env var setup; integration_test build
         gate dgn ldflag AppName supaya HOME isolation propagated
[x] S9.7 Fail-safe: missing shared spec.json → empty Spec → fall through ke
         socket dial → no daemon = exit 2 (verified via
         TestGate_MissingSharedSpecIsEmpty)

Daemon refactor (S9.8–S9.11) — single shared listener, cwd routing:
[x] S9.8  ApprovalManager: single shared listener (Start/Stop, drop
          StartSession/StopSession). RouteByCWD callback maps hook cwd →
          session for SSE broadcast routing (longest workspace-path prefix)
[x] S9.9  commands.jsonl shared global (~/.<app>/agents/gate/commands.jsonl).
          UI session-detail Commands tab filters by workspace cwd prefix
[x] S9.10 Resolve approve_always rewrites SHARED spec; RevokeAlways same;
          AutoApproved() global; AutoApprovedFor(_) shim removed (caller pakai
          AutoApproved())
[x] S9.11 server.go boot syncSharedSpec — writes Rules from configsSvc on boot
          + every Build invocation, preserves existing AutoApproved

Post-merge cleanup pass (S9.12–S9.17) — installer + dead code:
[x] S9.12 Installer ship sidecar — MSI WXS GateExecutable component +
          PackageMSI gateExePath param; .deb /usr/bin/<app>-gate +
          PackageDeb gateBinPath param; .app Contents/MacOS/<App>-gate +
          PackageApp gateBinPath param; builder.go wire `gateArtifact` ke
          semua per-OS package functions
[x] S9.13 Drop GATE_BIN env var — sibling-of-executable jadi resolution
          path #1 (installer ships it), embed extract jadi backup, PATH
          last-ditch. envOverride const + SourceEnvOverride label dropped
[x] S9.14 Dead code di pool/factory.go — `gateAwareSpawner` + `extraEnv`
          + nil-tuple return dari `attachGateConfig` (artefak env-var
          injection era) semua dropped. Signature: `(Spawner, error)`
[x] S9.15 Redundancy di server.go — `resolveGateBin` local helper dropped
          (duplicate `agentgate.ResolveGateBinaryWithSource`); GateLoader
          pakai `resolvedGateBin`. `gateAppName` fallback redundant
          (sharedGateDir handles "" → "wick"). `os/exec` import dropped
[x] S9.16 Misc surface — `requestApproval` test wrapper di cmd/gate/main.go
          dropped (test pakai `requestApprovalWithLog(..., "")`).
          `PendingFor(_)` arg unused, kept signature untuk JSON view-model
[x] S9.17 UI copy — providers.go Note + approvals.go error + approvals.templ
          banner: drop "Set GATE_BIN/WICK_GATE_BIN" mentions, ganti dgn
          "Run `wick build` to produce the sibling sidecar and embedded
          fallback"
```

**Exit criteria**: gate sidecar jalan tanpa env var apapun. `go test ./...`
hijau (52 pass di gate/pool/cmd/gate/tools-agents). Gate fail-safe block
kalau shared spec / socket gak available. Installer ship sidecar; dev `go
run` cuma butuh `wick build` sekali dan sibling-of-exe lookup picks it up.

---


---

## 13. Nama Teknik & Referensi

Daftar istilah teknis yang dipakai dalam arsitektur ini beserta link dokumentasi primer.

### Naming

| Istilah | Arti dalam konteks wick |
|---|---|
| **Pre-execution Hook** | Hook yang fire sebelum tool dieksekusi — `PreToolUse` di Claude |
| **PEP** (Policy Enforcement Point) | Yang enforce keputusan → claude CLI |
| **PDP** (Policy Decision Point) | Yang bikin keputusan → gate sidecar (`<app>-gate`) |
| **Stateless ephemeral binary** | Binary tanpa state internal, semua via env/stdin/file, hidup detik-an |
| **HITL** (Human-in-the-loop) | Approval yang butuh keputusan manusia sebelum proses lanjut |
| **Allow-list / deny-by-default** | Hanya yang explicit di whitelist boleh; semua lainnya block |
| **Sidecar** | Proses pendamping kecil yang jalan parallel dengan proses utama |
| **bypassPermissions mode** | Claude mode yang matikan interactive TTY approval — hook jadi authority |

### Hook per CLI

| CLI | Nama Hook | Cara Block | Docs |
|---|---|---|---|
| Claude CLI | `PreToolUse` | exit code `2` | https://code.claude.com/docs/en/hooks-guide |
| Codex CLI | `PermissionRequest` | stdout JSON `{"behavior":"deny"}` | https://developers.openai.com/codex/hooks |
| Gemini CLI | `BeforeTool` | stdout JSON deny | https://geminicli.com/docs/hooks/ |

### Kenapa Pre-exec (bukan post-exec audit)?

- **Pre-exec**: command belum jalan saat hook fire — block = command tidak pernah jalan
- **Post-exec audit**: command sudah jalan, hook cuma rekam — blast radius sudah terjadi

### Kenapa Whitelist (bukan Blacklist)?

Blacklist mudah di-bypass: alias, path absolut (`/usr/bin/rm` vs `rm`), encoding (`r\m`), built-in vs binary. Whitelist + shell-metachar guard = surface area kecil, default deny.

### Bacaan Lanjutan

1. **Claude hooks-guide** — https://code.claude.com/docs/en/hooks-guide
2. **OPA sidecar PDP pattern** — https://www.openpolicyagent.org/docs/latest/
3. **OWASP Command Injection** — https://owasp.org/www-community/attacks/Command_Injection
4. **Slack interactivity** (untuk future Slack approval) — https://api.slack.com/interactivity/handling
5. **12-Factor Processes** — https://12factor.net/processes
6. **XACML PEP/PDP** — https://docs.oasis-open.org/xacml/3.0/xacml-3.0-core-spec-os-en.html