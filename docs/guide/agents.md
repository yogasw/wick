---
outline: deep
---

# AI Agents

Wick **Agents** spawn AI CLIs (Claude, Codex, Gemini) as long-lived subprocesses, route messages from **Slack threads, Telegram chats, or the web UI** into them, and stream every event back into the dashboard.

The point isn't to wrap a model — it's to let the AI keep its native CLI runtime (its MCP servers, skills, memory, settings) while wick handles session storage, command approval, multi-instance config, and concurrency.

::: tip Why this is interesting
Most "AI agent" tools either lock you into their own runtime, or expose a chat-only UI. Wick goes the other way: bring your own Claude / Codex / Gemini install, and wick gives it a place to run safely with **multi-channel routing (Slack + Telegram + UI)**, multi-session concurrency, real workspaces on disk, and a per-command [approval gate](./command-gate). One agent — many ways to talk to it.
:::

## What you get

| Capability | Where |
|---|---|
| **Multi-channel routing** — same agent reachable from Slack, Telegram, and the web UI; each thread / chat / conversation = its own session | [Channels](./agents/channels) |
| **Multi-session concurrency** — pool caps subprocess count, FIFO-queues the rest, idle-kills + resumes via `--resume <cli_session_id>` | [Pool & Sessions](./agents/pool) |
| **Workspaces** — folders on disk (managed or any custom path) used as the agent's `cwd`. Multiple sessions can share one | [Workspaces](./agents/workspaces) |
| **Multi-instance providers** — two `claude/...` profiles with different PATs, plus codex / gemini side-by-side | [Providers](./agents/providers) |
| **Command Gate** — `<app>-gate` sidecar binary intercepts every Bash command for whitelist + 4-mode interactive approval | [Command Gate](./command-gate) |
| **AskUser MCP tool** — agent asks a question mid-turn, web UI renders a card, answer goes back as MCP tool result | (covered in [Channels ▶ Web UI](./agents/channels#web-ui)) |
| **Persistent state on disk** — everything under `~/.<app>/agents/`. Backup is `tar`. Restart re-scans, no DB migration. | _below_ |

## Quick tour

After boot, head to `/tools/agents`.

> **📸 Screenshot needed:** `agents-overview.png` — capture `/tools/agents` (Overview tab), showing pool stats + Active/Queue snapshot + recent sessions list. Save to `docs/public/screenshots/agents-overview.png`.

| Page | What you do |
|---|---|
| **Overview** | Pool stats (active / max / queue), running list, recent sessions. |
| **Sessions** | List, open, delete sessions. Detail tabs: Conversation, Commands (gate audit), Raw events. Composer at the bottom posts a new message. |
| **Workspaces** | Create / delete workspaces. New = empty managed folder unless pointed at a custom path. |
| **Presets** | Edit reusable agent instructions. Each preset is one `agent.md` file. The built-in `default` preset is the fallback when a session has no workspace (or the workspace has no `DefaultPreset`); it cannot be deleted, only edited. |
| **Providers** | Per-instance status cards: binary path, version, env vars, extra args, "Rescan" button. Add custom instances when you need two PATs for the same CLI. |
| **Channels** | Slack + Telegram bot config (tokens, access control, default workspace). Web UI is always-on. |

Sessions auto-create on the first message in a Slack thread, a Telegram chat, or a fresh web conversation. You don't pre-allocate them.

## Storage layout

Everything lives under `~/.<app>/agents/`. The `<app>` part is whatever the binary's name resolves to ([appname.Resolve()](https://github.com/yogasw/wick/blob/master/internal/appname): strip `.exe`, strip `-gate` suffix, fall back to ldflag → `wick.yml` name → `"wick"`).

```
~/.<app>/agents/                          ← see internal/agents/config/layout.go
│
├── presets/
│   └── default/agent.md
│
├── workspaces/                           ← managed workspace metadata (custom paths live elsewhere)
│   └── <name>/
│       ├── meta.json                     ← workspace.Meta — custom_path, default_preset, ...
│       └── files/                        ← managed cwd (skipped when CustomPath is set)
│
├── sessions/
│   ├── T123ABC/                          ← Slack thread_ts
│   │   ├── meta.json                     ← session.Meta — workspace ref, status, pending_input
│   │   ├── agents.json                   ← []AgentEntry — cli_session_id, status per agent
│   │   ├── agent.md                      ← snapshot of the active preset
│   │   ├── conversation.jsonl            ← user/assistant turns (append-only)
│   │   ├── commands.jsonl                ← legacy per-session gate log
│   │   └── raw.jsonl                     ← raw stream events (optional)
│   └── 9b7e-uuid/                        ← web origin uses a UUID
│
├── providers/
│   └── spawns/
│       └── claude__work__T123ABC__1715167891234.jsonl   ← per-spawn start/exit log
│
└── gate/                                 ← shared command gate state (see Command Gate guide)
    ├── spec.json
    ├── gate.sock
    └── commands.jsonl
```

Daily wick logs (server, worker, app, gate tail) live one level up at `~/.<app>/logs/`.

::: info JSON vs JSONL
- `*.json` — small metadata, atomic write (tmp file + rename). See [storage.WriteJSON](https://github.com/yogasw/wick/blob/master/internal/agents/storage).
- `*.jsonl` — append-only logs. First line is a `_meta` header; readers skip lines containing `_meta`.
:::

## Agents config (general knobs)

The agents subsystem reads its top-level knobs from the `configs` table under owner `agents`. Edit from `/admin/configs` → group **Agents**.

> **📸 Screenshot needed:** `agents-general-config.png` — capture `/admin/configs` filtered to the `agents` group, showing the General fields table. Save to `docs/public/screenshots/agents-general-config.png`.

Source: [`config.GeneralConfig`](https://github.com/yogasw/wick/blob/master/internal/agents/config/general.go)

| Field | Default | What it does |
|---|---|---|
| `Enabled` | `false` | Master switch. Off = listeners don't start, pool doesn't run. |
| `MaxConcurrent` | `2` | Subprocess cap across all sessions. |
| `IdleTimeoutSec` | `120` | Seconds without I/O before subprocess is killed. |
| `KillAfterIdleSec` | `0` | Extra grace seconds after idle timeout before kill. |
| `DefaultProvider` | `claude` | Provider type used when a session doesn't specify one. |
| `BypassPermissions` | `false` | Pass `--permission-mode bypassPermissions` to Claude. Turn on if Claude is prompting for permission in Slack / HTTP sessions and you don't have a gate. |
| `PublicURL` | _(empty)_ | Base URL of this wick instance. Used to build `/dashboard` meta-command links. |
| `AutoRescan` | `true` | Re-probe provider binaries when cached version is older than 24h. Off = manual Rescan only. |
| `PreemptIdle` | `true` | When the pool is full and a new session is queued, kill the longest-idle active subprocess to free its slot instead of waiting out the idle TTL. Killed sessions resume via `--resume` on their next message. A 1 s background loop keeps retrying preemption while the queue is non-empty so a session that goes idle after a queued send still releases its slot promptly. |
| `SystemPrompt` | _(embedded baseline)_ | Global interaction rules appended to every preset's `agent.md` on spawn. Adds to the preset — never replaces it. Edit and reset the default from `/tools/agents/settings`; the shipped baseline is [`internal/agents/config/system_prompt_default.md`](https://github.com/yogasw/wick/blob/master/internal/agents/config/system_prompt_default.md). |

## Diagnostics

```bash
wick doctor                # checks the wick binary's own gate setup
wick doctor wick-lab.exe   # inspect a different branded build's gate setup
```

Each check reports `✓` / `✗` / `!`. Exit `0` when required checks pass, `1` otherwise. The gate-specific section is detailed in the [Command Gate guide](./command-gate#diagnostics).

## Sub-pages

- [**Workspaces**](./agents/workspaces) — folders on disk; managed vs custom path; built-in `default`.
- [**Providers**](./agents/providers) — multi-instance config, binary resolution chain, status cache.
- [**Channels**](./agents/channels) — Slack, Telegram, web UI; access control; meta-commands.
- [**Pool & Sessions**](./agents/pool) — slot allocation, idle-kill, resume, message buffer.
- [**Command Gate**](./command-gate) — shell-command approval system.

## See also

- [Environment Variables](../reference/env-vars) — `APP_NAME` namespacing for `~/.<app>/`.
- [`wick build`](../reference/build) — `--installer` flag bundles the gate sidecar.
- [MCP for LLMs](./mcp) — wick's MCP surface that agents call back into for connectors.
