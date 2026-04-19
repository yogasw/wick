# AGENTS.md

Operational rules for coding agents (Claude Code, etc.) working in this repo. Human onboarding lives in [README.md](README.md).

## Running the app (agent flow)

When an agent needs to start the server — e.g. to verify a UI change — use a **one-shot** flow, not watch mode:

1. Make sure the Tailwind CLI is present (see below).
2. Build CSS once:
   - Windows: `./bin/tailwindcss.exe -i web/src/input.css -o web/public/css/app.css --minify`
   - macOS/Linux: `./bin/tailwindcss -i web/src/input.css -o web/public/css/app.css --minify`
3. Run the server: `go run main.go server`
4. **Always kill the server when you're done.** Don't leave background processes hanging.

Do NOT run `make css/watch`, `make dev`, or `make run/live` from an agent session — watchers are for interactive development, they don't terminate, and they waste the user's resources.

### Tailwind CLI presence check

The standalone Tailwind binary is OS-specific and not committed. Before building CSS:

```bash
ls ./bin/tailwindcss* 2>/dev/null
```

If nothing is found, run `make setup` once — it auto-detects OS/arch and downloads the right binary to `./bin/`.

### Killing processes

- Anything **you** started (server, watcher, build job) must be killed before handing control back.
- If you encounter a process **the user** started (e.g. their own dev server already running on :8080), do NOT kill it silently. Ask first: "Looks like something is already running on :8080. OK to kill it, or are you using it?"
- Prefer `taskkill //F //PID <pid>` on Windows (git bash), `kill <pid>` elsewhere.

## Quick commands

| Task | Command |
|---|---|
| Build CSS once | `./bin/tailwindcss.exe -i web/src/input.css -o web/public/css/app.css --minify` |
| Regenerate templ | `templ generate` |
| Compile check | `go build ./...` |
| Run server (foreground) | `go run main.go server` |
| Run tests | `go test ./...` |
| Setup Tailwind CLI | `make setup` (one-time) |

## Before declaring a task done

- `templ generate && go build ./...` passes.
- Any process you started is stopped.
- Changes on the tool grid / Ctrl+K palette look right in both light and dark mode (if UI changed).

## Skills to use

- `tool-module` — any work under `internal/tools/{tool}/` **or** `internal/jobs/{job}/`. Covers both surfaces — they share the same Config reflection, `wick:"..."` tag grammar, and bootstrap contract.
- `design-system` — any UI styling decision (colors, spacing, typography).

## Module surfaces at a glance

| Surface | URL | Who | Purpose |
|---|---|---|---|
| Tool UI | `/tools/{key}` | End user / operator | Tool home page (module's own templ) |
| Job operator | `/jobs/{key}` | End user / operator | Run Now button + last run info + history |
| Job admin | `/manager/jobs/{key}` | Admin | Schedule + config (writable settings) |
| Tool admin | `/manager/tools/{key}` | Admin | Tool config only (no schedule) |

## Register signatures

```go
// Tool with typed Config (runtime-editable knobs)
app.RegisterTool(meta tool.Tool, cfg C, register func(tool.Router))

// Tool with no knobs (external-link cards, pure-compute tools)
app.RegisterToolNoConfig(meta tool.Tool, register func(tool.Router))

// Scheduled job with typed Config
app.RegisterJob(meta job.Meta, cfg C, run func(ctx) (string, error))

// Job with no knobs
app.RegisterJobNoConfig(meta job.Meta, run func(ctx) (string, error))
```

Handlers and Run funcs are top-level stateless funcs. No `NewTool()` / `NewJob()`. No per-module struct. Metadata always lives at the register call site, not inside the module.
