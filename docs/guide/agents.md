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
| **Workflows** — YAML DAG of classify / agent / connector / http / channel / datatable / branch / parallel nodes triggered by cron, channel events, webhooks, or manual runs; replayable per-run state on disk; visual canvas + MCP `workflow_*` ops | [Workflows](/workflow/) |
| **Workspaces** — folders on disk (managed or any custom path) used as the agent's `cwd`. Multiple sessions can share one | [Workspaces](./agents/workspaces) |
| **Multi-instance providers** — two `claude/...` profiles with different PATs, plus codex / gemini side-by-side | [Providers](./agents/providers) |
| **Skills Manager** — browse, preview, sync, and delete skill `.md` files across all provider skill dirs (`~/.claude/skills`, `~/.codex/skills`, `~/.gemini/skills`) from one UI | [Skills Manager](./agents/skills-manager) |
| **Command Gate** — `<app>-gate` sidecar binary intercepts every Bash command for whitelist + 4-mode interactive approval | [Command Gate](./command-gate) |
| **AskUser MCP tool** — agent asks a question mid-turn, web UI renders a card, answer goes back as MCP tool result | (covered in [Channels ▶ Web UI](./agents/channels#web-ui)) |
| **Context file panel** — slide-over on session detail to browse / read / edit / download / delete files in the agent's working directory, with markdown + HTML preview and Ace syntax highlighting | _[below](#context-file-panel)_ |
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
| **Skills** | Browse, preview, sync, and delete skill files across all provider skill dirs. |
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

## Context file panel

Every session-detail page has a vertically-labeled **Context** tab pinned to the right edge of the chat (shortcut `Ctrl+B`). Click or hit the shortcut and a slide-over panel reveals a file tree rooted at the session's resolved `cwd` — the same directory the agent process spawns in (a managed workspace's `files/`, the workspace's `CustomPath`, or the per-session fallback `<SessionDir>/cwd/`).

The panel is the single place to inspect what the agent has actually written, without leaving the chat.

| Action | How |
|---|---|
| **Browse** | Folder tree, click a directory to expand. Filter box does substring match on the full relative path. |
| **Read** | Click a file → opens a modal with preview tab. Markdown gets sanitized HTML (via `marked` + `DOMPurify`), HTML renders in a sandboxed iframe (`sandbox="allow-scripts"`, no same-origin), images / PDFs render inline, plain text shows in a `<pre>`. Binary files and files over 2 MiB show a "download instead" affordance. |
| **Edit** | Switch to the Edit tab — Ace editor mounts with syntax mode picked from the file extension (JS, Go, MD, HTML, CSS, JSON, YAML, Python, etc). Save writes back to disk. |
| **Download** | Per-row download icon or modal header button. Filename in `Content-Disposition` is stripped of CR/LF/quote/backslash to defend against header injection. |
| **Create** | `+ file` and `+ folder` icons in the panel header, plus a "new file here" icon on hover of each folder row. |
| **Delete** | Per-row trash icon. Folders recurse. The session `cwd` itself is refused. |

Heavy build artifact directories (`node_modules`, `.git`, `.venv`, `__pycache__`, `dist`, `build`, `target`, `.cache`, `.next`) are pruned from the walk and the listing caps at 5000 entries so the panel stays responsive on large workspaces.

### Sandbox

Every file operation routes through a single `safeJoin(cwd, rel)` that rejects:

- absolute paths and any segment equal to `..`
- Windows drive letters / UNC volume specs
- NUL bytes
- backslash separators (normalized so `\` and `/` are treated the same)
- symlink escapes — `EvalSymlinks` walks the deepest existing prefix of the target and re-checks the resolved path is still inside the resolved `cwd`

Combined with the existing `/tools/agents/*` `RequireToolAccess` middleware + session-ownership check, the panel can only ever touch the cwd of a session the logged-in user can already see. The agent process itself already has full read/write access to that directory, so the panel doesn't grant new authority — it just exposes the same scope through a UI.

### Vendored libs

Ace + `marked` + `DOMPurify` are vendored locally under [`internal/tools/agents/js/vendor/`](https://github.com/yogasw/wick/tree/master/internal/tools/agents/js/vendor) (`go:embed`-ed at build time and served from `/tools/agents/static/js/vendor/`). The Ace bundle is trimmed to ~1.5 MB — only the modes wick is likely to encounter in a session cwd are kept (JS, TS, Go, Python, MD, HTML, CSS, JSON, YAML, SH, SQL, Java, Rust, Ruby, PHP, C/C++, Dockerfile, PowerShell, TOML, XML). No third-party CDN is contacted at runtime, so the panel works on an air-gapped install.

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
