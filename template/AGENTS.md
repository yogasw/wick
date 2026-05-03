# Agent Guide

This repo is built for AI coding agents. Read this first.

## Layout

```
main.go                 # cobra root: server, worker
config.go               # env loader (caarlos0/env/v9 + godotenv)
server.go               # chi router, Tool interface, static handler
worker.go               # ticker, Job interface
tools/<name>/
  handler.go            # HTTP — parse req, call service, render
  service.go            # business logic — pure Go
  repo.go               # external I/O (DB, S3, HTTP)
  view.templ            # templ UI
tools/external/         # third-party links as tool cards (metadata + redirect)
  external.go           # shared Register — redirects to Meta().ExternalURL
  registry.go           # list of external links (edit to add/remove)
tags/
  defaults.go           # shared DefaultTag catalog — reference as tags.Foo in Meta().DefaultTags
jobs/<name>/
  handler.go            # top-level Run(ctx) — thin shell
  config.go             # typed Config struct with wick:"..." tags (if job has knobs)
  service.go            # orchestration / pure Go
  repo.go               # external I/O (optional)
connectors/<name>/
  connector.go          # Meta + Configs + Operations + ExecuteFunc — wraps one external API for LLMs via MCP
  service.go            # optional split when business logic outgrows connector.go
web/input.css           # tailwind entry
static/                 # tailwind output + assets
```

## Where to add what

| Need                        | File                                   |
|-----------------------------|----------------------------------------|
| New tool (UI)               | `tools/<name>/{handler,service,repo}.go` + `view.templ` |
| New background job          | `jobs/<name>/{handler,config,service}.go` |
| New connector (LLM via MCP) | `connectors/<name>/connector.go` |
| Register tool               | `main.go` — `app.RegisterTool(meta, cfg, mytool.Register)` (or `app.RegisterToolNoConfig(meta, mytool.Register)` for external links) |
| Register job                | `main.go` — `app.RegisterJob(meta, cfg, myjob.Run)` (or `app.RegisterJobNoConfig(meta, myjob.Run)` for no-knob jobs) |
| Register connector          | `main.go` — `app.RegisterConnector(meta, configs, ops)` |
| Add external link           | `tools/external/registry.go` — append a `tool.Module{Meta: ..., Register: external.Register}` entry |
| New shared tag (group/filter) | `tags/defaults.go` — add a `tool.DefaultTag` var, reuse via `tags.Foo` in `Meta().DefaultTags`. Check if one already fits before adding. |
| New env var                 | `config.go` — add field with `env:` tag |
| Change theme                | `tailwind.config.js`                   |

## Naming rules

- Folder: `kebab-case` (`convert-text`, `auto-get-data`)
- Package: lowercase, no hyphen (`converttext`, `autogetdata`)
- Tool shape: one top-level `Register(r tool.Router)` func + stateless handler funcs — no per-module struct, no `NewTool`, no `Meta()` method. Register an instance from `main.go` with `app.RegisterTool(meta, cfg, mytool.Register)`: `meta` = structural/display (Key, Name, Icon, Description, Category, DefaultVisibility, DefaultTags); `cfg` = typed struct with `wick:"..."` tags — the framework reflects it into runtime rows. One call = one card; call again with a different `meta.Key` + `cfg` for a second card backed by the same `Register` func.
- Job shape: one top-level `Run(ctx context.Context) (string, error)` func — no `NewJob`, no `Handler` struct, no `Meta()` method. Register with `app.RegisterJob(meta, cfg, myjob.Run)`. Read runtime config via `job.FromContext(ctx).Cfg("key")`. The returned string is the run-result summary; a non-nil error marks the run failed.
- Connector shape: one top-level `Meta()` returning `connector.Meta`, one typed `Configs` struct shared across operations, per-op typed `Input` structs, and `Operations()` returning `[]connector.Operation` built via `connector.Op(...)` / `connector.OpDestructive(...)`. ExecuteFunc receives `*connector.Ctx` — read configs via `c.Cfg("key")`, inputs via `c.Input("key")`, **always** pass `c.Context()` into `http.NewRequestWithContext` to prevent goroutine leaks. Register with `app.RegisterConnector(meta, configs, ops)` from `main.go`. One call = one connector definition; admins spawn N rows per definition at `/manager/connectors/{key}` (each row carries its own credentials and tags).
- Layering: **handler → service → repo**. Never skip, never reverse.
- Tool path: `Key` drives the mount (`/tools/{Key}`). Don't set `Meta().Path` — wick fills it. Register routes with paths **relative** to the base: `r.GET("/")`, `r.Static("/static/")`. Inside a handler, `c.Base()` returns the absolute base URL; `c.Meta()` returns the full `tool.Tool`.
- Job surfaces: `/jobs/{Key}` is the **operator** page (Run Now + history). `/manager/jobs/{Key}` is the **admin** page (schedule + config). The module doesn't mount these — wick owns both surfaces.
- Runtime-editable config: declare a typed `Config` struct with `wick:"desc=...;required;secret;dropdown=a|b|c"` tags and pass an instance as the `cfg` argument to `app.RegisterTool` / `app.RegisterJob` — the framework reflects the struct into rows via `entity.StructToConfigs` once at register time; no `Configs()` method on the module. Rows land in the `configs` table (composite PK `owner, key` where `owner = meta.Key`). Tools read via `c.Cfg("key")` / `c.CfgInt(...)` / `c.CfgBool(...)`. Jobs read via `job.FromContext(ctx).Cfg("key")`. Tag `required` for must-be-set knobs.

## Session start

Before answering the first question or running any task in this repo, confirm the toolchain once:

```bash
go version          # Go must be installed
wick version       # wick CLI must be on PATH
```

If `wick` is missing, install it and re-check:

```bash
go install github.com/yogasw/wick@v0.2.0
```

Do this at the start of the session — no need to repeat before every command.

## Commands

Use `wick <command>` (not make):

| Command                          | What it does                                               |
|----------------------------------|------------------------------------------------------------|
| `wick setup`                     | Install tailwind + templ to `./bin/`                       |
| `wick dev`                       | Generate templ + css, run server                           |
| `wick build`                     | Generate + minify css + build binary                       |
| `wick generate`                  | Regenerate templ + css only                                |
| `wick test`                      | Run tests                                                  |
| `wick tidy`                      | go mod tidy                                                |
| `wick run <task>`                | Run any task from `wick.yml` (advanced)                    |
| `wick skill list`                | List skills bundled with this wick binary                  |
| `wick skill sync [name...]`      | Replace `./.claude/skills/<name>/` with bundled version; also refreshes the skill table in `AGENTS.md` if its shape still matches the default. No args = sync all. |
| `wick upgrade`                   | Bump `github.com/yogasw/wick` in `go.mod` to latest, run `go mod tidy`, then `wick dev` |
| `wick version`                   | Print wick version                                         |

## Skills

Invoke the skill before reading code when the task matches:

| Task | Skill |
|------|-------|
| Create/edit a tool or job (`tools/`, `jobs/`) | [`tool-module`](./.claude/skills/tool-module/SKILL.md) |
| Create/edit a connector (`connectors/`) | [`connector-module`](./.claude/skills/connector-module/SKILL.md) |
| UI styling, colors, spacing, components | [`design-system`](./.claude/skills/design-system/SKILL.md) |
| Adding/editing `wick:"..."` config fields — widget types, modifiers, key derivation | [`config-tags`](./.claude/skills/config-tags/SKILL.md) |

The `tool-module` skill holds the full module contract, widget catalog, layering rules, and points at the canonical examples (`tools/convert-text/`, `jobs/auto-get-data/`) — read those end-to-end before writing a new tool/job.

The `connector-module` skill covers connectors — LLM-facing modules consumed via MCP (Model Context Protocol). The canonical example is [`connectors/crudcrud/`](./connectors/crudcrud/connector.go). Read it before adding a new connector.

**Before creating a new tool/job/connector:** confirm the request matches the pattern in the canonical examples. If it doesn't, ask before improvising.

For wick framework APIs not covered by the skill: <https://yogasw.github.io/wick/llms.txt>.

## Rules of thumb

- Never edit `*_templ.go` by hand — regenerated from `.templ`.
- Pure compute tools → leave `repo.go` as stub.
- Add new env var? Update `.env.example` too.
- Tailwind classes live in `.templ` files only.
- Connectors are LLM-facing — keep per-op `Description` sharp (LLMs read it), mark destructive ops with `OpDestructive`, and return ramping JSON (typed structs preferred) instead of raw upstream bytes.
- Connector `ExecuteFunc` MUST build HTTP requests with `http.NewRequestWithContext(c.Context(), ...)` — plain `http.NewRequest` leaks the goroutine when MCP cancels.
