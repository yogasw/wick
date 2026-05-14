# Wick

> **Build internal tools and AI agents in Go — or just download and run Claude / Codex / Gemini as a Slack + Telegram + web agent host. No copy-pasting. You own the code.**

Two ways to use wick:

---

## 1. Run AI Agents — no Go, no framework

Want Claude / Codex / Gemini as a Slack bot, Telegram bot, or web assistant? Just download the binary.

```bash
# Linux / macOS
curl -L https://github.com/yogasw/wick/releases/latest/download/wick-linux-amd64 -o wick
chmod +x wick
./wick setup    # first-boot: generates credentials + SQLite DB
./wick server   # web UI at http://localhost:9425
```

```bash
# Docker — single-container: HTTP + cron in one process
docker run -d \
  -p 9425:9425 \
  -v wick-data:/root/.wick \
  ghcr.io/yogasw/wick:latest all
```

The binary supports two modes — pick one:

| Mode | Command | Best for |
|---|---|---|
| **System tray** | `./wick` (no args) | Desktop — right-click menu, icon shows state, auto-start on login |
| **Headless** | `./wick server` | Remote server / Docker — no GUI, logs to stdout |

Then in the web UI (`/tools/agents`):

1. **Providers** — point wick at your Claude / Codex / Gemini binary and your PAT
2. **Channels** — connect Slack (Socket Mode), Telegram bot, or just use the built-in Web UI
3. **Workspaces** — pick a folder for the agent to work in (a `default` is created automatically)
4. Send a message → wick spawns the agent and routes the conversation

Every Bash command the agent runs goes through the **Command Gate** — whitelist globs or escalate to interactive 4-mode approval (Approve once / This session / Always / Block), audited to JSONL.

→ [Agent host docs](https://yogasw.github.io/wick/guide/agents-only)

---

## 2. Build Internal Tools & Jobs — AI writes real Go files

```bash
go install github.com/yogasw/wick@latest
wick init my-app
cd my-app
wick dev   # http://localhost:9425
```

Open `my-app/` in Claude Code and prompt what you need:

```
add a tool called "base64" that encodes and decodes text
```

```
add a background job that syncs data from our API every 30 minutes
```

```
add a connector for GitHub with list_repos and create_issue operations
```

Claude writes real Go files in your repo — you own everything. `git diff` to review, `git revert` to undo.

### What wick scaffolds

```
my-app/
├── main.go          # register tools, jobs, and connectors here
├── AGENTS.md        # Claude reads this — framework conventions
├── .claude/skills/  # bundled AI skills (tool-module, job-module, connector-module)
├── wick.yml         # task runner config
├── tools/
│   ├── convert-text/   # example tool (UI page)
│   └── external/       # external link cards
├── jobs/
│   └── auto-get-data/  # example background job
├── connectors/
│   └── crudcrud/       # example connector (LLM-facing via MCP)
└── tags/
    └── defaults.go     # shared tag catalog
```

### What the framework handles

You write business logic. Wick handles everything else:

| You write | Wick provides |
|---|---|
| `func Register(r tool.Router)` | Admin UI, tag-based access, runtime config editing |
| `func Run(ctx) (string, error)` | Cron scheduler, job history, run/retry UI |
| `func Operations() []connector.Op` | MCP endpoint, per-call audit log, OAuth + PAT auth |

### Module types

| Type | Audience | Entry point | URL |
|------|----------|-------------|-----|
| Tool | Humans (web UI) | `Register(r tool.Router)` | `/tools/{key}` |
| Job | Scheduler | `Run(ctx) (string, error)` | `/jobs/{key}` |
| Connector | LLMs via MCP | `Operations()` | `/mcp` |
| Agents | Slack / Telegram / Web | built-in | `/tools/agents` |

### Common commands

| Command | What it does |
|---------|-------------|
| `wick dev` | Generate templ + CSS, start server |
| `wick server` | Start HTTP server only |
| `wick worker` | Start background job worker |
| `wick all` | HTTP + cron in one process (single-node deploys) |
| `wick build` | Compile binary with version metadata |
| `wick test` | Run `go test ./...` with coverage |
| `wick skill sync` | Refresh bundled AI skills after upgrade |

→ [Framework docs](https://yogasw.github.io/wick/guide/getting-started) · [AI Quickstart](https://yogasw.github.io/wick/guide/ai-quickstart)

---

## Contributing

See [CONTRIBUTING.md](docs/contributing.md) or the [Contributing guide](https://yogasw.github.io/wick/contributing).

## License

MIT
