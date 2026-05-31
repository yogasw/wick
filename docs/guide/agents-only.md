# Using Wick as an AI Agent Host

> **Just want to run AI agents?** You don't need Go, `wick init`, or any framework knowledge.
> Download the binary (or pull the Docker image) and you're done.

Wick ships as a **single self-contained binary**. If your goal is to run Claude / Codex / Gemini as a Slack bot, Telegram bot, or web-based AI assistant — you only need that binary.

The same binary supports three run modes — see [App CLI Reference](/reference/app-cli) for the full subcommand list:

| Mode | Command | Best for |
|---|---|---|
| **System tray** | `./wick` (no args) | Desktop / local machine — right-click menu, icon shows server/worker state, auto-start on login |
| **Daemon** | `./wick start` / `stop` / `status` | Termux, VPS without systemd — wick manages its own PID file + log |
| **Foreground** | `./wick server` (or `./wick all`) | Docker, systemd, pm2 — supervisor handles restart + logs |

## Download the binary

Head to the [releases page](https://github.com/yogasw/wick/releases) and grab the latest binary for your OS:

| OS | File |
|---|---|
| Linux (amd64) | `wick-linux-amd64` |
| Linux (arm64) | `wick-linux-arm64` |
| macOS (amd64) | `wick-darwin-amd64` |
| macOS (arm64) | `wick-darwin-arm64` |
| Windows | `wick-windows-amd64.exe` |

## Install per platform

Pick the one that matches where you're running. Each page has its own quickstart + platform-specific tips & tricks.

| Platform | When to use | Guide |
|---|---|---|
| **Desktop Tray** | Local laptop, team member's machine — GUI tray icon, MCP one-click, auto-updater | [Desktop Tray](/guide/desktop-tray) |
| **Headless server** | Remote VPS, bare-metal Linux, anywhere without a desktop session | [Headless Server](/guide/headless) |
| **Docker** | Container deploys, k8s, docker-compose stacks | [Docker](/guide/docker) |
| **Termux / Android** | Always-on bot on a spare phone, no VPS | [Termux / Android](/guide/termux-android) |

## What to do next

1. **Add a provider** — go to `/tools/agents` → Providers, point wick at your Claude / Codex / Gemini binary and your PAT.
2. **Connect a channel** — Slack (Socket Mode bot token), Telegram bot token, or just use the always-on Web UI.
3. **Create a workspace** — a folder on disk the agent uses as its working directory. The `default` workspace is created automatically.
4. **Start a conversation** — send a message in the web UI, Slack thread, or Telegram chat. Wick spawns the agent and routes the conversation.

See [AI Agents](/guide/agents) for the full breakdown, or jump to:

- [Providers](/guide/agents/providers) — binary path, PAT, multi-instance
- [Channels](/guide/agents/channels) — Slack, Telegram, Web UI setup
- [Workspaces](/guide/agents/workspaces) — workspace management
- [Command Gate](/guide/command-gate) — what the gate sidecar does and why it matters
