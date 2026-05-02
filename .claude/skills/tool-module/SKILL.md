---
name: tool-module
description: Use for ANY work on a tool OR job in the repo — creating new, improving/refactoring existing, fixing bugs, editing view/handler/service/JS, adding features, or touching anything under internal/tools/{tool}/ or internal/jobs/{job}/. Covers both surfaces because they share the same Config reflection, the same `wick:"..."` tag grammar, the same tag/widget catalog, and the same bootstrap contract — splitting would drift them apart. Enforces: templ + Tailwind UI, top-level stateless Register(r tool.Router) or top-level Run(ctx) (no per-module struct, no NewTool/NewJob), Router + Ctx contract (no direct *http.ServeMux in tools), Key-driven mount path (/tools/{Key} for tools, /jobs/{Key} for jobs), typed Config struct passed to app.RegisterTool/RegisterJob and read via c.Cfg / job.FromContext(ctx).Cfg, JS-first computation, per-module //go:embed js pattern, no CDN, r.Static for assets, @ui.Layout + @ui.Navbar usage, dark/light + responsive requirements, logging pattern, and file-splitting rules. Also mandates the "ask before adding configs" question whenever a new tool or job is requested.
allowed-tools: Read, Grep, Glob, Edit, Write, Bash
paths:
  - "internal/**/*.go"
  - "internal/**/*.templ"
  - "internal/**/js/**/*.js"
  - "internal/tools/registry.go"
  - "internal/jobs/registry.go"
  - "internal/pkg/ui/**"
  - "web/**"
  - "README.md"
  - "AGENTS.md"
  - "template/**"
---

# Module Conventions (tools & jobs) — upstream (wick core)

> **Scope:** this skill is for work on **wick itself** — modules shipped under `internal/tools/` and `internal/jobs/` in the wick source tree. If you're building on top of wick in a downstream project (a scaffold generated from `template/`, where modules live under `tools/` and `jobs/` without the `internal/` prefix), use the downstream skill at `template/.claude/skills/tool-module/SKILL.md` — the paths, registration site, and view conventions differ.

Activate this skill whenever the user touches a **tool** (UI module under `internal/tools/`) or a **job** (scheduled worker under `internal/jobs/`) — creating, improving, fixing, refactoring, restyling, or adding features. **The same conventions apply in both directions.** When editing an existing module, first audit it against the rules below and bring it up to spec as part of the change; don't leave it half-compliant.

Tools and jobs share one skill because they use the **same Config reflection** (`entity.StructToConfigs` with `wick:"..."` tags), the **same widget catalog**, and the **same boot-time registration pattern** — splitting the docs would mean two files drifting out of sync. The only hard differences are the HTTP surface (tools render pages, jobs don't) and the run trigger (tools = request, jobs = cron tick or manual). Read the whole skill once; then the section you need (Tools / Jobs) applies the shared rules to that surface.

## Applies to (non-exhaustive triggers)

- "Bikin tool baru X" / "Bikin job baru X"
- "Tambahin fitur Y ke tool X" / "ke job X"
- "Perbaiki bug di tool X" / "di job X"
- "Refactor tool X" / "Refactor job X"
- "Ubah tampilan tool X" / "restyle tool X"
- "Pindahin tool X ke pakai JS"
- Any edit under `internal/tools/{tool}/` or `internal/jobs/{job}/` (handler, service, view.templ, js/, static.go, config.go)
- Changes to `internal/tools/registry.go`, `internal/jobs/registry.go`, `internal/pkg/ui/*`, or anything that affects how tools/jobs render or register

When in doubt: if the work involves a tool page or a scheduled worker, this skill applies.

## Before building: ALWAYS ask about configs

Whenever the user asks for a **new** tool or job, the very first question back is:

> *"Apakah ini perlu bisa dikonfigurasi sama admin tanpa redeploy? Misalnya URL endpoint, API key, feature flag, seed text, atau knob lain yang biasanya beda per environment atau bisa berubah. Kalau iya, fieldnya apa aja, dan untuk tiap field mau widget yang mana dari daftar di bawah?"*

Then list the widget catalog (below) so the user can pick per field. Don't silently add a `Config` struct just because the logic has an endpoint — the user may want it hardcoded, or they may want a completely different set of knobs. Also don't skip this question just because the request looks simple: a "tool that converts text" may still want a seed text admins can edit.

When auditing an existing module as part of a change, similarly: if the user asks you to add a new hardcoded value that looks like it *should* be configurable (URL, API key, timeout, template, endpoint), ask before hardcoding.

### Widget catalog (shared across tools & jobs)

Every field with a `wick:"..."` tag becomes one row in the `configs` table, scoped to that module's `Meta.Key`. The widget is picked from the Go type plus tag flags — explicit flags always win.

| Go type | Default widget | Override with tag flag |
|---|---|---|
| `string` | `text` | `textarea` / `dropdown=a\|b\|c` / `email` / `url` / `color` / `date` / `datetime` / `kvlist=col1\|col2` |
| `bool` | `checkbox` | — |
| `int`/`float` | `number` | — |

Additional flags (any widget):

- `required` — blocks the module until admin fills it in. Tools surface this via `c.Missing()`; jobs via the `ScopedSetupBanner` on `/jobs/{key}` and `/manager/jobs/{key}`.
- `secret` — masked in UI, never rendered back to the admin after first save.
- `locked` — read-only in admin UI (set by seed, not editable post-boot).
- `regen` — admin gets a "Regenerate" button. Only wired for app-level variables today; per-module regenerate is a TODO.
- `key=custom_name` — override the snake_case column name derived from the field name.
- `desc=...` — admin UI help text. Be useful — this is the only hint the admin sees.
- `kvlist=col1|col2|col3` — editable table widget. Value is stored as a JSON array of objects (`[{"col1":"...","col2":"..."}]`). Read with `json.Unmarshal([]byte(c.Cfg("key")), &rows)`. Bare `kvlist` (no `=`) defaults to a single `value` column.

### Tag grammar

Fields are separated by `;`. `key=value` sets a named field; a bare key becomes a boolean flag.

```go
type Config struct {
    // text (default string)
    Title string `wick:"desc=Card title shown in the admin UI."`

    // url + required + desc
    Endpoint string `wick:"url;required;desc=API base URL. Example: https://api.example.com"`

    // dropdown with fixed options
    Mode string `wick:"desc=Conversion mode.;dropdown=uppercase|lowercase|titlecase"`

    // textarea for multi-line
    Template string `wick:"desc=Prompt template.;textarea"`

    // number, required
    MaxRows int `wick:"desc=Max rows returned per query.;required"`

    // secret, required
    APIKey string `wick:"desc=External API key.;secret;required"`

    // checkbox (default bool)
    EnableCache bool `wick:"desc=Cache results across requests."`

    // kvlist — editable table; value stored as JSON array
    // e.g. [{"id":"1","name":"Sales"},{"id":"2","name":"Support"}]
    QuestionGroups string `wick:"kvlist=id|name;desc=Question group definitions."`

    // override the column name
    LegacyKey string `wick:"key=legacy_api_key;secret;desc=Deprecated. Kept for v1 clients."`
}
```

**Rules (same for tools & jobs):**

- **Fields without a `wick` tag are ignored** — internal state stays internal.
- **One wick-tagged field per runtime-editable knob.** The Go value in `cfg` is the first-boot seed; once a row exists in the `configs` table the DB value wins.
- **Pass a different `cfg` per instance** to seed different defaults. Same Register/Run func, different Meta.Key + Config = second card/scheduled instance.
- **Never set `Owner` manually.** Wick assigns `Owner = Meta.Key` at bootstrap. A module can't spoof another module's namespace.
- **Tools read via `c.Cfg("key")`**, typed helpers `c.CfgInt` / `c.CfgBool`. Cross-module reads via `c.CfgOf(owner, key)` — rare, always needs a comment explaining why.
- **Jobs read via `job.FromContext(ctx).Cfg("key")`**, same typed helpers, same `CfgOf` escape hatch.
- **Key derivation:** field name is snake-cased (`InitText` → `init_text`, `APIBaseURL` → `api_base_url`).

### No runtime-editable knobs?

- **Tools:** use `app.RegisterToolNoConfig(meta, mytool.Register)` instead of `RegisterTool`.
- **Jobs:** use `app.RegisterJobNoConfig(meta, myjob.Run)` instead of `RegisterJob`.

Don't pass `struct{}{}` — the No-Config variants exist so the intent is explicit in the call site.

## Golden rules (non-negotiable, both surfaces)

0. **Always register a new module in its registry as part of the same task.** `internal/tools/registry.go` for tools, `internal/jobs/registry.go` for jobs. Never ask "should I register it?" — an unregistered tool is invisible on the home grid; an unregistered job never runs. Registration is part of creating the module.
1. **Tailwind + templ only** for UI (tools). No raw HTML/CSS frameworks, no component libraries, no CDN tags.
2. **JS-first for computation** (tools). If the logic is pure text/number transformation, do it in JS so the user sees results instantly. Only hit a Go handler when the work genuinely needs the server (database, external API, secrets, heavy compute).
3. **No CDN for third-party JS.** If you truly need an external library, download it and commit it under the module's `js/vendor/`.
4. **Mobile-first and responsive** (tools). Use Tailwind's `sm:` / `md:` / `lg:` breakpoints.
5. **Dark + light mode** (tools). Every color MUST have both variants. Use the named tokens — see the `design-system` skill.
6. **Split when complex.** Heavy service logic → `service_a.go`, `service_b.go`. For templ: see the **templ splitting rules** section.
7. **Logging.** On error paths use `log.Ctx(ctx).Error().Msgf("failed to X: %s", err.Error())`. Bind once when you log repeatedly in one function.
8. **Names and descriptions matter.** `Meta.Name` is short and human. `Meta.Description` is one sentence — it's surfaced on the home grid and the Ctrl+K palette (tools) or the jobs list + operator page (jobs).
9. **Stateless top-level funcs.** No `NewTool(...)` / `NewJob(...)` constructors, no per-module `Handler` struct, no `Meta()` method on the module. Metadata is declared by the caller of `app.RegisterTool` / `app.RegisterJob`; the module only carries the logic and the `Config` schema.

## Tools

### Module layout

```
internal/tools/mytool/
  handler.go      # Top-level Register(r tool.Router) + stateless handler funcs. No Handler struct.
  service.go      # Business logic — pure Go, orchestrates repo calls, no HTTP, no DB driver
  repo.go         # External I/O — DB (gorm), Redis, S3, HTTP APIs. Only file that touches *gorm.DB.
  config.go       # Typed Config struct with `wick:"..."` tags (omit entirely if no runtime-editable knobs).
  view.templ      # main page — split into a view/ subpackage once it grows (see rules below)
  view_templ.go   # generated — DO NOT edit by hand
  static.go       # //go:embed js  + var StaticFS embed.FS
  js/
    mytool.js     # module JS (required; folder must exist or //go:embed fails)
```

> **Tool shape:** one top-level `Register(r tool.Router)` function plus handler funcs — stateless code, the framework carries meta + cfg per-instance. Register an instance from `main.go` / `registry.go` with `app.RegisterTool(meta, cfg, mytool.Register)` — one call = one card on the home grid; call again with a different `meta.Key` + `Config` to get a second card backed by the same `Register` function (see multi-instance example in `template/main.go`). Tools with no runtime-editable knobs use `app.RegisterToolNoConfig(meta, mytool.Register)`. If a tool needs process-wide state (a DB handle, an HTTP client, a cache), wrap `Register` in a factory closure: `func NewRegister(db *sql.DB) tool.RegisterFunc { return func(r tool.Router) { ... } }`.

### Layering rules

Strict call direction: **handler → service → repo**. Never skip a layer, never reverse it.

- **handler.go** — receive `*tool.Ctx`, read input via `c.Form(...)` / `c.Query(...)` / `c.PathValue(...)` / `c.BindJSON(&v)`, validate, call the service, write response via `c.HTML(...)` / `c.JSON(status, v)` / `c.Redirect(url, code)`. Drop to `c.W` / `c.R` only when no helper fits. No business rules, no DB, no `*http.ServeMux`.
- **service.go** — holds `*repo` (or multiple repos), not `*gorm.DB`. Does the thinking: validation, orchestration, transforming repo results into view models. `NewService(db *gorm.DB)` may accept the db only to construct `newRepo(db)` internally — that's the one allowed spot.
- **repo.go** — the **only** file allowed to `import "gorm.io/gorm"` for queries. One method per query. Lowercase `repo` struct, unexported `newRepo(db)`. Returns entities or primitives, never `*gorm.DB`, never query builders.

**When to skip `repo.go`:** only when the tool is pure computation (convert-text, wa-me, json parser — no DB, no external API). Then service.go exists as pure logic.

**Smell test:** if `service.go` imports `gorm.io/gorm` for anything other than the `*gorm.DB` parameter in `NewService`, that's a leak. Extract it to `repo.go` before shipping.

### Step-by-step: add a new tool

1. **Ask the configs question** (top of this skill). Lock in the fields + widgets before writing code.

2. **Create the folder** `internal/tools/mytool/` and a `js/` subfolder with at least one file (`.gitkeep` won't help — `//go:embed` needs a real file matching the pattern; start `js/mytool.js` immediately).

3. **`static.go`:**

   ```go
   package mytool
   import "embed"
   //go:embed js
   var StaticFS embed.FS
   ```

4. **`config.go`** (skip if no knobs):

   ```go
   package mytool
   type Config struct {
       InitText string `wick:"desc=Seed text dropped into the textarea on first load."`
       InitType string `wick:"desc=Seed conversion type.;dropdown=uppercase|lowercase|titlecase"`
   }
   ```

5. **`service.go`** — pure Go logic. Mirror the same computation in `js/mytool.js` when applicable (JS-first rule).

6. **`view.templ`** — body only. The renderer wraps every tool page in `@ui.Layout` + `@ui.Navbar` + setup banner + shared `ToolHeader` (icon, name, description, admin Settings link) at render time. Do **not** write your own `<html>`, nav, or `<h1>` title — they come from `Meta.Name` / `Meta.Description`.

   ```go
   package mytool

   templ IndexBody(basePath string /*, other view state */) {
       <main class="mx-auto w-full max-w-container px-6 pb-8">
           <!-- your form / content here -->
       </main>
       <script src={ basePath + "/static/js/mytool.js" }></script>
   }
   ```

7. **`handler.go`** — one top-level `Register(r tool.Router)` plus handler funcs. The Router exposes `GET`/`POST`/`PUT`/`DELETE`/`PATCH` plus `Static(prefix, fsys)` and `Meta()`. Paths are **relative** to `/tools/{Meta.Key}`. Inside handlers use `c.Base()` for the absolute base URL, `c.Meta()` for display metadata, `c.Cfg(...)` for runtime config.

   ```go
   package mytool
   import "github.com/yogasw/wick/pkg/tool"
   func Register(r tool.Router) {
       r.GET("/", index)
       r.Static("/static/", StaticFS)
   }
   func index(c *tool.Ctx) {
       c.HTML(IndexBody(c.Base() /*, view state */))
   }
   ```

   **Never import `net/http` or `*http.ServeMux` in a tool.** **Never hardcode `/tools/...`.** Use `r.Static(...)` for assets.

8. **`js/mytool.js`** — wrap in an IIFE, wait for `DOMContentLoaded`. Compute on `input` / `change` events.

9. **Register** in `internal/tools/registry.go` (core wick lab) or in the downstream `main.go`:

   ```go
   app.RegisterTool(toolMeta, mytool.Config{...seed...}, mytool.Register)
   // or for no-config tools:
   app.RegisterToolNoConfig(toolMeta, mytool.Register)
   ```

10. **Regenerate and build:** `templ generate && go build ./...`

## Jobs

### Module layout

```
internal/jobs/myjob/
  handler.go      # Top-level Run(ctx) func. No NewJob(), no Meta() method, no Handler struct.
  service.go      # Pure Go logic called from Run. Optional if Run is tiny.
  repo.go         # External I/O (DB, HTTP APIs). Only for jobs with real data crossings.
  config.go       # Typed Config struct with `wick:"..."` tags. Omit entirely if no knobs.
```

No `view.templ`, no `static.go`, no `js/` — jobs have no HTTP surface of their own. The operator page `/jobs/{key}` (Run Now + run history) and the admin settings page `/manager/jobs/{key}` (schedule + configs) are both rendered by wick itself, not by the module.

> **Job shape:** one top-level `Run(ctx context.Context) (string, error)` func — that's the whole contract. Metadata lives on `job.Meta` declared by the caller of `app.RegisterJob`. Register an instance with `app.RegisterJob(meta, cfg, myjob.Run)` — one call = one row in the jobs table; call again with a different `meta.Key` + `Config` to get a second scheduled instance backed by the same `Run` func. Jobs with no runtime-editable knobs use `app.RegisterJobNoConfig(meta, myjob.Run)`. If a job needs process-wide state (a DB handle, an HTTP client), wrap `Run` in a factory closure: `func NewRun(db *sql.DB) job.RunFunc { return func(ctx context.Context) (string, error) { ... } }`.

### Run contract

- Return `(markdown, nil)` on success — the string is shown verbatim in the run history. Empty string means "no output to display".
- Return `("", err)` on failure — the scheduler stores `err.Error()` as the run's result and marks status=error.
- `ctx` carries a `*job.Ctx` (via `job.FromContext(ctx)`) scoped to this instance. Read config with `.Cfg("key")`, `.CfgInt("key")`, `.CfgBool("key")`. Log with `log.Ctx(ctx)` so the run ID is captured.
- Respect `ctx.Done()` — the scheduler may cancel long-running jobs on shutdown.

### Step-by-step: add a new job

1. **Ask the configs question** (top of this skill).
2. **Create the folder** `internal/jobs/myjob/`.
3. **`config.go`** (skip if no knobs): same `wick:"..."` tag grammar as tools.
4. **`handler.go`** — top-level `Run` func:

   ```go
   package myjob
   import (
       "context"
       "errors"
       "github.com/yogasw/wick/pkg/job"
   )
   func Run(ctx context.Context) (string, error) {
       url := job.FromContext(ctx).Cfg("url")
       if url == "" {
           return "", errors.New("url not configured — set it from /manager/jobs")
       }
       // ... do work, return markdown
   }
   ```

5. **`service.go` / `repo.go`** — split when there's real logic or external I/O. Same layering rules as tools: handler (here: Run) → service → repo.
6. **Register** in `internal/jobs/registry.go` (core wick lab) or in the downstream `main.go`:

   ```go
   app.RegisterJob(
       job.Meta{Key: "myjob", Name: "My Job", Icon: "🔄", DefaultCron: "*/30 * * * *", DefaultTags: []tool.DefaultTag{tags.Job}},
       myjob.Config{Endpoint: "https://api.example.com"},
       myjob.Run,
   )
   ```
7. **Build:** `go build ./...` — jobs have no templ surface.

### Surfaces (you get both for free)

- **`/jobs/{key}`** — operator page: Run Now button + last-run info + recent history. Rendered by wick. Any user with access to the job card (tag-based + visibility) can reach it.
- **`/manager/jobs/{key}`** — admin settings: cron schedule, enabled flag, max runs, runtime configs. Admin-only.

Home grid card for a job links to `/jobs/{key}` (operator). Non-admins never see `/manager/*`.

## Templ splitting — when and how (tools only)

Start with a single `view.templ` in the module root. **Split into a `view/` subpackage** when either trigger fires:

- **File is approaching ~300 lines.**
- **The file defines ≥3 templs covering distinct screens/pages.** Row fragments and small helpers used by the same parent screen don't count.

When auditing an existing tool as part of any edit, re-check these triggers.

**Don't split prematurely.** A 150-line file with one page and two card fragments is fine. Per-screen subfolders are almost always overkill.

### Target structure

```
internal/tools/mytool/
  handler.go
  service.go
  static.go
  js/
    mytool.js
  view/              # package view
    layout.templ     # shared chrome, tabs, partials reused across screens
    {screen}.templ   # one file per screen
    {screen}_templ.go
    models.go        # view models
    helpers.go       # pure-Go helpers used by templs
```

The subpackage is named `view`. The handler calls `view.UsersPage(...)`, `view.UserRow{...}`, etc.

### Splitting playbook

1. `mkdir internal/tools/mytool/view/` — flat, no js/css subfolder.
2. Move the shared layout templ + tab/partial helpers into `view/layout.templ`.
3. Move each screen + its row/card fragments into `view/{screen}.templ`.
4. Move view models into `view/models.go`. **Flatten any cross-package types** so `view` doesn't import back into the parent package.
5. Move pure-Go helpers called from templs into `view/helpers.go`.
6. Update handler imports: `view.UsersPage(...)`, etc.
7. Delete the old `view.templ`, `view_templ.go`, and `types.go` from the module root.
8. `templ generate && go build ./...` — must pass.

Reference implementation: [internal/admin/view/](../../../internal/admin/view/).

## Default tags — group vs filter (both surfaces)

Every new tool/job ships with at least one `IsGroup` tag in `DefaultTags` so it has a home on the home page. Group tags are cheap and easy to create. **Filter tags are a different story** — they restrict access, so they only come from explicit user request.

### Group tags (auto)

1. **Look at the tag catalog first.** Core repo: `internal/tags/defaults.go`. Template repo: `template/tags/defaults.go`. Reuse an existing `IsGroup` entry if it fits. No threshold — one-module groups are fine.
2. **If nothing fits, propose a new group.** Broad enough to catch siblings later ("Text", "Messaging", "JSON"). Tell the user before adding it.
3. Reference as `tags.Foo` — never inline `tool.DefaultTag{Name: "..."}` literals in a module's Meta.

### Filter tags (only on request)

`IsFilter` tags gate access. **Never add an `IsFilter` entry on your own initiative.** Do it only when the user explicitly asks ("only the QA team should access this") AND you confirm visibility (they probably want `DefaultVisibility: entity.VisibilityPrivate` too).

If a request is ambiguous ("hide this from outsiders"), ask: *"Mau pakai filter tag, atau cukup di-set Private aja?"* The admin can manage filter tags from `/admin/tags` and `/admin/tools` without any code change.

## Per-module JS pattern — the footgun (tools)

`//go:embed js` fails at compile time if `internal/tools/mytool/js/` doesn't exist or is empty. Always create the folder AND at least one file before the first build. If a new module breaks with `pattern js: no matching files found`, this is why.

Route convention:
- **Tool modules:** `/tools/{name}/static/` → served by the module's own `Register()`.
- **Shared UI:** `/public/js/*` — lives in `web/public/js/` and is loaded by `ui.Layout` on every page.
- **Home / admin / manager:** `/modules/home/js/*`, `/modules/admin/js/*`, `/modules/manager/js/*` — registered in `internal/pkg/api/server.go`.

Never reference a CDN URL from a templ file.

## UI conventions (tools)

- **Container:** `<main class="mx-auto w-full max-w-container px-6 py-8">`
- **Cards:** `rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-8 shadow-md`
- **Inputs:** always pair with a `<label>`. Focus ring: `focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800`
- **Primary button:** `bg-green-500 hover:bg-green-600 text-white-100 rounded-lg px-6 py-4`
- **Secondary button:** `border border-green-500 text-green-500 hover:bg-green-200 rounded-lg`
- **Spacing:** multiples of 4 — `gap-2`, `gap-4`, `mt-6`
- **Icons:** inline SVG with `h-4 w-4` or `h-5 w-5`, `fill="none" stroke="currentColor" stroke-width="2"`

See the `design-system` skill for the full token list.

## Service / logging patterns (both)

Split big services by responsibility:

```
service.go         # package-level entry, thin
service_parse.go   # input parsing helpers
service_render.go  # output formatting
```

Error logging:

```go
if err := h.svc.Do(ctx, req); err != nil {
    log.Ctx(ctx).Error().Msgf("failed to do thing: %s", err.Error())
    // tool: c.Error(http.StatusInternalServerError, "something broke"); return
    // job: return "", err
}
```

When you log repeatedly in one function, bind once:

```go
l := log.Ctx(ctx).With().Str("module", "myjob").Int64("user_id", user.ID).Logger()
l.Info().Msg("started")
l.Error().Err(err).Msg("failed to fetch remote")
```

## Verifying your work

After adding or changing a module, you **must** regenerate before declaring done:

1. **templ** (tools only) — any time you touched a `.templ` file:
   ```bash
   templ generate
   ```
2. **Tailwind CSS** (tools only) — any time you added/changed a Tailwind class:
   ```bash
   ./bin/tailwindcss* -i web/src/input.css -o web/public/css/app.css --minify
   ```
3. **Compile check** (both):
   ```bash
   go build ./...
   ```

Shortcut: `make generate` runs all three in order.

**When in doubt, run both regens** (for tools). Running templ when nothing changed is a no-op; skipping it when something did ships broken UI.

If you need to boot the server to verify, follow the **one-shot flow in [AGENTS.md](../../../AGENTS.md)**: ensure `./bin/tailwindcss*` exists (`make setup` if missing), rebuild CSS once, start `go run main.go server` in the background, **kill it when done**. Never start `make css/watch`, `make dev`, or `make run/live` from an agent session.

Manual confirmation:

- **Tools:** card on the home grid, entry in the Ctrl+K palette, renders in light + dark + narrow viewport, JS-first computation runs without form submit/refresh, `/tools/{Key}/static/js/{name}.js` serves the file but `/tools/{Key}/static/` returns 404.
- **Jobs:** card on the home grid links to `/jobs/{Key}`, Run Now works, schedule on `/manager/jobs/{Key}` persists, ScopedSetupBanner shows when Required configs are empty.

## When to ask before acting

- New external API or credential — confirm endpoint and secret storage approach.
- **Removing an existing tool/job** — confirm, since visibility overrides and scheduled runs may exist in DB.
- Changing `ui.Tool` shape, `ui.Layout`, `ui.Navbar`, `ui.Palette`, or the Router/Ctx contracts — cross-cutting; pause and confirm scope.
- Adding a new **default tag** that doesn't exist yet — propose the name first.
- Adding an **`IsFilter`** tag — never on your own initiative.
