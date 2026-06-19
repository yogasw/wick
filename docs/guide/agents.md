---
outline: deep
---

# AI Agents

Wick **Agents** spawn AI CLIs (Claude, Codex, Gemini) as long-lived subprocesses, route messages from **Slack threads, Telegram chats, or the web UI** into them, and stream every event back into the dashboard.

The point isn't to wrap a model — it's to let the AI keep its native CLI runtime (its MCP servers, skills, memory, settings) while wick handles session storage, command approval, multi-instance config, and concurrency.

::: tip Why this is interesting
Most "AI agent" tools either lock you into their own runtime, or expose a chat-only UI. Wick goes the other way: bring your own Claude / Codex / Gemini install, and wick gives it a place to run safely with **multi-channel routing (Slack + Telegram + UI)**, multi-session concurrency, real projects on disk, and a per-command [approval gate](./command-gate). One agent — many ways to talk to it.
:::

## What you get

| Capability | Where |
|---|---|
| **Multi-channel routing** — same agent reachable from Slack, Telegram, and the web UI; each thread / chat / conversation = its own session | [Channels](./agents/channels) |
| **Multi-session concurrency** — pool caps subprocess count, FIFO-queues the rest, idle-kills + resumes via `--resume <cli_session_id>` | [Pool & Sessions](./agents/pool) |
| **Workflows** — YAML DAG of classify / agent / connector / http / channel / datatable / branch / parallel nodes triggered by cron, channel events, webhooks, or manual runs; replayable per-run state on disk; visual canvas + MCP `workflow_*` ops | [Workflows](/workflow/) |
| **Projects** — folders on disk (managed or any custom path) used as the agent's `cwd`. Multiple sessions can share one | [Projects](./agents/projects) |
| **Multi-instance providers** — two `claude/...` profiles with different PATs, plus codex / gemini side-by-side | [Providers](./agents/providers) |
| **Skills Manager** — browse, preview, sync, and delete skill `.md` files across all provider skill dirs (`~/.claude/skills`, `~/.codex/skills`, `~/.gemini/skills`) from one UI | [Skills Manager](./agents/skills-manager) |
| **Command Gate** — `<app>-gate` sidecar binary intercepts every Bash command for whitelist + 4-mode interactive approval | [Command Gate](./command-gate) |
| **AskUser MCP tool** — agent asks a question mid-turn, web UI renders a card, answer goes back as MCP tool result | (covered in [Channels ▶ Web UI](./agents/channels#web-ui)) |
| **Context file panel** — slide-over on session detail to browse / read / edit / download / delete files in the agent's working directory, with markdown + HTML preview and Ace syntax highlighting | _[below](#context-file-panel)_ |
| **Source Control panel** — docked, pinnable SCM sidebar on the session detail page: multi-repo git status, tree/list view, stage/unstage/discard, commit, branches, push/pull, Monaco diff viewer, commit history, live SSE updates | [Source Control](./agents/source-control) |
| **Persistent state on disk** — everything under `~/.<app>/agents/`. Backup is `tar`. Restart re-scans, no DB migration. | _below_ |

## Quick tour

After boot, head to `/tools/agents`.

> **📸 Screenshot needed:** `agents-overview.png` — capture `/tools/agents` (Overview tab), showing pool stats + Active/Queue snapshot + recent sessions list. Save to `docs/public/screenshots/agents-overview.png`.

| Page | What you do |
|---|---|
| **Overview** | Pool stats (active / max / queue), running list, recent sessions. |
| **Sessions** | List, open, delete sessions. Detail tabs: Conversation (assistant bubbles render Mermaid diagrams and SVG graphics progressively during streaming; AI bubbles show an always-visible `HH:mm` stamp, user bubbles show it on hover; static date separators between day groups with a floating day pill that appears while scrolling; double-click any diagram to open a fullscreen zoom/pan lightbox — scroll/trackpad to pan, Ctrl/Cmd+scroll or pinch to zoom, switchable backdrop; syntax-highlighted code, KaTeX math, and [artifact galleries](#artifacts)), Commands (gate audit), Approvals, Raw (collapsible JSON tree of all turns + per-turn tool/thinking traces fetched on demand). Composer at the bottom posts a new message. |
| **Projects** | Create / delete projects. New = empty managed folder unless pointed at a custom path. |
| **Presets** | Edit reusable agent instructions. Each preset is one `agent.md` file. The built-in `default` preset is the fallback when a session has no project (or the project has no `DefaultPreset`); it cannot be deleted, only edited. |
| **Providers** | Per-instance status cards: binary path, version, env vars, extra args, "Rescan" button. Add custom instances when you need two PATs for the same CLI. |
| **Skills** | Browse, preview, sync, and delete skill files across all provider skill dirs. |
| **Channels** | Slack + Telegram bot config (tokens, access control, default project). Web UI is always-on. |

Sessions auto-create on the first message in a Slack thread, a Telegram chat, or a fresh web conversation. You don't pre-allocate them.

## Storage layout

Everything lives under `~/.<app>/agents/`. The `<app>` part is whatever the binary's name resolves to ([appname.Resolve()](https://github.com/yogasw/wick/blob/master/internal/appname): strip `.exe`, strip `-gate` suffix, fall back to ldflag → `wick.yml` name → `"wick"`).

```
~/.<app>/agents/                          ← see internal/agents/config/layout.go
│
├── presets/
│   └── default/agent.md
│
├── projects/                             ← managed project metadata (custom paths live elsewhere)
│   └── <id>/
│       ├── meta.json                     ← project.Meta — custom_path, default_preset, ...
│       └── files/                        ← managed cwd (skipped when CustomPath is set)
│
├── sessions/
│   ├── T123ABC/                          ← Slack thread_ts
│   │   ├── meta.json                     ← session.Meta — project_id ref, status, pending_input
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
| `SystemPrompt` | _(embedded baseline)_ | Global interaction rules appended to every preset's `agent.md` on spawn. Adds to the preset — never replaces it. Edit and reset the default from `/tools/agents/settings`; the shipped baseline is [`internal/agents/system-prompt/default.md`](https://github.com/yogasw/wick/blob/master/internal/agents/system-prompt/default.md). |

## Chat rendering

Assistant bubbles in the **web Conversation tab** render as GitHub-flavored markdown plus a few rich formats — what the agent writes is rendered the same way [claude.ai](https://claude.ai) does. Every format degrades gracefully: on channels with no rich renderer (Slack, Telegram) the raw source still reads fine.

| Format | Author it as | Renders as |
|---|---|---|
| **Markdown** | normal GFM — headings, lists, **bold**, `inline code`, tables, blockquotes, `~~strikethrough~~` | styled rich text |
| **Links** | `[short label](https://…)` | clickable label (the noisy query string is hidden) |
| **Code (highlighted)** | fenced block with a language tag: ` ```js `, ` ```python `, ` ```go `, ` ```sql `, … | syntax-highlighted block via [highlight.js](https://highlightjs.org/), light/dark aware |
| **Mermaid diagrams** | a ` ```mermaid ` fence — `flowchart`, `sequenceDiagram`, `classDiagram`, `stateDiagram-v2`, `erDiagram`, `gantt`, `pie`, `journey`, … | colored [Mermaid](https://mermaid.js.org/) diagram, theme-aware light/dark |
| **Inline math** | `$…$` — e.g. `$E = mc^2$` | [KaTeX](https://katex.org/) inline (a bare `$5 and $10` stays currency, not math) |
| **Display math** | `$$…$$` on its own line(s) | KaTeX centered block |

::: tip Same list the agent sees
This table is mirrored into the agent's immutable system prompt (the `## Renderable formats in chat` section, sourced from [`internal/agents/system-prompt/render_formats.md`](https://github.com/yogasw/wick/blob/master/internal/agents/system-prompt/render_formats.md)) so the model knows it can reach for a diagram or a highlighted snippet. To add a newly supported render type, edit that one file — it lands in the prompt and stays documented here in lockstep.
:::

The renderers (mermaid / highlight.js / KaTeX) are **lazy-loaded** on first use as separate chunks, and Mermaid runs with `securityLevel: strict` since the content is LLM-authored.

## Artifacts

When an assistant turn writes or edits files in the session `cwd/`, those files are automatically surfaced as **artifacts** — inline previews attached to that bubble, directly below the message text. No extra steps: the detection is read-time from the turn's trace index (Write / Edit tool calls), so it works retroactively for sessions that already exist.

Files produced in a single turn are shown as a **grid** (up to 4 items) or a **carousel** (more than 4), each rendered according to its type:

| File type | How it renders |
|---|---|
| Image — png, jpg, jpeg, svg, webp, gif | Thumbnail in the grid. Click opens a **zoomable / pannable lightbox** (zoom buttons, mouse-wheel, drag to pan, `Esc` / `+` / `−` / `0` keyboard shortcuts). |
| PDF | Thumbnail chip. Click opens the PDF inline in the lightbox. |
| HTML | Live sandboxed `<iframe>` preview chip. Click opens a full-screen sandboxed preview. |
| Any other type | Downloadable file chip (icon + filename). |

Detection rules: Write and Edit calls on any file type qualify as artifacts. Read calls qualify only for non-text files (e.g. a PNG the agent generated and then read back). Text files the agent only read — not wrote — are not shown as artifacts.

### Serving artifacts

A new backend endpoint serves cwd files on demand:

```
GET /tools/agents/sessions/{id}/files/raw?path=<relative-path>
```

- Images, PDFs: served `inline` with the detected MIME type and `X-Content-Type-Options: nosniff`.
- SVG: served inline with an additional `Content-Security-Policy: sandbox` header to prevent script execution.
- HTML and other types: forced to `attachment` (download) for safety — the lightbox HTML preview uses `srcdoc` not `src` so the raw file is never trusted directly.

The endpoint is gated by the same session-ownership check as the rest of the agents API and respects the `safeJoin` path sandbox (no `..` traversal).

## Context file panel

Every session-detail page has a vertically-labeled **Context** tab pinned to the right edge of the chat (shortcut `Ctrl+B`). Click or hit the shortcut and a slide-over panel reveals a file tree rooted at the session's resolved `cwd` — the same directory the agent process spawns in (a managed project's `files/`, the project's `CustomPath`, or the per-session fallback `<SessionDir>/cwd/`).

The panel is the single place to inspect what the agent has actually written, without leaving the chat.

| Action | How |
|---|---|
| **Browse** | Folder tree, click a directory to expand. Filter box does substring match on the full relative path. |
| **Read** | Click a file → opens a modal with preview tab. Markdown gets sanitized HTML (via `marked` + `DOMPurify`), HTML renders in a sandboxed iframe (`sandbox="allow-scripts"`, no same-origin), images / PDFs render inline, plain text shows in a `<pre>`. Binary files and files over 2 MiB show a "download instead" affordance. |
| **Edit** | Switch to the Edit tab — Ace editor mounts with syntax mode picked from the file extension (JS, Go, MD, HTML, CSS, JSON, YAML, Python, etc). Save writes back to disk. |
| **Download** | Per-row download icon or modal header button. Filename in `Content-Disposition` is stripped of CR/LF/quote/backslash to defend against header injection. |
| **Create** | `+ file` and `+ folder` icons in the panel header, plus a "new file here" icon on hover of each folder row. |
| **Delete** | Per-row trash icon. Folders recurse. The session `cwd` itself is refused. |

Heavy build artifact directories (`node_modules`, `.git`, `.venv`, `__pycache__`, `dist`, `build`, `target`, `.cache`, `.next`) are pruned from the walk and the listing caps at 5000 entries so the panel stays responsive on large projects.

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

## Lifecycle notifications

Agents are async by nature — once you've sent the first message, the agent may take seconds or minutes to come back. Wick can fire a **browser push notification** when a session transitions to **idle** (the "your turn is back" moment) so you don't have to babysit the tab.

### Opting in

The push surface is opt-in twice — once per browser (must allow notifications) and once per session (the user has to explicitly subscribe to a session before its idle transitions push to them).

| Surface | What it does |
|---|---|
| **Bell on the new-session composer** | Pre-toggle before submitting. The form carries a hidden `subscribe=1` flag and the server calls `Manager.SubscribeUser` right after `CreateSession`. Refresh clears the toggle — nothing persists pre-creation. |
| **Bell on the session composer** | Live per-session toggle. Click POSTs `/sessions/{id}/subscribe` or `/unsubscribe`. Green dot = subscribed. |
| **Bell on overview queue rows** | Hover-reveal bell per queued session. Same subscribe POST, scoped to whichever queued session you point at. |
| **Master switch at `/profile`** | Per-browser device list (one row per Chrome / Firefox / mobile Safari), Send test, Copy PN ID. The bells elsewhere assume this is set up; the first click on any bell triggers `Notification.requestPermission()` if it isn't. |

A reflex-Block on the browser's permission dialog is permanent for the origin. The bell flips to a slash icon — the only way to recover is unblocking in the browser's site settings.

### Which transitions push

Only `idle` (turn finished). `working` was deliberately dropped — it fires at the start of every turn, so the user would get pinged immediately after sending a message. `spawning` and `killed` don't trigger pushes either.

### What the notification carries

- **Title** — `Agent is idle — your turn`.
- **Body** — first 140 runes of the most recent assistant turn (newlines collapsed). Falls back to the session label when the assistant turn hasn't been flushed yet.
- **URL** — `/tools/agents/sessions/<id>`. Click navigates to the session.

### In-app vs OS surface

The service worker checks for same-origin clients on each push:

- **Wick is open in at least one tab** → `postMessage` to every client. The page renders an in-app card (icon + title + body preview + click-to-open hint) and plays a short two-tone chime. The OS notification is suppressed (the `userVisibleOnly: true` subscription requires `showNotification` to be called, so the service worker calls it silently and immediately closes it — the OS surface never reaches the user).
- **No wick tab is open anywhere** → OS notification with sound + banner. Click opens wick to the session URL (focuses an existing tab if one exists, otherwise opens a new tab).

### Who receives a push

Pushes are scoped to the session's `Subscribers` list (a list of user IDs stored in `meta.json`). Wick agent sessions are shared (any logged-in user can open them) but the push targets only the users who explicitly subscribed.

### Sending notifications outside the lifecycle path

The platform also exposes a [`notifications` connector](/connectors/notifications) for sending ad-hoc browser pushes to a specific subscribed user by their opaque PN ID. Useful when a workflow node or an LLM should send a notification on demand rather than waiting for an agent lifecycle event.

## Diagnostics

```bash
wick doctor                # checks the wick binary's own gate setup
wick doctor wick-lab.exe   # inspect a different branded build's gate setup
```

Each check reports `✓` / `✗` / `!`. Exit `0` when required checks pass, `1` otherwise. The gate-specific section is detailed in the [Command Gate guide](./command-gate#diagnostics).

## Sub-pages

- [**Projects**](./agents/projects) — folders on disk; managed vs custom path; built-in `default`.
- [**Providers**](./agents/providers) — multi-instance config, binary resolution chain, status cache.
- [**Channels**](./agents/channels) — Slack, Telegram, web UI; access control; meta-commands.
- [**Pool & Sessions**](./agents/pool) — slot allocation, idle-kill, resume, message buffer.
- [**Source Control**](./agents/source-control) — git SCM panel on the session detail page.
- [**Command Gate**](./command-gate) — shell-command approval system.

## See also

- [Environment Variables](../reference/env-vars) — `APP_NAME` namespacing for `~/.<app>/`.
- [`wick build`](../reference/build) — `--installer` flag bundles the gate sidecar.
- [MCP for LLMs](./mcp) — wick's MCP surface that agents call back into for connectors.
