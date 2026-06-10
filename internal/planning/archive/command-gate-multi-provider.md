# Command Gate — Multi-Provider Design

Status: **Phase 1 landed** — spawn-time gating refactored ke per-instance intent + master switch cascade. Tooltip-theme, codex/gemini wiring, fail-safe sync UI shipped.
Update terakhir: 2026-05-11.

Commits di branch `improve-gate2`:
- `d5d467c` feat(gate): multi-provider capability detection (Priority 0)
- `a53b648` feat(ui): per-provider Command Gate section in Providers card
- `aa86220` fix(ui): theme-aware tooltip + register codex/gemini for capability lookup
- `994e880` feat(gate): per-instance Hooks intent + master switch cascade + spawner refactor
- (current) fix(gate): bypass dan gate mutually exclusive — revert regression dari 994e880 + UI bypass-lock state

Doc ini supersede arsitektur lama yang Claude-only ([command-gate-architecture.md](command-gate-architecture.md)). Tujuan: gate jadi generic, per-provider hook contract di-translate sama adapter, capability dicek runtime jadi user gak nyalain gate di provider yang gak support hook.

## TODO — Provider hook detection (priority 0)

Tujuan: implement capability detection **dulu**, terisolasi dari spawn/chat path. Begitu ini hijau, kita punya integration test yang reproducible tanpa harus jalanin full agent session. Chat-level test nyusul setelah detection stabil.

Urutan task — tiap step bisa landed independent, gak block phase berikutnya.

```
[x] D1. Capability registry skeleton
    - internal/agents/capability/capability.go (separate package — hindari circular
      import dari spawner subpackages yang juga butuh baca Capability)
    - Capability struct + HookSupported static map
    - Initial values: claude=true (scope=bash+edit+mcp), codex=true (scope=shell-only),
      gemini=true (scope=untested — adapter shipped but runtime unverified)
    - Self-registration pattern: tiap provider sub-package (claude/codex/gemini)
      manggil capability.Register("claude", Capability{...}) di init() — central map
      gak perlu di-edit tiap nambah provider
    - Unit test:
        - Register + Lookup roundtrip
        - Lookup unknown name → (zero, false)
        - Concurrent Register calls safe (race detector pass)
        - Duplicate Register: last-write-wins atau panic (decide saat impl, document)

[x] D2. Hook config writer per provider (project-scoped, dry-run mode)
    - internal/agents/provider/claude/hookconfig.go: WriteHookConfig + RemoveHookConfig
    - Same untuk codex/, gemini/ (semua ship implementation, gemini boleh return
      stub gate path tapi argv shape harus match docs)
    - Dry-run flag: write ke temp dir, return path, jangan touch real config
    - Unit test:
        - WriteHookConfig produces JSON matching golden file per provider
        - RemoveHookConfig deletes file, no-op kalau gak ada
        - Idempotent: WriteHookConfig twice = same file content
        - Merge behavior: kalau target file existing dgn unrelated keys, preserve
          (penting — user mungkin punya hook config lain)
        - Dry-run: returns path tanpa side effect di filesystem nyata

[x] D2b. Spawner sub-package per provider (all 3: claude, codex, gemini)
    Scope: ship spawn.go untuk ketiga provider supaya factory bisa dispatch
    uniform. Test coverage realistis:
      - claude: full test (user punya install)
      - codex: full test (user punya install — codex 0.129.0)
      - gemini: ship code lengkap tapi runtime test deferred — user belum bisa
        verify. Tandai test gemini sebagai `t.Skip("requires gemini install, manual verify")`
        sampai ada yang bisa run end-to-end.

    Tasks:
    - internal/agents/provider/claude/spawn.go: existing, no behavior change.
      Audit BypassPermissions wiring vs gate-active conflict guard (sudah ada di factory).
    - internal/agents/provider/codex/spawn.go (new): Spawner struct impl provider.Spawner
        - Fields: Binary, AskForApproval (codex equivalent dari BypassPermissions), ExtraArgs
        - Argv: codex headless flags. Verify dari `codex --help` binary terinstall (jangan trust doc)
        - Conflict: gate active → skip --ask-for-approval=never (atau apapun bypass equivalent)
    - internal/agents/provider/gemini/spawn.go (new): Spawner struct impl provider.Spawner
        - Fields: Binary, YoloMode (atau apapun gemini equivalent), ExtraArgs
        - Argv: gemini headless flags. Best-effort dari docs — TODO comment "verify saat ada akses gemini"
        - Conflict guard placeholder, sama pattern
    - spawner.go interface tetap unchanged (already generic)
    - pool/factory.go: dispatch by opt.ProviderType ganti hardcode claude.Spawner
        - switch case 3 way: claude / codex / gemini → instantiate Spawner respective
        - Factory tetap pegang rule "gate active → skip provider's bypass flag", translate per-provider
    - Unit test:
        - claude: argv shape with + without gate (existing pattern preserved)
        - codex: argv shape with + without gate, --ask-for-approval flag wiring
        - gemini: argv shape only (integration t.Skip — need install)
        - All three: SpawnOptions{Workspace, ResumeID, ExtraEnv} respected
        - All three: BypassPermissions/equivalent NOT set when gate active (factory rule)
        - Factory dispatch test: opt.ProviderType="codex" → codex.Spawner instance,
          dst (table-driven test mudah extend)

[x] D3. Gate probe mode (--probe)
    - cmd/gate: tambah --probe flag
    - Behavior: parse stdin, set Decision.Probe=true, kirim ke daemon
    - Daemon: route ke probe handler (lihat D4), bukan ke session
    - Adapter emit canned deny output, exit sesuai provider quirks
    - Unit test:
        - per adapter: golden file stdin → stdout (with Probe=true)
        - flag parsing: --probe absent vs present → Decision.Probe shape
        - fail-open behavior unchanged saat daemon socket missing

[x] D4. Daemon probe handler
    - internal/agents/gate/daemon: handle Decision.Probe==true tanpa SSE broadcast
    - Reply Result{Allow:false, Reason:"capability probe"}
    - Log probe event ke commands.jsonl dgn stage=probe
    - Daemon path harus exist sebelum D3 (cek `internal/agents/gate/` repo —
      kalau belum ada daemon entrypoint, D4 included scaffolding listener)
    - Unit test:
        - probe request → Result{Allow:false, Reason:"capability probe"}
        - non-probe request → normal flow (whitelist eval atau pending channel)
        - jsonl entry stage=probe written correctly
    - Integration test: dial socket dari fake gate, kirim probe, assert reply shape

[x] D5. HookCapabilityCheck function
    - internal/agents/capability/check.go: HookCapabilityCheck(ctx, ins) Capability
      (same package as D1's registry; check func reads registry + invokes per-provider Prober)
    - Spawn provider dgn project-scoped hook config (D2) pointing ke gate --probe (D3)
    - Send minimal "run a sentinel shell command" prompt via provider stdin
    - Verify provider honors deny: sentinel file should NOT exist after spawn exits
    - Timeout 10s, cleanup temp workspace
    - HookVerified=true kalau sentinel absent, false kalau ada
    - Unit test (mock Prober):
        - HookSupported=false → return early, no spawn attempt
        - Prober returns nil → HookVerified=true
        - Prober returns error → HookVerified=false, HookError populated
        - ctx canceled → HookError="canceled", no leak
        - Timeout exceeded → HookError="timeout"
    - Integration test per provider (real binary):
        - claude: full integration test (user bisa verify)
        - codex: full integration test (user bisa verify)
        - gemini: code path exist, runtime test t.Skip dgn note "needs gemini install"
        - Skip kalau binary gak install di CI runner (gunakan exec.LookPath guard)
        - Cleanup: workspace temp dir di-defer remove regardless of test outcome

[x] D6. Capability cache + invalidation
    - probeCache di provider.go diperluas: simpan Capability bareng Status
    - Cache key: Type + ResolvedPath + Version (re-probe saat version berubah)
    - Invalidation triggers:
        - Save/Delete instance (existing — extend untuk drop capability)
        - Version change detected di Probe (existing version refresh)
        - Manual Rescan dari UI / `wick agents rescan`
    - TTL: tetap pakai probeCacheTTL existing (30s) untuk Status, tapi Capability
      pakai TTL lebih panjang (1h) karena hook contract jarang berubah dalam
      satu version. Pisah TTL biar version probe tetap fresh tanpa re-probe hook.
    - Unit test:
        - cache hit returns cached Capability
        - version change forces re-probe
        - InvalidateProbeCache drops both Status + Capability
        - concurrent ProbeAllCached calls gak duplikat probe (sync.Once or mutex)

[~] D7. (SKIPPED — capability check dari web UI only, gak butuh CLI)
        CLI subcommand untuk manual probe (debugging)
    - `wick agents capability <type> [--name <name>] [--json]` → run HookCapabilityCheck
    - Default output: human-readable text format:
        ```
        Provider: claude/claude
        Binary:   /usr/local/bin/claude
        Version:  claude 2.1.142
        Hook support:  yes (bash+edit+mcp)
        Hook verified: yes (probed 2026-05-11T10:23:00Z)
        ```
    - `--json` flag: emit raw Capability struct sebagai JSON untuk scripting
    - Exit code: 0 kalau HookVerified=true, 1 kalau HookError non-empty
    - Useful buat reproduce CI failure di local
    - Help text jelasin "this spawns the provider with a sentinel; expect a deny"
    - Unit test:
        - text format snapshot test (stable output across runs)
        - json output parsable
        - exit code matches HookVerified state

[x] D8. Integration test harness
    - test/integration/gate_capability_test.go
    - Spin up daemon socket, fake provider binary (shell script yg simulate hook call)
    - Walk through full D3→D4→D5 flow tanpa real claude/codex
    - Run di CI di setiap PR (cheap, no external deps)
    - Test scenarios:
        - Happy path: fake provider invokes gate --probe → daemon replies deny → assert audit log
        - Daemon down: gate fail-open allow, sentinel created (negative case)
        - Adapter parse error: malformed stdin → fail-closed deny + stderr log
        - Concurrent probes: 5 parallel HookCapabilityCheck calls share daemon socket OK
        - Cleanup: socket file removed after daemon shutdown
    - Fake provider script per OS:
        - Linux/macOS: bash script
        - Windows: powershell script (.ps1)
        - Build tag `//go:build integration` to opt-in
```

**Exit criteria Priority 0** — ✅ done:
- D1–D6, D8 hijau di CI (unit tests + integration harness with build tag)
- D7 skipped — capability check dari web UI only
- Provider self-registration via init() shipped untuk claude+codex+gemini
- Existing claude path UNTOUCHED (factory.go + gate.WriteWorkspaceHooks intact)
- TestProviderRegistrationsLoadAll proves all 3 registries populated

**Implementation notes (post-merge):**
- Capability state persists ke `userconfig.ProviderStatus.Hooks` map (Opsi B nested
  shape — extensible buat hook event lain di masa depan tanpa schema churn)
- `provider.MergeHookCapability(t, name, event, hc)` helper buat HTTP handler
- Fake provider Go binary (build on-the-fly) jadi backbone integration test —
  cross-platform tanpa per-OS script
- Codex argv `codex exec --sandbox workspace-write` + hook config di
  `<ws>/.codex/hooks.json` — verify ulang against codex 0.129 binary saat
  pertama kali user toggle gate ON
- Gemini argv `gemini -p` + hook config di `<ws>/.gemini/settings.json` — UNVERIFIED,
  scope tetap "untested" di registry sampai ada kontributor yang verify

**Next (Phase 1+):**
- ✅ HTTP handler `POST /providers/{type}/{name}/hooks/{event}/check` —
  shipped di `internal/tools/agents/providers.go` (`checkProviderHook`)
- ✅ UI Command Gate section per provider card — `hookCapabilitySection`
  templ helper + `data-check-hook` JS handler, badge baca dari
  `ProviderStatus.Hooks["PreToolUse"]`, Test button + auto-reload
- ⏳ Phase 1 refactor existing claude spawn jadi adapter-based:
  - Replace `factory.attachGateConfig` dispatch dgn capability writer lookup
  - Remove paralel `gate.ProbeGateSupport` + `probeProviderGate` route
  - Route gate-toggle ON via per-instance `GateEnabled` flag instead of global config
- ⏳ Per-instance `GateEnabled bool` field di `userconfig.ProviderInstance`
- ⏳ Wire `MergeHookCapability` result ke spawn-time enforcement (block spawn
  kalau user toggle ON tapi capability gak verified)

## Ringkasan keputusan

- **Gate = binary decision protocol**: stdout cuma allow/deny. Gak ada `approve_once / session / always` di level gate.
- **Adapter per provider** di dalam gate binary: parse stdin shape provider → canonical → daemon → format stdout sesuai provider quirks (exit code, envelope shape, stderr).
- **Daemon hold all state**: whitelist, auto-approved (persistent), session cache (in-memory). Gate stateless.
- **Gate config per-provider, bukan global**: `GateEnabled` di-rename jadi opt-in flag per-instance. Provider yang gak support hook → gate option disabled di UI + spawn gak inject hook config sama sekali.
- **Default OFF**: gate disable by default untuk semua provider instance. User harus eksplisit aktifin lewat UI toggle. Alasannya: hook = intercept tiap shell call agent, ada overhead + butuh user paham tradeoff. UI kasih explainer singkat di atas toggle.
- **Capability probe**: saat user toggle gate ON di Providers UI, jalanin `HookCapabilityCheck` per provider. Hasil cached bareng `Status.Version`. Gagal → gate option locked off, banner "hook not supported in <provider> <version>".

## Kenapa pindah dari design lama

1. **Spec.json race di gate** — sekarang gate baca spec setiap call. Pindah eval ke daemon = single writer, no race.
2. **Approval modes 4 (once/session/always/block)** = kebijakan daemon, bukan kontrak gate. Gate cuma butuh tau "boleh atau nggak". Logika expand session cache, persist `auto_approved`, hidup di daemon.
3. **Multi-provider**: Claude bukan satu-satunya. Codex 0.129 punya `PreToolUse` (mirip Claude tapi flat envelope). Gemini punya `BeforeTool`. Tiap provider bisa ubah kontrak kapanpun (Claude udah dua kali). Adapter pattern isolate drift.
4. **Heterogen support**: Codex baru intercept "simple shell only". Gemini hooks masih maturing. Provider yang gak punya hook sama sekali (atau kontrak gak compatible) gak boleh diam-diam bypass gate — user harus tau, dan toggle harus reflect realitas.

## Arsitektur

```
Provider subprocess (claude / codex / gemini)
  │
  │ provider-specific PreToolUse / BeforeTool fires
  ▼
<app>-gate --provider=<name>  (stateless, short-lived)
  │
  ├─ stdin: provider payload shape
  ├─ adapter.Parse → canonical Decision
  ├─ dial daemon socket (Unix domain, raw JSON)
  ├─ daemon evaluates:
  │     1. auto_approved (persistent) hit?  → allow
  │     2. session_approve cache hit?       → allow
  │     3. whitelist rule glob match?       → allow
  │     4. else broadcast SSE → user modal → reply
  │
  ├─ receive canonical Result {Allow, Reason}
  └─ adapter.Emit → provider-specific stdout/exit
```

Daemon side gak peduli provider. Whitelist match, audit log, SSE event, semua canonical.

## Kontrak canonical (gate ↔ daemon)

Internal only. Gak pernah lewat boundary provider.

```go
// Sent gate → daemon
type Decision struct {
    Tool      string `json:"tool"`        // "Bash" | "Edit" | "MCP:<name>"
    Cmd       string `json:"cmd"`         // shell line, empty for non-shell
    Cwd       string `json:"cwd"`
    RequestID string `json:"request_id"`
    Provider  string `json:"provider"`    // for audit only
    Probe     bool   `json:"probe,omitempty"` // true = capability probe, daemon skips SSE
    Raw       json.RawMessage `json:"raw,omitempty"` // original payload preserved
}

// Sent daemon → gate
type Result struct {
    Allow  bool   `json:"allow"`
    Reason string `json:"reason,omitempty"`
}
```

Itu doang. Binary decision. Tiga session-scope ada di daemon, gak boncor ke gate.

## Adapter interface

```go
package adapter

type Adapter interface {
    // Name returns the provider identifier as used in --provider flag.
    Name() string

    // Parse converts provider-specific hook stdin into canonical Decision.
    Parse(stdin []byte) (Decision, error)

    // Emit writes the provider-specific stdout envelope and returns the
    // exit code the gate process must use. Exit code semantics differ
    // by provider (Claude >= 2.1.138 forces exit 0 for deny, Codex
    // accepts exit 2, Gemini prefers exit 2 + stderr).
    Emit(w io.Writer, result Result) (exitCode int, err error)
}
```

Lokasi: `internal/agents/gate/adapter/<provider>/adapter.go`. Satu file per provider, lengkap dengan unit test stdin/stdout golden.

### Adapter registry — dispatch by --provider flag

`cmd/gate` parse flag `--provider=<name>`, lookup adapter dari registry:

```go
package adapter

var registry = map[string]Adapter{}

// Register adds an adapter to the lookup table. Called from adapter
// sub-packages in init(). Adapter.Name() determines the lookup key.
func Register(a Adapter) { registry[a.Name()] = a }

// Lookup returns the adapter registered for provider name, or error
// "unknown provider" if no adapter registered (cmd/gate forgot blank import).
func Lookup(name string) (Adapter, error) { ... }
```

Tiap adapter sub-package (`adapter/claude`, `adapter/codex`, dst) panggil `adapter.Register(&adapter{})` di `init()`. Gate binary blank-import semua adapter di `cmd/gate/main.go`:

```go
import (
    _ "github.com/yogasw/wick/internal/agents/gate/adapter/claude"
    _ "github.com/yogasw/wick/internal/agents/gate/adapter/codex"
    _ "github.com/yogasw/wick/internal/agents/gate/adapter/gemini"
)
```

Tambah provider baru = bikin folder adapter + 1 baris blank import. Gak ada central switch.

### Bypass flag translation per provider

Factory translate intent "bypass approval prompt" jadi flag CLI-specific:

| Provider | Bypass flag | Behavior saat gate active |
|---|---|---|
| claude | `--permission-mode bypassPermissions` | **SKIP** — claude 2.1.138+ fire hook tapi **ignore deny envelope** dgn flag set (verified 2026-05-11, regression dari `mkdir 125`). Pre-2.1.138 flag skip hook total. Either way: hidup berdampingan = gate sia-sia. |
| codex | `--ask-for-approval=never` | **SKIP** — bypass mode kemungkinan skip PreToolUse juga (verify saat impl) |
| gemini | TBD (`--yolo`?) | **SKIP** — defensive, sama pattern |

Aturan universal: **gate active ⇒ JANGAN set bypass flag manapun**. Tiap Spawner sub-package punya field bypass sendiri (claude.Spawner.BypassPermissions, codex.Spawner.AskForApproval=never, dst), factory yang decide set atau gak.

**Mutex inverse** (per [command-gate-claude-2.1-fix.md](command-gate-claude-2.1-fix.md)): kalau `Spawner.BypassPermissions=true` di-set eksplisit (channel non-interaktif), `applyHookConfig` HARUS strip workspace hook config dan return `false`. Bypass owner spawn outright — alert gate sia-sia karena ngga ada UI buat approve.

### Per-provider quirks (initial mapping)

| Provider | stdin path | Allow output | Deny output | Exit (deny) |
|---|---|---|---|---|
| claude | `tool_name`, `tool_input.command`, `cwd` | `{"hookSpecificOutput":{"permissionDecision":"allow"}}` | `{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"..."}}` | **0** (≥2.1.138) |
| codex | TBD — cek `codex/codex 0.129.0` schema | `{"permissionDecision":"allow"}` | `{"permissionDecision":"deny","reason":"..."}` atau exit 2 + stderr | 0 atau 2 |
| gemini | TBD — cek `BeforeTool` schema | empty stdout, exit 0 | `{"decision":"deny","reason":"..."}` | 2 |

TBD = belum diverifikasi langsung dari binary terinstall; lookup contract saat implement adapter (jangan trust docs blindly, version drift).

## Provider capability — hook support detection

**Inti perubahan**: gate jadi per-provider feature, bukan global.

### Capability flags

Per `provider.Type` declare capability statis + runtime probe:

```go
package capability  // separate package biar spawner subpackages bisa import

type Capability struct {
    HookSupported   bool   // structurally — adapter exists in code
    HookVerified    bool   // runtime probe passed
    HookProbedAt    time.Time
    HookError       string // why probe failed (version too old, schema mismatch)
    InterceptScope  string // "bash+edit+mcp" (claude) | "shell-only" (codex) | "untested" (gemini)
}

// Self-registration: each provider sub-package calls this in init()
func Register(name string, cap Capability) { ... }
func Lookup(name string) (Capability, bool) { ... }
```

Lokasi: `internal/agents/capability/` — package terpisah dari `provider/` biar `provider/claude/`, `provider/codex/` dst bisa import tanpa circular dependency.

`HookSupported` derived dari registry: provider yang ada adapter file + manggil `capability.Register` di init = true. Saat ini:
- claude → true, scope=bash+edit+mcp (existing adapter)
- codex → true, scope=shell-only (new adapter, partial intercept per OpenAI docs)
- gemini → true, scope=untested (adapter shipped, runtime test deferred sampai ada akses)

`InterceptScope` dipake:
- **UI**: tampilin badge "Bash, Edit, MCP" vs "Shell only" vs "Untested" di Providers card biar user tau coverage
- **Audit log**: prefix entry di `commands.jsonl` biar bisa filter "command yang ke-skip karena di luar scope"
- **Daemon**: gak peduli (binary allow/deny tetap)

### Runtime probe (`HookCapabilityCheck`)

Saat user toggle gate ON di Providers UI per-instance, panggil:

```go
func HookCapabilityCheck(ctx context.Context, ins Instance) Capability
```

Tahapan:
1. Cek `HookSupported` dari registry. False → return early, UI lock toggle off.
2. Spawn provider dengan minimal hook config pointing ke `<app>-gate --provider=<name> --probe`. Mode `--probe` di gate: balas immediately dengan canned deny, log probe event, exit. Daemon route ke probe handler, gak ke session.
3. Provider proses dispatch shell command sentinel. Kalau dispatch ke-block sesuai expected → `HookVerified = true`.
4. Timeout 10s. Gagal apapun → `HookError`.

### Sentinel mechanism per provider

Provider beda cara terima prompt. Probe runner punya per-provider plugin:

```go
type Prober interface {
    // SendSentinel spawns provider with hook config + workspace, sends prompt
    // asking provider to `touch sentinel.txt`, waits for exit/timeout.
    // Returns nil if sentinel file absent (deny honored), error otherwise.
    SendSentinel(ctx context.Context, ins Instance, workspace string) error
}
```

Per provider implementation:
- **claude**: pakai stream-json input (`{"type":"user","message":{"role":"user","content":"run: touch sentinel.txt"}}`) via stdin headless mode
- **codex**: `codex exec --sandbox workspace-write "touch sentinel.txt"` (one-shot mode, gak perlu interactive)
- **gemini**: TBD — kemungkinan `gemini -p "touch sentinel.txt"` headless

Tiap Prober ada di `provider/<name>/prober.go`, register ke `capability` package via init.

Result di-cache (lihat D6 — Capability TTL 1h, terpisah dari Status TTL 30s, karena hook contract jarang berubah dalam satu version). User klik Rescan = re-probe.

### UI behavior

Per-instance Providers card tambah section:

```
┌─ Command Gate ────────────────────────────────────────────┐
│ ⓘ What's this?                                            │
│   Every shell command the agent runs goes through wick    │
│   first. You approve / block from the web UI before it    │
│   executes. Without gate, the provider's own permission   │
│   flow applies (usually a terminal prompt or auto-allow). │
│                                                            │
│ Status: [supported, verified ✓]                           │
│         [supported — click Test to verify]                │
│         [not supported in this provider]                  │
│         [not supported in version <x>]                    │
│                                                            │
│ [Toggle: OFF]   ← default off, user enables explicitly    │
│ [Test capability]                                         │
└────────────────────────────────────────────────────────────┘
```

Toggle disabled kalau capability negative. Tooltip jelasin alasan. Explainer copy diatas itu wajib — user gak boleh nyalain tanpa tau dia ngapain.

### Per-instance gate flag (config schema)

`userconfig.ProviderInstance` punya `Hooks` map (nested shape Opsi B —
extensible buat hook event lain):

```go
type ProviderInstance struct {
    // existing fields...
    Hooks map[string]HookInstanceConfig `json:"hooks,omitempty"`
}

type HookInstanceConfig struct {
    Enabled bool `json:"enabled,omitempty"`
    // Future: Mode, AllowList, ChannelOverride
}
```

Key map = event name (`"PreToolUse"`, future `"SessionStart"` dst).
Provider-level constant: `provider.HookEventPreToolUse`.

### Master switch + cascade

`config.GateConfig.GateEnabled` tetap ada sebagai **master switch**, tapi
behaviornya bukan independent gate — dia **fan-out command**:

- **OFF → ON**: handler `toggleGate` flip semua `Hooks[event].Enabled = true`
  per instance, lalu spawn goroutine background per provider yang jalanin
  `HookCapabilityCheck`. Provider yang verified tetep enabled; provider yang
  fail di-rollback ke `Enabled=false`. Capability state persist ke
  `ProviderStatus.Hooks[event]`.
- **ON → OFF**: flip semua `Hooks[event].Enabled = false`. Capability state
  preserved jadi re-enable later gak ilang last probe result.

**Single source of truth**: `Instance.Hooks[event].Enabled` di disk. Spawner
gak peduli master state — cuma baca per-instance flag. Master cuma trigger
mass-update.

Defensive guard: `enableProviderHook` HTTP handler refuse dgn 409 kalau
master off (handle stale tab / direct curl).

### Inflight probe single-flight

`capability.probeInflight sync.Map` lock per provider name. Concurrent
`HookCapabilityCheck` untuk provider sama → second caller dapat
`ErrProbeInflight`. UI surface lewat `provider.Status.Probing` (set di
`providersPage` via `capability.IsProbing`). Badge "testing…" + button
disabled saat probe in-flight. Probe survive page refresh karena goroutine
detached dari request.

## Spawn-time wiring

Spawner per-provider implement `applyHookConfig(opt)` yang:

```go
// Bypass takes precedence over gate: non-interactive channels don't have
// a UI to answer prompts, alerts from gate would be unactionable.
if s.BypassPermissions {
    writer.Remove(opt.Workspace) // cleanup stale gate-on config
    return false
}
enabled := opt.Instance != nil && opt.Instance.HookEnabled(provider.HookEventPreToolUse)
if !enabled {
    writer.Remove(opt.Workspace) // cleanup stale config
    return false
}
writer.Write(opt.Workspace, opt.GateBinary)
return true
```

Saat returnnya true (gate active):
- **claude**: JANGAN tambah `--permission-mode bypassPermissions`. Claude
  2.1.138+ fire hook tapi ignore deny envelope dgn flag set — verified
  2026-05-11. Gate hook = sole authority via `emitBlock` / `emitAllow`.
- **codex**: skip `--ask-for-approval=never` (biar PreToolUse fire)
- **gemini**: skip `--yolo` (sama)

Saat `s.BypassPermissions=true` (gate paksa OFF di applyHookConfig):
- **claude**: tambah `--permission-mode bypassPermissions`. Spawn unguarded
  — sesuai intent caller non-interaktif.

`Remove` penting — toggle OFF harus bersihin hook config workspace yang
sebelumnya ON, biar provider gak still panggil gate binary stale.

### Factory dispatch

`pool/factory.go` resolve `Instance` once per Build via `provider.Find()`
(hits in-memory cache, gak file IO setiap spawn), forward ke `provider.Options`:

```go
provider.New(provider.Options{
    Instance:   &resolvedIns,
    GateBinary: gateBin,
    // ...
})
```

`agent.Start` forward ke `SpawnOptions`. Spawner sub-package consume
`opt.Instance.HookEnabled(event)`.

### Instance cache

`provider.instanceCache` in-memory map keyed by AppName. Invalidate di
Save/Delete/SetHookEnabled. `Find()` consult cache; miss → reload.
Spawn path gak hit userconfig file lagi.

### Per-provider hook config writer

Lokasi: `internal/agents/provider/<name>/hookconfig.go`. Satu fungsi:

```go
func WriteHookConfig(gateBin string, scope HookScope) error
func RemoveHookConfig() error
```

Scope = project-scoped config file (decided: gak nyentuh user-global config — session cleanup otomatis hapus, dan user-managed settings tetap intact). Detailnya per provider:

- **Claude**: `<sessionDir>/.claude/settings.json` — sudah ada mekanisme di repo
- **Codex**: `<sessionDir>/.codex/hooks.json` — verify path saat impl
- **Gemini**: `<sessionDir>/.gemini/settings.json` — verify path saat impl

Project-scoped artinya hook cuma aktif untuk session ini. Mati = config terhapus sama session cleanup.

## Daemon changes

### State pindah dari gate

Daemon `internal/agents/gate/daemon.go` (TBD jika belum ada) handle:

```go
type Daemon struct {
    autoApproved   *autoApprovedStore   // persistent, file-backed
    sessionApprove map[SessionID]map[MatchKey]bool  // in-memory
    rules          []Rule               // glob whitelist
    pending        sync.Map             // RequestID → chan Result
}

func (d *Daemon) Decide(req Decision) Result {
    // 1. auto_approved hit?
    // 2. session cache hit (lookup session by cwd → sessionID)?
    // 3. rule glob match?
    // 4. broadcast SSE, wait pending channel
}
```

Existing logic di gate side (`LoadSpec`, `MatchRule`, `AutoApprovedHit`) di-port ke daemon. Gate side jadi dumb pipe.

### Approval response wider

Modal masih 4 options (`approve_once / session / always / block`), tapi cuma daemon yang interpret. Web UI POST `/api/agents/sessions/{id}/approve` payload tetap sama. Daemon translate:
- `approve_once` → reply current Result{Allow:true}
- `approve_session` → reply + add ke sessionApprove cache
- `approve_always` → reply + persist ke auto_approved + save spec.json
- `block` → reply Result{Allow:false}

Gate gak tau bedanya, dia cuma terima Result.

## UI conventions

### Theme-aware tooltip

Native `title=` attribute pakai OS chrome (white-on-black di Windows) yang
tabrakan sama dark theme. Solusi: helper `tooltipStyles()` di
`view/layout.templ` — pure CSS via `[data-tooltip="..."]` + pseudo-elements
yang respect `.dark` selector. Pakai `data-tooltip="..."` (optional
`data-tooltip-pos="bottom"`) di mana saja, gak butuh JS.

Migration: replace semua `title="..."` di providers card ke `data-tooltip="..."`.

### Per-card status badge

| State | Badge | Button |
|---|---|---|
| `agents.bypass_permissions=true` | `locked (bypass)` (abu) | tersembunyi |
| master OFF | `locked` (abu) | tersembunyi |
| master ON + probe inflight | `testing…` (biru pulse) | "Testing…" disabled |
| master ON + intent ON + verified | `enabled ✓` (hijau) | Test + Disable |
| master ON + intent ON + verify failed | `enabled (unverified)` (kuning) | Test + Disable |
| master ON + intent OFF + probe pass | `ready` (abu) | Enable |
| master ON + intent OFF | `disabled` (abu) | Enable |

`hookCapabilitySection` templ helper take `gate GateStatusVM` param.
Bypass-locked branch precedence > master OFF branch — same `Enabled=false`
result tapi tooltip beda biar user tau alasannya.

### Master switch card states

`gateStatusCard` (di top Providers page) reflect `BypassLocked` via amber
badge + disabled "Locked" button gantiin "Turn on/off" form. `Note` field
spell out alasan ("Bypass permissions is on … Turn bypass off in agents
settings to use the gate"). Toggle aksi POST tetap ada server-side, tapi
handler `toggleGate` 409 saat bypass on (defensive untuk stale tab).

## File layout (perubahan minimal)

```
~/.<app>/agents/gate/
├── spec.json          ← daemon-owned (was: gate read, daemon write)
├── gate.sock          ← unchanged
└── commands.jsonl     ← daemon writes (was: gate writes)
```

Daily tail log unchanged.

## Failure modes (updated)

| Situation | Behavior | Catatan |
|---|---|---|
| Daemon mati / socket absent | Gate fail-open allow | sama seperti sekarang |
| Adapter Parse error | Gate fail-closed deny + log | provider kirim payload aneh |
| Provider gak support hook + GateEnabled=true | Spawn refuse, return error to caller | shouldn't happen (UI block toggle) tapi defensive |
| Capability probe timeout 10s | `HookError=timeout`, toggle locked off | retry via Rescan |
| Hook config write fail | Spawn error, abort | gak boleh spawn tanpa gate kalau user opt-in |
| Bypass flag + per-instance Hook ON | Spawner Strip hook config, run unguarded | Bypass intent (Slack/HTTP no UI) trump per-instance gate. Mencegah claude 2.1.138+ regression dimana hook fire tapi deny ignored. Lihat command-gate-claude-2.1-fix.md regression 2026-05-11. |
| User klik Enable per-provider sementara `agents.bypass_permissions=true` | HTTP handler return 409 + UI tampilin badge `locked (bypass)` | UI sembunyiin tombol Enable saat bypass on; 409 cuma kena kalau direct curl / stale tab. |
| User klik master gate toggle sementara bypass on | HTTP handler return 409 + tombol "Locked" disabled | Sama defensive guard. Ubah bypass dulu di `agents` config. |

## Migration / rollout

**Relationship: D-tasks vs Phases**

Priority 0 (D1–D8) = detection infrastructure, **independent** dari Phase 1-5 rollout sequence. Bisa landed paralel:

- D1, D2, D2b, D6 = pure infrastructure → bisa merged kapan saja, no user-visible change
- D3, D4, D5 = probe path → enables capability UI di Phase 2
- D7, D8 = tooling + test harness → support development semua phase

Phase 2 ready saat D1+D5+D6 hijau (capability registry + probe + cache).
Phase 3 (codex) ready saat semua D-task hijau + codex adapter shipped.

Phase 1 (refactor claude) bisa start paralel dengan D-tasks selama gak break existing whitelist behavior.

1. ✅ **Phase 1**: per-instance `Hooks` flag di userconfig, capability registry self-registering, master switch cascade, spawner consume per-instance intent. Existing claude path tetap jalan — `attachGateConfig` di factory now coexist sama new path (writer registries take precedence di Spawner.applyHookConfig).
2. ✅ **Phase 2**: UI Command Gate section per-card (Enable/Disable/Test), badge state machine (locked/testing/enabled/ready/disabled), theme-aware tooltip.
3. ✅ **Phase 3**: codex adapter + Spawner + Prober + capability probe shipped. User verifies via UI Test.
4. ✅ **Phase 4**: gemini adapter + Spawner + Prober shipped together. Badge `untested` scope sampai verified.
5. ⏳ **Phase 5**: hapus `attachGateConfig` legacy path di pool/factory.go (sekarang masih dipanggil untuk refresh spec.json AllowedCmds — perlu split jadi separate function biar gak nyentuh hook config).

Tiap phase shippable independen.

## Adding a new provider — contributor checklist

Tujuan: tambah provider keempat (mis. Aider) tanpa harus baca semua doc. Self-registering pattern bikin step ini lokal di satu folder, bukan tersebar.

```
[ ] 1. Constant
    - provider/provider.go: tambah `TypeAider Type = "aider"`
    - SupportedTypes() append TypeAider

[ ] 2. Spawner sub-package
    - provider/aider/spawn.go: impl provider.Spawner
    - Fields: Binary, <bypass-equivalent>, ExtraArgs
    - Argv verified dari `aider --help` binary terinstall
    - init() bisa Register ke factory dispatch (lihat step 5)

[ ] 3. Adapter
    - gate/adapter/aider/adapter.go: impl adapter.Adapter
    - Parse stdin shape Aider's hook payload → canonical Decision
    - Emit canonical Result → Aider's expected stdout + exit code
    - init() panggil adapter.Register(&adapter{})
    - Unit test golden stdin → stdout

[ ] 4. Hook config writer
    - provider/aider/hookconfig.go: WriteHookConfig + RemoveHookConfig
    - Path: `<sessionDir>/.aider/<hooks-file>` (verify lokasi konvensi Aider)
    - Unit test JSON shape

[ ] 5. Capability registration
    - provider/aider/capability_init.go:
        ```go
        func init() {
            capability.Register("aider", capability.Capability{
                HookSupported:  true,
                InterceptScope: "...", // depends on Aider docs
            })
        }
        ```

[ ] 6. Prober
    - provider/aider/prober.go: impl capability.Prober
    - SendSentinel: cara Aider terima prompt one-shot
    - init() register prober ke capability package

[ ] 7. Factory dispatch
    - Saat ini pool/factory.go switch by ProviderType. Idealnya factory baca
      dari spawner-registry juga (provider package register spawner factory di init),
      tapi sampai refactor itu landed, tambah case "aider" di switch.

[ ] 8. Gate binary blank-import
    - cmd/gate/main.go: tambah `_ "github.com/.../gate/adapter/aider"`
    - Tanpa ini adapter gak ke-register di binary final

[ ] 9. Test
    - argv shape test (unit)
    - capability probe test (integration, skip kalau binary gak install)
    - end-to-end smoke: deny envelope honored
```

**3 file central yang tetap harus disentuh** (sampai refactor self-registering spawner factory landed):
- `provider/provider.go` (constant)
- `pool/factory.go` (switch — bisa di-refactor jadi map lookup)
- `cmd/gate/main.go` (blank import)

Sisanya semua lokal di `provider/<name>/` + `gate/adapter/<name>/`. Aman buat scale 5–10 providers tanpa file-touching ledakan.

## Decided / open questions

**Decided:**
1. **Capability probe overhead** → cache by `Type + Path + Version`. Re-probe cuma kalau version berubah.
2. **`approve_always` lintas provider** → adapter **normalize tool name ke canonical** (`shell` → `Bash`, `apply_patch` → `Edit`). Match key seragam, user approve sekali apply ke semua provider. Trade-off: kalau user mau scope per-provider, harus bedain manual via UI nanti.
3. **Adapter registry** → self-registering via init() + blank import (lihat section "Adapter registry").
4. **Capability registry** → self-registering juga (lihat D1).

**Open:**
1. **`approve_session` saat dua session share cwd** — routing daemon by cwd ambigu. Tunda (jarang terjadi). Workaround: pindah ke session-ID injection lewat env var saat masalah jadi nyata.
2. **Hook config conflict dgn user-managed settings** — kalau user udah punya `.claude/settings.json` manual, write kita harus merge bukan overwrite. Verify saat refactor Phase 1. Saat ini repo udah tulis ke `.claude/settings.local.json` (lihat `attachGateConfig` di factory.go) — itu udah aman buat claude, tapi codex/gemini equivalent harus dicari.
3. **Spawner factory registry** — sekarang masih switch di `pool/factory.go`. Untuk full self-registering, butuh `spawnerFactory.Register("aider", func() Spawner { ... })` pattern. Tunda sampai providers > 4 (premature buat sekarang).

## See also

- [command-gate-architecture.md](command-gate-architecture.md) — design lama Claude-only (akan di-supersede setelah Phase 1 merge)
- [command-gate-claude-2.1-fix.md](command-gate-claude-2.1-fix.md) — incident report Claude contract change, motivasi adapter pattern
- [docs/guide/command-gate.md](../../docs/guide/command-gate.md) — user-facing guide
