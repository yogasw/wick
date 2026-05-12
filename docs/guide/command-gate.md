---
outline: deep
---

# Command Gate

The Command Gate is wick's shell-command approval system for AI agents. Every shell command an agent wants to run goes through a sidecar binary (`<app>-gate`) that either approves it from a whitelist, blocks it, or asks you in real time via the web UI.

::: tip Prerequisite
The gate is part of the [Agents subsystem](./agents). If you're not running agents, you don't need it.
:::

## Why it exists

An agent running as a subprocess can call `Bash` tools at any time. Without a gate, there's no point at which you can intervene before the command runs.

```
User: "delete old logs"
Agent: [runs: find /var/log -mtime +30 -delete]
       ← already executed, nothing stopped it
```

Provider CLIs expose a pre-execution hook — the gate is the binary called by that hook. The hook fires before the tool executes; the gate either allows or denies, and the provider acts accordingly.

### Provider hook contracts

| Provider | Hook name | Allow output | Deny output | Exit (deny) |
|---|---|---|---|---|
| Claude (≥ 2.1.138) | `PreToolUse` | `{"hookSpecificOutput":{"permissionDecision":"allow"}}` | `{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"..."}}` | **0** |
| Codex (0.129+) | `PreToolUse` | `{"permissionDecision":"allow"}` | `{"permissionDecision":"deny","reason":"..."}` | 0 or 2 |
| Gemini | `BeforeTool` | exit 0 | `{"decision":"deny","reason":"..."}` | 2 |

The gate uses an **adapter per provider** (`internal/agents/gate/adapter/<provider>/`) that translates between the provider-specific stdin/stdout shape and the canonical internal protocol. This isolates contract drift — when Claude changed from `exit 2` to `exit 0 + JSON` in v2.1.138, only the claude adapter changed; everything else was untouched. See [command-gate-claude-2.1-fix.md](https://github.com/yogasw/wick/blob/master/internal/docs/command-gate-claude-2.1-fix.md) for the full incident write-up.

We do **not** pass bypass flags (`--permission-mode bypassPermissions`, `--ask-for-approval=never`, etc.) when a gate is attached — those flags suppress the hook entirely, defeating the gate. The bypass flag and per-instance gate are mutually exclusive; factory enforces this.

## Per-instance gate toggle

Gate is **off by default** for every provider instance. Turn it on per-instance from the Providers page → Command Gate section.

When you enable it, wick runs a **capability probe**: it spawns the provider with a force-deny hook (`<app>-gate --probe`), asks it to touch a sentinel file, and verifies the file was not created. Green = hook honored; red = gate locked off with a "not supported in this version" banner. The probe result is cached for 1 hour (re-run via the **Test** button).

### Master switch

The master gate switch on the Providers page fans out — toggle ON sets every instance's per-instance flag and kicks off background probes; toggle OFF clears all flags. Single source of truth is always the per-instance `Hooks["PreToolUse"].Enabled` field on disk.

**Bypass lock**: if `bypass_permissions` is set in agents config (non-interactive channels), the master switch shows a `locked (bypass)` badge and refuses toggles. Turn bypass off first.

### Capability badges per provider card

| State | Badge |
|---|---|
| `bypass_permissions` on | `locked (bypass)` |
| Master off | `locked` |
| Master on + probe in flight | `testing…` |
| Master on + intent on + verified | `enabled ✓` |
| Master on + intent on + unverified | `enabled (unverified)` |
| Master on + intent off + probe passed | `ready` |
| Master on + intent off | `disabled` |

### Intercept scope per provider

| Provider | Scope |
|---|---|
| claude | All tools — Bash, Read, Write, Edit, Glob, MCP tools, and any future tools |
| codex | Shell commands only |
| gemini | Untested — adapter shipped, runtime unverified |

Scope is shown as a badge on the Providers card so you know what the gate actually covers.

## What gets intercepted (Claude)

The gate uses a catch-all `.*` matcher, so **every tool call** routes through the gate:

| Tool type | Gate behavior |
|---|---|
| **Bash** | Whitelist check → auto-approved check → ask user |
| **Read / Write / Edit / Glob** | Scope check — if path is within the workspace `default_scope`, auto-allow; otherwise ask user |
| **MCP tools** (e.g. `mcp__support-tools__wick_execute`) | Always ask user (no scope to check) |
| **Unknown / future tools** | Always ask user |

File tools within the workspace scope are auto-allowed without a popup, so normal agent file operations stay fast. Only out-of-scope file access and all MCP/shell calls prompt you.

## Approval modes

When a tool call isn't auto-approved, the gate dials a Unix socket and the daemon broadcasts an SSE event. The web UI renders a modal with four choices:

| Button | API value | Scope | Effect on future matching calls |
|---|---|---|---|
| **Approve once** | `approve_once` | This request only | Modal again next time |
| **Allow this session** | `approve_session` | While the session lives (in-memory) | Auto-approved silently for this session |
| **Always allow** | `approve_always` | Persistent (written to `spec.json`) | Auto-approved across restarts, all sessions |
| **Block** | `block` | This request only | Tool call cancelled; modal again next time |

A countdown shows the remaining 25 seconds. If you don't answer, the daemon auto-blocks.

The "Approved commands" panel on the session detail page lists every `approve_always` rule with a Revoke button. Match key is a hash of `tool + cmd`; tool names are normalized to canonical (`shell` → `Bash`, `apply_patch` → `Edit`) so an `approve_always` from one provider applies across providers.

## Architecture

```
Provider subprocess (claude / codex / gemini)
  │
  │ provider-specific hook fires (PreToolUse / BeforeTool / …)
  ▼
<app>-gate --provider=<name>  (stateless, short-lived, one per command)
  │
  ├─ stdin: provider-specific hook payload
  ├─ adapter.Parse → canonical Decision {Tool, Cmd, Cwd, RequestID, Provider}
  ├─ read shared spec.json
  │     └─ AutoApproved hit? → adapter.Emit allow (zero-latency hot path)
  │     └─ whitelist rule match? → adapter.Emit allow
  │
  ├─ dial Unix socket: ~/.<app>/agents/gate/gate.sock
  ├─ send Decision (raw JSON, newline-delimited)
  │
  ▼
Daemon (in main wick process)
  │
  ├─ route by cwd → which session owns this hook payload?
  ├─ approve_session cache hit? → auto-reply Result{Allow:true}
  ├─ broadcast SSE event to web UI
  │
  ▼
Web UI modal → user clicks Approve / Reject
  │
  ▼
POST /api/agents/sessions/{id}/approve  →  daemon
  │
  ▼
Daemon sends Result {Allow, Reason} back through socket
  │
  ▼
Gate: adapter.Emit → provider-specific stdout envelope, exit 0
```

The whole path runs inside the provider's 30-second hook timeout. The daemon's own deadline is 25s, so the gate always exits before the provider gives up.

### Canonical gate ↔ daemon protocol

Internal only — never crosses the provider boundary:

```go
// gate → daemon
type Decision struct {
    Tool      string          `json:"tool"`
    Cmd       string          `json:"cmd"`
    Cwd       string          `json:"cwd"`
    RequestID string          `json:"request_id"`
    Provider  string          `json:"provider"`   // audit only
    Probe     bool            `json:"probe,omitempty"`
    Raw       json.RawMessage `json:"raw,omitempty"`
}

// daemon → gate
type Result struct {
    Allow  bool   `json:"allow"`
    Reason string `json:"reason,omitempty"`
}
```

Binary decision only — approval scope (`once / session / always`) lives entirely in the daemon.

## File layout

Everything is shared per-app at `~/.<app>/agents/gate/`:

```
~/.<app>/agents/gate/
├── spec.json          ← whitelist rules + AutoApproved list (daemon-owned)
├── gate.sock          ← Unix domain socket, chmod 0600
└── commands.jsonl     ← machine-readable audit trail (multi-stage entries)
```

Plus a per-day human-readable tail log alongside the other wick logs:

```
~/.<app>/logs/gate-YYYY-MM-DD.log
```

::: info Why one shared spec/socket/log
Earlier iterations gave each session its own socket directory. Approvals are an app-wide concern (`approve_always` should mean the same thing across every session) and the daemon routes to the right session by matching the hook's `cwd` against known workspace paths. One listener, one spec, one audit log.
:::

### `commands.jsonl` format

Multi-stage trail per invocation, tied together by `RequestID`:

```jsonl
{"ts":"...","stage":"received","request_id":"r-abc","tool":"Bash","cmd":"git status"}
{"ts":"...","stage":"socket_dial","request_id":"r-abc"}
{"ts":"...","stage":"socket_sent","request_id":"r-abc"}
{"ts":"...","stage":"socket_recv","request_id":"r-abc"}
{"ts":"...","stage":"terminal","request_id":"r-abc","decision":"approve_once","match_key":"..."}
```

Filter by `request_id` to follow one command end-to-end. The session detail Commands tab filters by workspace cwd prefix.

### Daily tail log

`~/.<app>/logs/gate-YYYY-MM-DD.log` is a human-readable mirror, one line per stage transition:

```
2026-05-10T06:36:34Z info  cmd=git status  status=allowed  decision=whitelist
2026-05-10T06:36:40Z info  cmd=rm -rf .    status=blocked  decision=block  reason="user blocked"
```

Best-effort — write errors are swallowed so the gate never crashes on logging failure.

## Binary resolution

`<app>` is derived at runtime from the gate executable's filename: strip `.exe`, strip the `-gate` suffix. So `wick-lab-gate.exe` → `wick-lab` → paths land in `~/.wick-lab/agents/gate/`. The main wick binary uses the same chain on its own filename, so they always agree on the directory.

`ResolveGateBinary` finds the gate sidecar in this order — first hit wins:

1. **sibling-of-executable** — `<app>-gate[.exe]` next to the main binary. **Primary path** when installed via `wick build --installer` (MSI / .deb / .app ships the sidecar alongside the main binary).
2. **embedded extract** — `//go:embed assets/gate-<os>-<arch>` unpacked once into a temp location. Backup for portable `.exe` / source builds.
3. **PATH** — `exec.LookPath("<app>-gate")`. Last-ditch.

No environment variables in the chain. `WICK_GATE_BIN`, `GATE_BIN`, `WICK_GATE_SPEC`, `GATE_SPEC` were all dropped.

## Building & shipping

`wick build` compiles `cmd/gate/` as part of its pipeline (no separate CI step). Output goes two places:

- `internal/agents/gate/assets/gate-<os>-<arch>[.exe]` — picked up by `//go:embed`, shipped inside the main binary.
- `bin/<app>-gate-<os>-<arch>[.exe]` — sibling artifact for distribution.

`wick build --installer` packages the sidecar into the platform-native installer:

| OS | Where the sidecar lands |
|---|---|
| Windows MSI | Same folder as `<App>.exe` (`%LocalAppData%\Programs\<AppName>\<App>-gate.exe`) |
| Linux .deb | `/usr/bin/<app>-gate` |
| macOS .app bundle | `Contents/MacOS/<App>-gate` |

If your fork strips `cmd/gate/`, the builder soft-skips — the gate won't be available and the Providers page shows a "gate disabled" banner.

## Whitelist rules (`spec.json`)

The shared `spec.json` carries two things:

```json
{
  "rules": [
    { "tool": "Bash", "match": "git status*" },
    { "tool": "Bash", "match": "ls *" }
  ],
  "auto_approved": [
    { "tool": "Bash", "cmd": "git pull origin main" }
  ]
}
```

- `rules` — glob patterns evaluated by the gate without a socket round-trip. Edit from `/admin/configs` under the `agents` group; the daemon rewrites `spec.json` on save and on every Build invocation.
- `auto_approved` — exact matches added by **Always allow** in the modal. Same hot-path: gate reads, matches, emits allow — no daemon round-trip.

## Diagnostics

```bash
wick doctor
wick doctor wick-lab.exe    # inspect a branded build
```

The gate section of the doctor report:

| Check | What it verifies |
|---|---|
| `gate app_name` | `<app>` derived from the binary filename |
| `gate binary` | `<app>-gate[.exe]` resolves via sibling / embed / PATH |
| `gate name match` | Gate binary stem matches `<app>` (socket paths align) |
| `gate socket` | Socket path the daemon would use |
| `gate round-trip` | Dial socket + send probe — `Probe: true` skips the pending queue and daemon auto-replies, proving full encode → decode path |
| `gate spec` | `spec.json` exists and is non-empty |

A failing round-trip is the most useful signal: binary resolves but daemon isn't listening, or AppName mismatch between gate binary and running daemon.

### Test gate button

The Providers page has a per-card **Test gate** button. It spawns the provider with a force-deny hook in a temp workspace, asks it to touch a sentinel file, and reports whether the file was created. Green = deny envelope honored; red = gate effectively bypassed.

Use this as a smoke test after upgrading a provider CLI — the contract has changed before without warning.

## Failure modes

| Situation | Behavior | What you'll see |
|---|---|---|
| Daemon not running | `connect()` → "no such file" → **fail-open** allow | Command runs unconditionally |
| Daemon hangs (25s) | Deadline fires → `block reason=timeout` → deny envelope | Modal disappears mid-render; `commands.jsonl` entry has `decision=block reason=timeout` |
| `spec.json` missing | `LoadSpec` returns empty Spec → falls through to socket dial | Every command hits the modal |
| Stdin missing / malformed | 3s read timeout → deny envelope | Look at the daily tail log |
| Adapter parse error | fail-closed deny + log | Provider sent unexpected payload shape |
| Provider capability probe timeout | `HookError=timeout` → toggle locked off | Retry via **Test** button on Providers page |
| Bypass flag + gate ON | Spawner strips hook config, runs unguarded | Gate won't fire — bypass intent (non-interactive channels) takes precedence |

Infrastructure failures outside wick's control fail open; failures inside the gate (malformed stdin, post-dial hang) fail closed.

## Two patterns of approval

Wick uses **system-intercept** approval (the gate enforces the prompt). The other pattern, **voluntary ask** (the AI asks via a tool call), can't be enforced — the agent can forget to call it. The harness-level `AskUserQuestion` tool also isn't available when the provider runs in pipe mode.

Wick ships a separate **AskUser MCP tool** for the case where an agent legitimately wants to ask you a question mid-turn. It bridges to a web-UI card via SSE but is voluntary and orthogonal to the gate.

## Adding a new provider

See [command-gate-multi-provider.md](https://github.com/yogasw/wick/blob/master/internal/docs/command-gate-multi-provider.md) for the full contributor checklist. Short version:

1. `provider/provider.go` — add `TypeFoo Type = "foo"`
2. `gate/adapter/foo/` — implement `adapter.Adapter` (Parse + Emit), register via `init()`
3. `provider/foo/hookconfig.go` — `WriteHookConfig` + `RemoveHookConfig`
4. `provider/foo/spawn.go` — implement `provider.Spawner`, skip bypass flag when gate active
5. `provider/foo/prober.go` — implement `capability.Prober`, register via `init()`
6. `provider/foo/capability_init.go` — `capability.Register("foo", ...)` in `init()`
7. `cmd/gate/main.go` — add blank import for the adapter
8. `pool/factory.go` — add `case "foo"` to spawner dispatch switch

## See also

- [AI Agents](./agents) — sessions, workspaces, providers.
- [`wick build`](../reference/build) — `--installer` flag bundles the gate sidecar.
- [Environment Variables](../reference/env-vars) — `APP_NAME` namespacing for `~/.<app>/`.
