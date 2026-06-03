# Wick Agent

> Run Claude / Codex / Gemini as a Slack bot, Telegram bot, or web AI assistant — without writing Go, scaffolding a project, or learning the framework. One self-contained binary.

## Quick install

::: code-group

```bash [Linux / macOS]
curl -fsSL https://yogasw.github.io/wick/install.sh | sh
wick-agent start              # daemon mode — manages its own PID + log
# or:  wick-agent server      # foreground (systemd / supervisor)
```

```powershell [Windows]
iwr -useb https://yogasw.github.io/wick/install.ps1 | iex
wick-agent.exe                # launches system tray
```

```bash [Termux / Android]
pkg install curl
curl -fsSL https://yogasw.github.io/wick/install.sh | sh
wick-agent start                  # reachable from your laptop on the same Wi-Fi
# or:  wick-agent start --localhost   # 127.0.0.1 only — access via `ssh -L 9425:localhost:9425`
```

```bash [Docker]
docker run -d -p 9425:9425 \
  -v wick-data:/root/.wick \
  ghcr.io/yogasw/wick:latest
```

:::

Web UI at `http://localhost:9425`. Initial credentials print to the daemon log / container stdout and land in `~/.wick/INITIAL_CREDENTIALS.txt`. Full one-liners + per-platform tips below.

::: warning Default exposes the TCP port — host allowlist is app-level only
By default the server binds `0.0.0.0:9425`. Wick has a built-in **host allowlist** (`app_url` + `allowed_origins`) that rejects HTTP requests with a non-matching `Host` header, **but that runs after the TCP handshake** — anyone on the same Wi-Fi can still:

- See the port open via `nmap` / port scan
- Complete TCP connections (SYN-flood, slowloris, port-exhaust attacks)
- Hit `/health` and any unauthenticated route before the allowlist fires

To **really** close the port at the kernel level (drops non-loopback SYN packets, no TCP handshake at all):

```bash
wick-agent start --localhost           # shortcut: --host 127.0.0.1
wick-agent start --host 192.168.1.42   # or bind one specific NIC
```

Remote access still works via SSH tunnel: `ssh -L 9425:localhost:9425 user@host`. Required on Termux phones — unrooted Android has no `iptables`/`ufw` to lock the port down. See [`server` reference](/reference/app-cli#app-server) for full precedence rules.
:::

## Run modes

The binary supports three modes — see [App CLI Reference](/reference/app-cli) for every subcommand:

| Mode | Command | Best for |
|---|---|---|
| **System tray** | `./wick` (no args) | Desktop / local machine — right-click menu, icon shows server/worker state, auto-start on login |
| **Daemon** | `./wick start` / `stop` / `status` | Termux, VPS without systemd — wick manages its own PID file + log |
| **Foreground** | `./wick server` (or `./wick all`) | Docker, systemd, pm2 — supervisor handles restart + logs |

## Download manually (no installer)

If you prefer to grab the binary directly, head to the [releases page](https://github.com/yogasw/wick/releases):

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
3. **Create a project** — a folder on disk the agent uses as its working directory. The `default` project is created automatically.
4. **Start a conversation** — send a message in the web UI, Slack thread, or Telegram chat. Wick spawns the agent and routes the conversation.

See [AI Agents](/guide/agents) for the full breakdown, or jump to:

- [Providers](/guide/agents/providers) — binary path, PAT, multi-instance
- [Channels](/guide/agents/channels) — Slack, Telegram, Web UI setup
- [Projects](/guide/agents/projects) — project management
- [Command Gate](/guide/command-gate) — what the gate sidecar does and why it matters
