# Using Wick as an AI Agent Host

> **Just want to run AI agents?** You don't need Go, `wick init`, or any framework knowledge.
> Download the binary (or pull the Docker image) and you're done.

Wick ships as a **single self-contained binary**. If your goal is to run Claude / Codex / Gemini as a Slack bot, Telegram bot, or web-based AI assistant — you only need that binary.

The same binary supports two run modes:

| Mode | Command | Best for |
|---|---|---|
| **System tray** | `./wick` (no args) | Desktop / local machine — right-click menu, icon shows server/worker state, auto-start on login |
| **Headless server** | `./wick server` | Remote server, Docker, CI — no GUI, logs to stdout |

## Download the binary

Head to the [releases page](https://github.com/yogasw/wick/releases) and grab the latest binary for your OS:

| OS | File |
|---|---|
| Linux (amd64) | `wick-linux-amd64` |
| Linux (arm64) | `wick-linux-arm64` |
| macOS (amd64) | `wick-darwin-amd64` |
| macOS (arm64) | `wick-darwin-arm64` |
| Windows | `wick-windows-amd64.exe` |

### Option A — Desktop (system tray)

Best for running on your local machine or a team member's laptop.

```bash
# macOS / Linux
chmod +x wick
./wick setup   # first-boot: generates credentials + SQLite DB
./wick         # launches system tray — right-click to start server/worker
```

```
# Windows — double-click wick.exe, or from terminal:
wick.exe setup
wick.exe
```

The tray icon shows server/worker state at a glance. Right-click → **Start server** to bring up the web UI at `http://localhost:9425`.

Tray features:
- Start / stop server and worker without a terminal
- MCP install/uninstall for Claude Desktop, Cursor, Gemini CLI, Codex CLI
- Self-updater — checks for new releases on launch, downloads in background, applies on restart
- Auto-start at login (opt-in from Preferences menu)
- Per-day log rotation, "Open logs" in About menu

### Option B — Headless server

Best for remote servers, Docker, or anywhere there's no desktop.

```bash
chmod +x wick
./wick setup    # first-boot init — generates credentials, SQLite DB
./wick server   # start the web UI + agent host, logs to stdout
```

The web UI is at `http://localhost:9425`. Initial credentials are printed to the terminal and saved to `~/.wick/INITIAL_CREDENTIALS.txt`.

## Run with Docker

Docker always runs headless (`wick server` under the hood).

```bash
docker run -d \
  --name wick \
  -p 9425:9425 \
  -v wick-data:/root/.wick \
  ghcr.io/yogasw/wick:latest
```

::: tip Persist your data
Mount `/root/.wick` to a volume or bind mount. That directory holds the SQLite database, sessions, workspaces, and credentials. Without a mount, everything is lost on container restart.
:::

For production, pass env vars to override defaults:

```bash
docker run -d \
  --name wick \
  -p 9425:9425 \
  -v wick-data:/root/.wick \
  -e APP_BASE_URL=https://wick.example.com \
  -e APP_ADMIN_EMAILS=you@example.com \
  ghcr.io/yogasw/wick:latest
```

## Docker Compose

```yaml
services:
  wick:
    image: ghcr.io/yogasw/wick:latest
    ports:
      - "9425:9425"
    volumes:
      - wick-data:/root/.wick
    environment:
      APP_BASE_URL: https://wick.example.com
      APP_ADMIN_EMAILS: you@example.com
    restart: unless-stopped

volumes:
  wick-data:
```

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
