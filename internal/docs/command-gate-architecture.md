# Command Gate — Arsitektur & Approval System

Status: draft — arsitektur final, implementasi belum dimulai.
Update terakhir: 2026-05-09.

Keputusan final yang sudah locked:
- IPC: Unix Domain Socket (raw JSON, bukan HTTP)
- Gate binary: embed ke main binary via `//go:embed` (bukan sidecar/subcommand)
- Dev override: `WICK_GATE_BIN` env var di `.env`
- Approval style: Gate style (Pola A) — system intercept, bukan Claude Code style

---

## 0. TL;DR

**Command Gate** = mekanisme intercept shell command sebelum Claude mengeksekusinya. User bisa approve atau block command secara real-time tanpa restart session.

Dokumen ini menjelaskan:
- Kenapa gate diperlukan dan bagaimana cara kerjanya
- Perbandingan dua pola approval (Claude Code style vs Gate style)
- Perbandingan empat opsi IPC antara gate dan daemon
- Detail Unix Domain Socket — cara kerja, keamanan, isi file
- Bagaimana Web UI perlu render dua jenis interaksi (gate approval + AskUser)
- Cara release dengan dua binary (`wick` + `wick-gate`) termasuk MSI
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

`wick-gate` adalah binary yang dipanggil oleh hook ini. Dia yang memutuskan allow atau block.

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

### 1.4 Built-in vs wick-gate

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

**Wick sengaja mematikan dialog ini** dengan set `bypassPermissions = true` di `settings.json`, lalu pasang `wick-gate` sebagai penggantinya:

```
Tanpa Wick → dialog TUI terminal muncul
Dengan Wick → bypassPermissions = true → dialog mati → wick-gate aktif
```

| | Claude Code Built-in | wick-gate |
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
- Perlu binary terpisah (`wick-gate`) + endpoint daemon + socket

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

Kalau mau lebih ketat, bisa verify peer credentials (`SO_PEERCRED`) untuk pastikan hanya `wick-gate` dengan UID yang benar yang bisa connect — tapi untuk Wick, `chmod 0600` di session directory sudah cukup.

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
Claude (PID 1234)        wick-gate          daemon         User (Web)
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

Dipicu saat `wick-gate` mengirim request ke daemon. Daemon broadcast SSE event dengan tipe baru.

**SSE event dari daemon:**

```json
{
  "session_id": "sess_xyz",
  "agent_name": "backend",
  "type": "approval_request",
  "data": "{\"id\":\"abc123\",\"cmd\":\"rm -rf /data\",\"tool\":\"Bash\",\"work_dir\":\"/home/user/project\"}"
}
```

**Yang perlu dirender:** modal/card dengan tombol Approve dan Block, menampilkan command yang mau dieksekusi.

**Response dari UI:** `POST /api/agents/sessions/{id}/approve` dengan `{"id":"abc123","decision":"approve"}`.

**Timing:** harus dijawab dalam 25 detik atau otomatis di-block oleh daemon.

### 6.2 AskUser dari Claude (Sekarang sudah ada sebagian)

Ketika Claude output event `tool_use` dari stream dengan nama tool tertentu yang berisi pertanyaan ke user.

> **Catatan:** `AskUserQuestion` adalah tool harness Claude Code CLI (mode interaktif). Di Wick, Claude jalan dengan `-p` (pipe mode) sehingga tool ini **tidak tersedia**. Tapi Claude masih bisa output teks dengan pilihan sebagai bagian dari response biasa — ini turn-based, bukan blocking.

Kalau ke depan Wick ingin support interactive question dari Claude (yang blocking), perlu:
1. Detect event tipe `tool_use` dengan nama khusus di stream parser
2. Render UI pilihan
3. Inject tool result ke stdin Claude

Ini berbeda dari gate approval karena tidak ada binary yang nunggu exit code.

### 6.3 Perbedaan Dua Interaksi di UI

| | Gate Approval | AskUser Claude |
|---|---|---|
| **Trigger** | SSE `type: approval_request` | SSE `type: tool_use` (nama khusus) |
| **Deadline** | Ya, 25 detik | Tidak (Claude nunggu turn baru) |
| **Response ke** | `POST /approve` → daemon → gate | `POST /send` → stdin Claude (turn baru) |
| **Claude state** | Sedang nunggu (mid-turn) | Sudah selesai turn, nunggu input |
| **Visual** | Modal dengan countdown timer | Card/inline dengan pilihan |
| **Bisa diabaikan?** | Tidak (auto-block setelah timeout) | Ya (Claude nunggu terus) |

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

Untuk gate approval, cukup tambah `type: "approval_request"` dan publish via broadcaster yang sama. Frontend tinggal handle tipe baru ini.

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
| `wick` (atau nama app) | Server daemon, web UI, semua logic utama |
| `wick-gate` | Hook binary kecil, dipanggil Claude sebelum Bash |

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

### 8.2 Strategi: Embed wick-gate ke Main Binary ✅ DIPILIH

`wick-gate` di-compile dulu untuk platform target, lalu di-embed sebagai bytes di dalam main binary via `//go:embed`. Saat daemon start pertama kali per session, binary di-extract ke session directory.

```go
//go:embed assets/wick-gate-*
var embeddedGates embed.FS

func extractEmbeddedGate(sessionDir string) (string, error) {
    name := fmt.Sprintf("assets/wick-gate-%s-%s", runtime.GOOS, runtime.GOARCH)
    if runtime.GOOS == "windows" {
        name += ".exe"
    }
    data, err := embeddedGates.ReadFile(name)
    if err != nil {
        return "", fmt.Errorf("embedded gate not found for %s/%s", runtime.GOOS, runtime.GOARCH)
    }
    gatePath := filepath.Join(sessionDir, "gate", "wick-gate")
    if runtime.GOOS == "windows" {
        gatePath += ".exe"
    }
    if err := os.MkdirAll(filepath.Dir(gatePath), 0700); err != nil {
        return "", err
    }
    if err := os.WriteFile(gatePath, data, 0755); err != nil {
        return "", err
    }
    return gatePath, nil
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

### 8.3 Build Pipeline di CI

Template release workflow (`template/.github/workflows/release.yml`) perlu satu step tambahan **sebelum** `wick build --installer` di setiap matrix job:

```yaml
# Di build job, SEBELUM step "Build":
- name: Build wick-gate
  env:
    GOOS: ${{ matrix.os }}
    GOARCH: ${{ matrix.arch }}
  run: |
    EXT=""
    [ "${{ matrix.os }}" = "windows" ] && EXT=".exe"
    mkdir -p assets
    go build -o "assets/wick-gate-${{ matrix.os }}-${{ matrix.arch }}${EXT}" ./cmd/wick-gate

- name: Build          # ← step existing, tidak berubah
  run: wick build --installer
```

`wick-gate` pure Go (no CGO) sehingga cross-compile works di semua runner. Gate di-compile untuk target platform yang sama dengan main binary, lalu `//go:embed assets/wick-gate-*` otomatis picks it up saat `go build` main binary.

### 8.4 Template Downstream

Proyek downstream yang pakai Wick sebagai framework:
- Tidak perlu buat `cmd/wick-gate/` sendiri — bisa reuse binary dari Wick atau skip gate
- CI workflow tinggal tambah step build gate seperti di atas
- `wick build --installer` tetap tidak berubah

---

## 9. Resolve Gate Binary per Environment

Ada tiga environment dengan cara berbeda untuk menemukan `wick-gate`:

```
Environment           Gate binary dari mana         Cara set
──────────────────────────────────────────────────────────────────
VSCode (wicklab)   →  bin/wick-gate.exe (lokal)  →  WICK_GATE_BIN di .env
Serve (raw binary) →  embedded → extract sekali  →  otomatis
MSI (installer)    →  embedded → extract sekali  →  otomatis
```

### 9.1 Logic Resolve di Daemon

```go
func resolveGateBin(sessionDir string) (string, error) {
    // Dev override — set di .env untuk VSCode / go run
    if p := os.Getenv("WICK_GATE_BIN"); p != "" {
        return p, nil
    }
    // Production: extract dari embed ke session dir (sekali per session)
    return extractEmbeddedGate(sessionDir)
}
```

Urutan prioritas: `WICK_GATE_BIN` env → embedded binary. Kalau keduanya tidak ada → gate tidak aktif, commands lolos semua (fail-open, logged).

### 9.2 VSCode (wicklab)

**Launch config:** `.vscode/launch.json` → `wicklab` → `preLaunchTask: "debug: prep"`

#### Dua launch untuk debug gate

Untuk debug gate secara terpisah tanpa restart wicklab, kita pakai dua launch yang berjalan bersamaan:

```
wicklab          → daemon berjalan, buat session → tulis spec.json
wicklab-gate     → attach debugger ke gate, baca spec dari session yang sama
```

"Sync link" antara keduanya: task `gate: sync-spec` yang otomatis cari `spec.json` dari session terbaru yang dibuat wicklab, lalu tulis path-nya ke `bin/.gate-debug.env`. Gate launch baca dari file itu.

```
wicklab buat session
  → ~\.wick\sessions\<id>\gate\spec.json ditulis
  → jalankan task "gate: sync-spec"
     → ls -t ~\.wick\sessions\*/gate/spec.json | head -1
     → tulis WICK_GATE_SPEC=<path> ke bin/.gate-debug.env
wicklab-gate launch
  → envFile: bin/.gate-debug.env
  → gate baca spec → sama persis dengan yang wicklab pakai
```

#### Yang perlu ditambah ke `.vscode/tasks.json`

```json
{
  "label": "debug: prep",
  "type": "shell",
  "command": "templ generate ./... && bin/tailwindcss.exe -i web/src/input.css -o web/public/css/app.css && go build -o bin/wick-gate.exe ./cmd/wick-gate",
  "problemMatcher": []
},
{
  "label": "gate: sync-spec",
  "type": "shell",
  "command": "powershell -NoProfile -Command \"$spec = Get-ChildItem $env:USERPROFILE\\.wick\\sessions -Recurse -Filter spec.json | Where-Object { $_.FullName -like '*\\gate\\spec.json' } | Sort-Object LastWriteTime -Descending | Select-Object -First 1 -ExpandProperty FullName; if ($spec) { Set-Content -Path bin\\.gate-debug.env -Value \\\"WICK_GATE_SPEC=$spec\\\" -NoNewline; Write-Host \\\"Linked: $spec\\\" } else { Write-Error 'No session spec found. Start wicklab and create a session first.' }\"",
  "problemMatcher": []
}
```

> **Linux/macOS** — ganti command task `gate: sync-spec` dengan:
> ```bash
> "command": "spec=$(ls -t ~/.wick/sessions/*/gate/spec.json 2>/dev/null | head -1) && [ -n \"$spec\" ] && printf 'WICK_GATE_SPEC=%s' \"$spec\" > bin/.gate-debug.env && echo \"Linked: $spec\" || echo 'No session spec found'"
> ```

#### Yang perlu ditambah ke `.vscode/launch.json`

```json
{
  "name": "wicklab-gate",
  "type": "go",
  "request": "launch",
  "mode": "auto",
  "program": "${workspaceFolder}/cmd/wick-gate",
  "output": "${workspaceFolder}/bin/wick-gate",
  "envFile": "${workspaceFolder}/bin/.gate-debug.env",
  "console": "integratedTerminal"
}
```

`"console": "integratedTerminal"` wajib — gate baca stdin (JSON hook input dari Claude). Di terminal kamu bisa paste payload test:

```json
{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /data"}}
```

#### Compound launch (opsional — jalankan keduanya sekaligus)

```json
{
  "name": "wicklab + gate",
  "configurations": ["wicklab", "wicklab-gate"]
}
```

Tambahkan ke array `"compounds"` di `launch.json`. Tapi karena gate langsung exit setelah proses stdin, lebih praktis jalankan terpisah: `wicklab` dulu, baru `wicklab-gate` saat butuh debug.

#### Flow debug lengkap

```
1. F5 → "wicklab"                 → daemon jalan, buka web UI
2. Buat session di web UI          → spec.json ditulis di ~/.wick/sessions/<id>/gate/
3. Terminal → run task             → "gate: sync-spec"
   → bin/.gate-debug.env terisi WICK_GATE_SPEC=<path>
4. F5 → "wicklab-gate"            → debugger attach, nunggu stdin
5. Paste JSON di terminal:
   {"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git status"}}
6. Gate proses → breakpoint hit → inspect spec, rules, decision
```

**Path note:** `WICK_GATE_BIN` di `.env` tetap diperlukan agar wicklab tahu binary gate yang mana. `WICK_GATE_SPEC` di `.gate-debug.env` adalah untuk gate launch sendiri (bukan untuk wicklab).

### 9.3 MSI (Windows Installer)

Dibangun via `wick build --installer`. Flow CI:

```
1. go build -o bin/wick-gate-windows-amd64.exe ./cmd/wick-gate   ← step baru di workflow
2. wick build --installer                                          ← existing, tidak berubah
   → compile main binary (embed wick-gate via //go:embed)
   → wixl → .msi (satu binary, wick-gate sudah di dalam)
```

Di-install ke `%LocalAppData%\Programs\<AppName>\<AppName>.exe`. Saat daemon start, gate di-extract ke session dir — tidak perlu WICK_GATE_BIN.

### 9.4 Serve (Raw Binary / Linux / Docker)

Binary dari `wick build` tanpa `--installer`, atau `.deb`, atau Docker image. Sama dengan MSI dari sisi gate: embedded, di-extract ke `~/.wick/sessions/<id>/gate/wick-gate` saat session start.

```
docker run myapp server     → gate di-extract dari embed otomatis
./myapp server              → sama
systemctl start myapp       → sama
```

Tidak ada konfigurasi tambahan yang diperlukan.

### 9.5 Perbandingan Tiga Environment

| | VSCode (wicklab) | Serve / raw binary | MSI |
|---|---|---|---|
| **Gate binary dari** | `bin/wick-gate.exe` (lokal) | Embedded → extracted | Embedded → extracted |
| **Cara set** | `WICK_GATE_BIN` di `.env` | Otomatis | Otomatis |
| **Perlu build manual?** | Ya (via `debug: prep` task) | Tidak | Tidak |
| **Version sync** | Manual (rebuild saat ada perubahan) | Selalu sync (embedded saat compile) | Selalu sync |
| **File yang perlu diedit** | `.vscode/tasks.json` + `.env` | Tidak ada | Tidak ada |

### 9.6 Template Downstream (cmd/lab)

Proyek yang pakai Wick sebagai framework perlu:

1. `cmd/wick-gate/` — bisa copy dari wick atau implement sendiri sesuai rules mereka
2. `.env.example` — tambah `WICK_GATE_BIN` entry (sudah ada di template)
3. `.vscode/tasks.json` — tambah gate build ke `debug: prep`
4. CI workflow — tambah `go build ./cmd/wick-gate` sebelum `wick build --installer`

---

## 10. Lokasi File di Filesystem (Runtime)

```
~/.wick/agents/sessions/<session-id>/
  ├── meta.json                  ← session metadata
  ├── agents.json                ← agent list + CLI session ID
  ├── commands.jsonl             ← audit log semua command
  └── gate/
      ├── spec.json              ← rules whitelist untuk gate
      ├── settings.json          ← Claude hook config (PreToolUse → wick-gate)
      └── gate.sock              ← Unix domain socket
                                    dibuat saat daemon start, chmod 0600
                                    dihapus saat daemon stop
```

Kalau pakai embed (opsi 1):

```
~/.wick/agents/sessions/<session-id>/gate/
  └── wick-gate                  ← di-extract dari embedded binary saat start
                                    chmod 0755, di-recreate tiap spawn
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
| D8 | Embed wick-gate ke binary utama (rekomendasi) | User satu file, version selalu sync, tidak perlu installer logic baru |
| D9 | Broadcast approval_request via Broadcaster yang sudah ada | Tidak perlu infrastruktur SSE baru, cukup tambah tipe event |
| D10 | `WICK_GATE_BIN` env var override untuk dev | VSCode/go run tidak punya embed, perlu path eksplisit. Env var paling tidak invasif — tidak ubah kode path, tidak ubah interface |
| D11 | `debug: prep` task build gate otomatis | Developer tidak perlu ingat build gate manual sebelum debug — F5 langsung siap |

---

## 12. Checklist Implementasi

Urutan logis: gate binary siap dulu → daemon socket → approval flow → web UI → release.

**A. Gate binary & embed**
```
[ ] A1. //go:embed assets/wick-gate-* di daemon package
[ ] A2. extractEmbeddedGate() — extract ke session dir, chmod 0755, idempotent
[ ] A3. resolveGateBin() — cek WICK_GATE_BIN env dulu, fallback ke extractEmbeddedGate
[ ] A4. Update wick-gate binary: tambah socket path dari spec (baca WICK_GATE_SPEC),
        connect unix socket → send ApprovalRequest → block → terima response → exit 0/2
```

**B. Daemon — Unix socket & approval state**
```
[ ] B1. Unix socket listener per session (buat saat session start, chmod 0600)
        path: ~/.wick/sessions/<id>/gate/gate.sock
[ ] B2. Pending state manager: sync.Map[id]chan ApprovalResponse + goroutine per conn
[ ] B3. Timeout goroutine: 25s → auto-block kalau user tidak respond
[ ] B4. Endpoint: POST /api/agents/sessions/{id}/approve
        body: {"id":"...","decision":"approve|block"}
        → resolve channel → goroutine balas ke gate
```

**C. SSE & Web UI**
```
[ ] C1. SSE event type baru "approval_request" — broadcast via Broadcaster yang sudah ada
[ ] C2. SSE event type "approval_resolved" — untuk dismiss modal di semua tab
[ ] C3. Web UI: render modal approval saat terima SSE "approval_request"
        tampilkan: command, agent name, work dir, countdown 25s
[ ] C4. Web UI: tombol Approve dan Block → POST /api/agents/sessions/{id}/approve
[ ] C5. Web UI: auto-dismiss modal saat terima "approval_resolved"
```

**D. Wiring & factory**
```
[ ] D1. Wire Gate di factory.go (saat ini masih nil) — inject socket path ke spec
[ ] D2. Spec.json: tambah field socket_path untuk gate → daemon socket
```

**E. Dev tooling & release**
```
[ ] E1. .vscode/tasks.json — update "debug: prep": tambah go build gate ke perintah
[ ] E2. .vscode/tasks.json — tambah task "gate: sync-spec" (auto-link spec terbaru)
[ ] E3. .vscode/launch.json — tambah launch "wicklab-gate" (envFile: bin/.gate-debug.env)
[x] E4. .env.example — WICK_GATE_BIN entry sudah ada
[ ] E5. template release workflow — tambah step "Build wick-gate" sebelum "wick build --installer"
```

---

## 13. Nama Teknik & Referensi

Daftar istilah teknis yang dipakai dalam arsitektur ini beserta link dokumentasi primer.

### Naming

| Istilah | Arti dalam konteks wick |
|---|---|
| **Pre-execution Hook** | Hook yang fire sebelum tool dieksekusi — `PreToolUse` di Claude |
| **PEP** (Policy Enforcement Point) | Yang enforce keputusan → claude CLI |
| **PDP** (Policy Decision Point) | Yang bikin keputusan → wick-gate |
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
