---
outline: deep
---

# Command Gate

The Command Gate is wick's shell-command approval system for AI agents. Every Bash command Claude wants to run goes through a sidecar binary (`<app>-gate`) that either approves it from a whitelist, blocks it, or asks you in real time via the web UI.

::: tip Prerequisite
The gate is part of the [Agents subsystem](./agents). If you're not running agents, you don't need it.
:::

## Why it exists

Claude as a subprocess can call `Bash` tools at any time. Without a gate, there's no point at which you can intervene before the command runs.

```
User: "delete old logs"
Claude: [runs: find /var/log -mtime +30 -delete]
        ← already executed, nothing stopped it
```

The Claude CLI's `PreToolUse` hook lets an external binary approve or block each tool call. The gate is that binary, and it always exits 0 — the decision is conveyed via stdout JSON:

```
exit 0 + {"hookSpecificOutput": {"permissionDecision": "allow", ...}}  → command runs
exit 0 + {"hookSpecificOutput": {"permissionDecision": "deny",  ...}}  → command cancelled
exit 0 + (no JSON)                                                     → falls through to claude's permission flow
```

Earlier wick releases used `exit 2` for the block path. Claude Code 2.1.138+ ignores stdout when the exit code is non-zero, so the deny envelope was lost and the tool ran anyway. The current gate emits both an explicit allow and an explicit deny envelope on every decision; see [command-gate-claude-2.1-fix.md](https://github.com/yogasw/wick/blob/master/internal/docs/command-gate-claude-2.1-fix.md) for the full incident write-up.

Claude Code also has a built-in TUI permission dialog. Wick suppresses it via the gate's allow envelope (`permissionDecision: "allow"` skips the prompt) — the goal is a single web-UI surface for approvals, not a terminal prompt. We do **not** pass `--permission-mode bypassPermissions` when a gate is attached, because that flag also skips the PreToolUse hook in current claude builds.

## Approval modes

When a command isn't in the whitelist, the gate dials a Unix socket and the daemon broadcasts an SSE event. The web UI renders a modal with four choices:

| Mode | API value | Scope | Effect on future matching commands |
|---|---|---|---|
| **Approve once** | `approve_once` | This request only | Modal again next time |
| **Allow this session** | `approve_session` | While the session lives (in-memory) | Auto-approved silently |
| **Always allow** | `approve_always` | Persistent (written to `spec.json`) | Auto-approved across restarts |
| **Reject** | `block` | This request only | Modal again next time |

A countdown shows the remaining 25 seconds. If you don't answer, the daemon auto-blocks.

The "Approved commands" panel on the session detail page lists every `approve_always` rule with a Revoke button. Match key is a hash of `tool + cmd`; matching is exact for now.

## Architecture

```
Claude (long-lived subprocess)
  │
  │ PreToolUse hook fires
  ▼
<app>-gate (short-lived, one per command)
  │
  ├─ stdin: hook JSON ({tool, cmd, cwd, ...})
  ├─ read shared spec.json
  │     └─ command in AutoApproved? → emit allow envelope (zero-latency hot path)
  │     └─ matches whitelist rule?  → emit allow envelope
  │
  ├─ dial Unix socket: ~/.<app>/agents/gate/gate.sock
  ├─ send ApprovalRequest (raw JSON, newline-delimited)
  │
  ▼
Daemon (in main wick process)
  │
  ├─ route by cwd → which session does this hook payload belong to?
  ├─ approve_session cache hit? → auto-reply, no UI prompt
  ├─ broadcast SSE event to web UI
  │
  ▼
Web UI modal → user clicks Approve / Reject
  │
  ▼
POST /api/agents/sessions/{id}/approve  →  daemon
  │
  ▼
Daemon sends ApprovalResponse back through socket
  │
  ▼
Gate emits allow or deny envelope on stdout (exit 0 either way) → Claude continues or aborts
```

The whole path runs inside Claude's 30-second hook timeout. The daemon's own deadline is 25s, so the gate always exits before Claude gives up with an ambiguous "hook timeout" message.

## File layout

Everything is shared per-app at `~/.<app>/agents/gate/`:

```
~/.<app>/agents/gate/
├── spec.json          ← whitelist rules + AutoApproved list (gate reads on every call)
├── gate.sock          ← Unix domain socket, chmod 0600
└── commands.jsonl     ← machine-readable audit trail (multi-stage entries)
```

Plus a per-day human-readable tail log alongside the other wick logs:

```
~/.<app>/logs/gate-YYYY-MM-DD.log
```

::: info Why one shared spec/socket/log
Earlier iterations gave each session its own socket directory. Real use revealed that approvals are an app-wide concern (an `approve_always` should mean the same thing across every session) and that the daemon can route to the right session by matching the hook's `cwd` against known workspace paths. One listener, one spec, one audit log.
:::

### `commands.jsonl` format

The gate emits a multi-stage trail per invocation, all entries tied together by `RequestID`:

```jsonl
{"ts":"...","stage":"received","request_id":"r-abc","tool":"Bash","cmd":"git status"}
{"ts":"...","stage":"socket_dial","request_id":"r-abc"}
{"ts":"...","stage":"socket_sent","request_id":"r-abc"}
{"ts":"...","stage":"socket_recv","request_id":"r-abc"}
{"ts":"...","stage":"terminal","request_id":"r-abc","decision":"approve_once","match_key":"..."}
```

Filter by `request_id` to follow one command end-to-end. The session detail Commands tab filters by workspace cwd prefix when displaying.

### Daily tail log

`~/.<app>/logs/gate-YYYY-MM-DD.log` is a human-readable mirror, one line per stage transition:

```
2026-05-10T06:36:34Z info  cmd=git status  status=allowed  decision=whitelist
2026-05-10T06:36:40Z info  cmd=rm -rf .    status=blocked  decision=block  reason="user blocked"
```

Best-effort — write errors are swallowed so the gate never crashes on logging failure. Aimed at operators who want `tail -f` on the gate stream while debugging "did the hook even fire?"

## Binary resolution

`<app>` is derived at runtime from the gate executable's filename: strip `.exe`, strip the `-gate` suffix. So `wick-lab-gate.exe` → `wick-lab` → paths land in `~/.wick-lab/agents/gate/`. The main wick / wick-lab binary uses the same chain on its own filename, so they always agree on the directory.

`ResolveGateBinary` finds the gate sidecar in this order — first hit wins:

1. **sibling-of-executable** — `<app>-gate[.exe]` next to the main binary. **This is the primary path** when the app was installed via `wick build --installer`: the MSI / .deb / .app bundle ships the sidecar alongside the main binary.
2. **embedded extract** — `//go:embed assets/gate-<os>-<arch>` is unpacked once into a temp location. Backup for portable `.exe` / source builds where there's no installer to ship the sidecar.
3. **PATH** — `exec.LookPath("<app>-gate")`. Last-ditch.

There are no environment variables in the chain. `WICK_GATE_BIN`, `GATE_BIN`, `WICK_GATE_SPEC`, `GATE_SPEC` were dropped — installer-shipped sidecar is more reliable, and dev `go run` simply runs `wick build` once to produce `bin/<app>-gate[.exe]` for the sibling lookup to find.

## Building & shipping

`wick build` compiles `cmd/gate/` as part of its pipeline (no separate CI step). The output goes two places:

- `internal/agents/gate/assets/gate-<os>-<arch>[.exe]` — picked up by `//go:embed` and shipped inside the main binary.
- `bin/<app>-gate-<os>-<arch>[.exe]` — sibling artifact for distribution.

`wick build --installer` packages the sidecar into the platform-native installer:

| OS | Where the sidecar lands |
|---|---|
| Windows MSI | Same folder as `<App>.exe` (`%LocalAppData%\Programs\<AppName>\<App>-gate.exe`) |
| Linux .deb | `/usr/bin/<app>-gate` |
| macOS .app bundle | `Contents/MacOS/<App>-gate` |

If your fork strips `cmd/gate/`, the builder soft-skips this step — the gate just won't be available, and the [Providers page](./agents#diagnostics) will show a "gate disabled" banner.

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

- `rules` — glob patterns, evaluated by the gate without socket round-trip. Edit from `/admin/configs` under the `agents` group; the daemon rewrites `spec.json` on save and on every Build invocation.
- `auto_approved` — exact matches added by clicking **Always allow** in the modal. Same hot-path: gate reads, matches, exits 0 with no daemon round-trip.

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
| `gate name match` | The gate binary's stem matches `<app>` (so socket paths align) |
| `gate socket` | The socket path the daemon would use |
| `gate round-trip` | Dial the socket and send a probe request — `Probe: true` skips the pending queue and the daemon auto-replies, so this proves the full encode → decode path without bothering a human |
| `gate spec` | `spec.json` exists and is non-empty |

A failing round-trip is the most useful signal: the binary resolves, but the daemon isn't listening. Usually that means wick isn't running, or the AppName derived from the gate binary doesn't match the AppName the running daemon used to choose its socket directory.

### Test gate button

The Providers page has a per-card **Test gate** button (claude only). It spawns claude with a force-deny PreToolUse hook in a temp workspace, asks it to touch a sentinel file, and reports whether the file got created. Green = the deny envelope was honored; red = the installed claude version no longer respects the contract and the gate is effectively bypassed.

Use this as a smoke test after upgrading claude — the contract has changed twice (top-level `decision` → `hookSpecificOutput`, exit-2 → exit-0+JSON), and the symptom is silent: sessions look fine until you actually try to block something.

## Failure modes

| Situation | What the gate does | What you'll see |
|---|---|---|
| Daemon not running | `connect()` returns "no such file" or "connection refused" → **fail-open** allow envelope | Command runs unconditionally — wick isn't up to mediate, so the gate gets out of the way rather than blocking the user's shell. |
| Daemon hangs (25s) | Daemon-side deadline fires → sends `block` with `reason=timeout` → gate emits deny envelope | Modal disappears mid-render; commands.jsonl entry has `decision=block reason=timeout`. |
| `spec.json` missing | `LoadSpec` returns empty Spec, no error → gate falls through to socket dial | Same as no whitelist — every command hits the modal. |
| Stdin missing / malformed | 3s read timeout on stdin → gate emits deny envelope | Hook payload didn't reach the gate. Look at the daily tail log. |

Default behavior splits by failure type: infrastructure failures *outside* wick's control (no daemon at all) fail open so the user's shell isn't held hostage by a half-installed wick; failures *inside* the gate (malformed stdin, post-dial hang) fail closed so a half-broken gate can't quietly let commands through.

## Two patterns of approval

Wick uses **system-intercept** approval (the gate is the system enforcing the prompt). The other pattern, **voluntary ask** (the AI itself asks via a tool call like `AskUserQuestion`), can't be enforced — Claude can forget to call it. And the harness-level `AskUserQuestion` tool isn't available when Claude runs as a subprocess (`-p` pipe mode), only inside the Claude Code TUI.

Wick does ship a separate **AskUser MCP tool** for the rare case when an agent legitimately wants to ask you a question mid-turn. That tool also bridges to a web-UI card via SSE, but it's voluntary and orthogonal to the gate.

## See also

- [AI Agents](./agents) — sessions, workspaces, providers.
- [`wick build`](../reference/build) — `--installer` flag bundles the gate sidecar.
- [Environment Variables](../reference/env-vars) — `APP_NAME` namespacing for `~/.<app>/`.
