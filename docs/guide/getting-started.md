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
go install github.com/yogasw/wick@v0.1.13
```

Verify:

```bash
wick --version
```

## 3. Init a project

```bash
wick init my-app
```

This scaffolds `my-app/`, runs `go mod tidy`, and downloads Tailwind + templ automatically.

```
my-app/
├── main.go          # register tools and jobs here
├── agent.md         # AI agent instructions (auto-included)
├── wick.yml         # task runner config
├── tools/
│   ├── convert-text/   # example tool
│   └── external/       # external link cards
├── jobs/
│   └── auto-get-data/  # example job
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
| `APP_ADMIN_PASSWORD` | `admin` | Change after first login |

Everything else (app name, URL, SSO, OAuth) is editable from `/admin/configs` after the app starts.

## 5. Start dev server

```bash
cd my-app
wick dev
```

This generates templ, rebuilds CSS, and starts the server at `http://localhost:8080`.

## 6. Let Claude build your tools

Open the project in Claude Code. Every project includes `agent.md` and Claude skills — Claude already knows the conventions.

Just tell Claude what you need:

```
add a tool called "base64" that encodes and decodes text
```

See [AI Quickstart](/guide/ai-quickstart) for more sample prompts.

## Task Runner Reference

All common tasks are defined in `wick.yml` and run via `go run . <task>`:

| Command | What it does |
|---------|-------------|
| `wick setup` | Download Tailwind + templ, run `go mod tidy` |
| `wick dev` | Generate templ + CSS, start server |
| `wick build` | Generate + minify CSS, compile binary |
| `wick test` | Run `go test ./...` with coverage |
| `wick tidy` | `go fmt` + `go mod tidy` |
| `wick generate` | templ + go generate + CSS rebuild |
