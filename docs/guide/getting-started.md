# Getting Started

## 1. Install Go

Download and install Go from [go.dev/dl](https://go.dev/dl/) (1.21 or later required).

Verify it works:

```bash
go version
# go version go1.21.x ...
```

::: tip Windows users
After installing Go, restart your terminal so `go` is available in `PATH`.
:::

## 2. Install Wick CLI

```bash
go install github.com/yogasw/wick@v0.11.10
```

Verify:

```bash
wick version
```

## 3. Init a project

```bash
wick init my-app
```

This scaffolds `my-app/`, runs `go mod tidy`, and downloads Tailwind + templ automatically.

```
my-app/
├── main.go          # register tools, jobs, and connectors here
├── AGENTS.md        # AI agent instructions (auto-included)
├── wick.yml         # task runner config
├── tools/
│   ├── convert-text/   # example tool
│   └── external/       # external link cards
├── jobs/
│   └── auto-get-data/  # example job
├── connectors/
│   └── crudcrud/       # example connector (LLM-facing via MCP)
└── tags/
    └── defaults.go     # shared tag catalog
```

::: tip Skip auto-setup
Use `--skip-setup` if you want to run setup manually later:
```bash
wick init my-app --skip-setup
cd my-app && go mod tidy && go run . setup
```
:::

## 4. Configure environment (optional)

Wick boots without any configuration — SQLite is used by default, no database setup needed.

To customize, copy the example file:

```bash
cp .env.example .env
```

All variables have working defaults. The only ones you may want to change before first boot:

| Variable | Default | Notes |
|----------|---------|-------|
| `DATABASE_URL` | *(blank = SQLite)* | Set to a Postgres URL to use PostgreSQL |
| `APP_ADMIN_EMAILS` | `admin@example.com` | Your email, gets admin on first login |
| `APP_ADMIN_PASSWORD` | *(auto-generated)* | Leave blank → 5-word passphrase saved to `~/.<app>/INITIAL_CREDENTIALS.txt`; first login forces a password rotation |

Everything else (app name, URL, SSO, OAuth) is editable from `/admin/configs` after the app starts.

## 5. Start dev server

```bash
cd my-app
wick dev
```

This generates templ, rebuilds CSS, and starts the server at `http://localhost:9425`.

## 6. Let Claude build your tools

Open the project in Claude Code. Every project includes `AGENTS.md` and Claude skills — Claude already knows the conventions.

Just tell Claude what you need:

```
add a tool called "base64" that encodes and decodes text
```

```
add a connector for the GitHub REST API with operations:
list_repos, get_repo, list_issues, create_issue (destructive)
```

See [AI Quickstart](/guide/ai-quickstart) for more sample prompts.

::: tip Wire up an LLM client
After your first boot, generate a Personal Access Token at `/profile/tokens` and paste it into Claude Desktop / Cursor / VSCode using the snippets at `/profile/mcp`. Your connectors are immediately callable from the LLM. See [MCP for LLMs](/guide/mcp).
:::

## Common commands

The ones you'll reach for day-to-day:

| Command | What it does |
|---------|-------------|
| `wick dev` | Generate templ + CSS, start server at `http://localhost:9425` |
| `wick server` | Start HTTP server only (`go run . server`) — no asset generation |
| `wick worker` | Start background job worker (`go run . worker`) |
| `wick build` | Compile binary with version metadata baked in via ldflags |
| `wick test` | Run `go test ./...` with coverage |
| `wick skill sync` | Refresh bundled AI skills after upgrading wick |

Full list — built-in CLI commands (`init`, `run`, `server`, `worker`, `skill`, `version`) and task shortcuts from `wick.yml` (`dev`, `setup`, `build`, `test`, `tidy`, `generate`) — see the [CLI reference](/reference/cli).
