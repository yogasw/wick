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
web/input.css           # tailwind entry
static/                 # tailwind output + assets
```

## Where to add what

| Need                        | File                                   |
|-----------------------------|----------------------------------------|
| New tool (UI)               | `tools/<name>/{handler,service,repo}.go` + `view.templ` |
| New background job          | `jobs/<name>/{handler,config,service}.go` |
| Register tool               | `main.go` — `app.RegisterTool(meta, cfg, mytool.Register)` (or `app.RegisterToolNoConfig(meta, mytool.Register)` for external links) |
| Register job                | `main.go` — `app.RegisterJob(meta, cfg, myjob.Run)` (or `app.RegisterJobNoConfig(meta, myjob.Run)` for no-knob jobs) |
| Add external link           | `tools/external/registry.go` — append a `tool.Module{Meta: ..., Register: external.Register}` entry |
| New shared tag (group/filter) | `tags/defaults.go` — add a `tool.DefaultTag` var, reuse via `tags.Foo` in `Meta().DefaultTags`. Check if one already fits before adding. |
| New env var                 | `config.go` — add field with `env:` tag |
| Change theme                | `tailwind.config.js`                   |

## Naming rules

- Folder: `kebab-case` (`convert-text`, `auto-get-data`)
- Package: lowercase, no hyphen (`converttext`, `autogetdata`)
- Tool shape: one top-level `Register(r tool.Router)` func + stateless handler funcs — no per-module struct, no `NewTool`, no `Meta()` method. Register an instance from `main.go` with `app.RegisterTool(meta, cfg, mytool.Register)`: `meta` = structural/display (Key, Name, Icon, Description, Category, DefaultVisibility, DefaultTags); `cfg` = typed struct with `wick:"..."` tags — the framework reflects it into runtime rows. One call = one card; call again with a different `meta.Key` + `cfg` for a second card backed by the same `Register` func.
- Job shape: one top-level `Run(ctx context.Context) (string, error)` func — no `NewJob`, no `Handler` struct, no `Meta()` method. Register with `app.RegisterJob(meta, cfg, myjob.Run)`. Read runtime config via `job.FromContext(ctx).Cfg("key")`. The returned string is the run-result summary; a non-nil error marks the run failed.
- Layering: **handler → service → repo**. Never skip, never reverse.
- Tool path: `Key` drives the mount (`/tools/{Key}`). Don't set `Meta().Path` — wick fills it. Register routes with paths **relative** to the base: `r.GET("/")`, `r.Static("/static/")`. Inside a handler, `c.Base()` returns the absolute base URL; `c.Meta()` returns the full `tool.Tool`.
- Job surfaces: `/jobs/{Key}` is the **operator** page (Run Now + history). `/manager/jobs/{Key}` is the **admin** page (schedule + config). The module doesn't mount these — wick owns both surfaces.
- Runtime-editable config: declare a typed `Config` struct with `wick:"desc=...;required;secret;dropdown=a|b|c"` tags and pass an instance as the `cfg` argument to `app.RegisterTool` / `app.RegisterJob` — the framework reflects the struct into rows via `entity.StructToConfigs` once at register time; no `Configs()` method on the module. Rows land in the `configs` table (composite PK `owner, key` where `owner = meta.Key`). Tools read via `c.Cfg("key")` / `c.CfgInt(...)` / `c.CfgBool(...)`. Jobs read via `job.FromContext(ctx).Cfg("key")`. Tag `required` for must-be-set knobs.

## Makefile

| Target          | What it does                                    |
|-----------------|-------------------------------------------------|
| `make setup`    | Install tailwind.exe + templ.exe to `./bin/`   |
| `make dev`      | Generate templ + css, run server               |
| `make build`    | Generate + minify css + build binary            |
| `make tailwind-init` | Regenerate `tailwind.config.js`           |
| `make clean`    | Remove `bin/` + generated css                   |

## Rules of thumb

- Never edit `*_templ.go` by hand — regenerated from `.templ`.
- Pure compute tools → leave `repo.go` as stub.
- Add new env var? Update `.env.example` too.
- Tailwind classes live in `.templ` files only.
