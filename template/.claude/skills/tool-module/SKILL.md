---
name: tool-module
description: Use for ANY work on a tool OR job in this wick-based project — creating new, improving/refactoring existing, fixing bugs, editing view/handler/service/JS, adding features, or touching anything under tools/{tool}/ or jobs/{job}/. Enforces the wick module contract (top-level stateless Register(r tool.Router) for tools or Run(ctx) for jobs, Key-driven mount path, typed Config with wick:"..." tags, JS-first compute, per-module //go:embed js, no CDN, dark/light + responsive). MANDATORY clarify+plan loop before writing any new tool/job code — first reply is always questions + a written plan, never files. Only after the user confirms does code get written. Also mandates reading tools/convert-text/ end-to-end before any new tool and asking the "do you need runtime-editable configs?" question with the widget catalog.
allowed-tools: Read, Grep, Glob, Edit, Write, Bash
paths:
  - "tools/**"
  - "jobs/**"
  - "tags/**"
  - "web/**"
  - "main.go"
  - "AGENTS.md"
  - "README.md"
---

# Module Conventions (tools & jobs) — downstream

## Before Starting

Read `SKILL.md` from the `config-tags` folder, which is a sibling folder of this skill's folder.

Activate whenever the user touches a **tool** (UI module under `tools/`) or a **job** (scheduled worker under `jobs/`) — creating, improving, fixing, refactoring, restyling, or adding features. When editing an existing module, audit it against these rules and bring it up to spec as part of the change.

## Before writing any code — mandatory clarify + plan loop

**Hard rule: never start writing a new tool or job in the same turn the user asks for it.** The first reply is always clarify + plan. Only after the user explicitly confirms ("ok", "lanjut", "go") do you touch any file. One-shot "buatin tool X" → "done, here are 12 files" is a failure mode.

The loop is **three steps**, in order:

### Step 1 — Read the canonical example

Every new tool starts by reading `tools/convert-text/` end-to-end:

- `tools/convert-text/handler.go` — `Register(r tool.Router)` shape, `c.Cfg(...)`, `c.HTML(...)`, `c.Form(...)`, `c.Base()`, `c.Meta()`
- `tools/convert-text/service.go` — pure-Go transform (mirrored in JS)
- `tools/convert-text/config.go` — `wick:"..."` tags on exported fields
- `tools/convert-text/view.templ` — what the view looks like (starts with `<main>`, **no `@ui.Layout` / `@ui.Navbar`** — the framework wraps those)
- `tools/convert-text/static.go` — `//go:embed js` + `StaticFS`
- `tools/convert-text/js/convert.js` — JS-first computation, IIFE, `DOMContentLoaded`
- `main.go` — how the instance is registered (note: same `Register` func used twice with different `Key` + `Config` = two cards)

For jobs, read `jobs/auto-get-data/` the same way.

If the user's request doesn't obviously map onto these patterns, **stop and ask before improvising**. Do not invent a new shape.

### Step 2 — Ask clarifying questions

Ask only the questions the request doesn't already answer. Pick from this checklist — don't dump all of them if the user already told you. **Do ask when in doubt.** Better one extra round of Q&A than silently guessing.

**For a new tool:**

1. **Concrete example** — "Kasih satu contoh: input apa, output apa? Misalnya: input `hello world`, output `HELLO WORLD`."
2. **Scope** — "Ini satu halaman aja atau multi-screen (tabs, wizard)?"
3. **Computation side** — "Transformasi murni (bisa di JS, instant)? Atau butuh DB / external API / secret (harus lewat Go handler)?"
4. **Configs question** — *"Apakah ini perlu bisa dikonfigurasi sama admin tanpa redeploy? Misalnya URL endpoint, API key, feature flag, seed text, atau knob lain yang beda per environment atau bisa berubah. Kalau iya, fieldnya apa aja, dan untuk tiap field mau widget yang mana?"* — then paste the widget catalog below so the user can pick per field.
5. **Tag** — "Masuk group mana di home grid? Pakai tag yang sudah ada (`Text`, `API`, `Job`, …) atau bikin baru?"
6. **Visibility** — "Public (semua user) atau Private (harus di-grant)?"
7. **Edge cases** — "Kalau input kosong / invalid / kepanjangan, mau behavior apa? (Error message? Silent no-op? Truncate?)"
8. **JS-first mirroring** — if Go-side compute is needed, ask whether JS should mirror it for instant preview before submit.

**For a new job:**

1. **Trigger** — "Cron-nya berapa sering? (misal `*/30 * * * *` = tiap 30 menit)"
2. **What it does** — "Per tick: fetch apa, simpan ke mana, update apa?"
3. **Configs question** (same as tools).
4. **Failure mode** — "Kalau endpoint down / response error, mau fail (marks run as error) atau soft-skip (return empty markdown, status=ok)?"
5. **Output summary** — "Run history mau tampilkan apa? (jumlah record? diff summary? cuma 'ok'?)"
6. **Idempotency** — "Boleh di-Run Now kapan aja dari operator page, atau ada side effect yang harus dijagain?"

**For editing / extending an existing module:** always ask

1. **What breaks if we change this** — "Ada user yang udah pakai field/endpoint ini?"
2. **Additive vs. breaking** — "Mau nambah tanpa ubah behavior existing, atau boleh ubah?"
3. **Hardcoded → configurable?** — if a PR adds a URL/key/timeout/template literal, ask whether it should be a `wick:"..."` config instead of hardcoded.

### Step 3 — Present the plan, wait for explicit confirm

After clarifications land, write a short plan back to the user **before** touching files. Include:

- **Scope** — tool vs job, key, name, group tag, visibility
- **Config fields** — exact Go struct with `wick:"..."` tags; widget per field; which ones are `required` / `secret`
- **Files to create** — bullet list with one-sentence purpose each (`handler.go`, `service.go`, `view.templ`, `js/<name>.js`, `config.go` if knobs, `static.go`)
- **JS-first split** — what runs in JS, what runs in Go handler, what's mirrored both sides
- **Registration** — exact `app.RegisterTool(...)` or `app.RegisterJob(...)` call to add in `main.go`
- **Open questions** — if anything still unclear, list it so the user can fill in

Then **stop** and wait for the user to say "ok" / "lanjut" / "go" / equivalent. Silence means wait, not proceed.

**Exception — trivial edits.** If the task is a one-line fix (typo in a label, copy tweak, class name swap) and nothing about the module's shape changes, skip the plan step and just do it. The clarify+plan loop is for anything that creates files, changes a signature, or adds configs. When unsure whether the edit is trivial, default to asking.

When auditing an existing module: if the user asks you to add a hardcoded value that looks like it *should* be configurable (URL, API key, timeout, template, endpoint), ask before hardcoding.

## Widget catalog

Read `SKILL.md` from the `config-tags` folder (sibling of this skill's folder) for the full tag reference.

Quick rules:

- **Fields without a `wick` tag are ignored** — internal state stays internal.
- **One wick-tagged field per runtime-editable knob.** The Go value in `cfg` is the first-boot seed; once a row exists in `configs` the DB value wins.
- **Pass a different `cfg` per instance** to seed different defaults (see `main.go`: `convert-text` vs `convert-text-alt`).
- **Never set `Owner` manually** — wick assigns `Owner = Meta.Key` at bootstrap.
- **Tools read via `c.Cfg("key")`**, typed helpers `c.CfgInt` / `c.CfgBool`.
- **Jobs read via `job.FromContext(ctx).Cfg("key")`**, same typed helpers.
- **Key derivation:** field name is snake-cased (`InitText` → `init_text`, `APIBaseURL` → `api_base_url`).

### No runtime-editable knobs?

- **Tools:** `app.RegisterToolNoConfig(meta, mytool.Register)` — see `main.go` external-link loop
- **Jobs:** `app.RegisterJobNoConfig(meta, myjob.Run)`

Don't pass `struct{}{}` — the No-Config variants make intent explicit.

## Golden rules (non-negotiable)

0. **Register the module in `main.go` as part of the same task.** An unregistered tool never appears on the home grid; an unregistered job never runs.
1. **Tailwind + templ only** for UI (tools). No raw CSS frameworks, no component libraries, no CDN tags.
2. **JS-first for computation** (tools). Pure text/number transforms run in JS so results are instant. Only hit Go when you need the server (DB, external API, secret, heavy compute). See `tools/convert-text/js/convert.js`.
3. **No CDN for third-party JS.** Commit vendor files under `tools/<name>/js/vendor/`.
4. **Mobile-first and responsive** (tools). Tailwind `sm:` / `md:` / `lg:`.
5. **Dark + light mode** (tools). Every color MUST have both variants. Use named tokens — see the `design-system` skill.
6. **Stateless top-level funcs.** No `NewTool(...)` / `NewJob(...)` constructors, no per-module `Handler` struct, no `Meta()` method on the module. Metadata is declared at the `app.RegisterTool` / `app.RegisterJob` call site.
7. **Logging.** `log.Ctx(ctx).Error().Msgf("failed to X: %s", err.Error())`.
8. **Names and descriptions matter.** `Meta.Name` is short and human. `Meta.Description` is one sentence — surfaces on the home grid and Ctrl+K palette.
9. **No `@ui.Layout` / `@ui.Navbar` / page title in your templ.** The framework wraps every tool page in Layout + Navbar + setup banner + a shared ToolHeader (icon, `Meta.Name`, `Meta.Description`, admin-only Settings link). Your templ starts at `<main>` with no `<h1>` — the title comes from `Meta.Name`. Look at `tools/convert-text/view.templ`.
10. **No `*http.ServeMux`, no hardcoded `/tools/...`** — use `r.GET/POST/...` + `r.Static("/static/", StaticFS)`, paths relative to the instance mount.

## Tools

### Module layout

```
tools/mytool/
  handler.go      # Top-level Register(r tool.Router) + stateless handler funcs. No Handler struct.
  service.go      # Business logic — pure Go, orchestrates repo calls, no HTTP, no DB driver
  repo.go         # External I/O — DB (gorm), HTTP APIs. Only file that touches *gorm.DB. Omit for pure-compute tools.
  config.go       # Typed Config struct with `wick:"..."` tags. Omit entirely if no runtime-editable knobs.
  view.templ      # The page. Starts at <main>. Framework wraps Layout + Navbar.
  view_templ.go   # generated — DO NOT edit by hand
  static.go       # //go:embed js + var StaticFS embed.FS
  js/
    mytool.js     # Module JS (required; folder must exist or //go:embed fails)
```

Imports you will use:

```go
import (
    "github.com/yogasw/wick/pkg/tool"   // tool.Router, tool.Ctx, tool.Tool, tool.DefaultTag, tool.Module
    "github.com/yogasw/wick/pkg/job"    // job.Meta, job.FromContext, job.RunFunc
    "github.com/yogasw/wick/pkg/entity" // entity.VisibilityPublic / VisibilityPrivate
)
```

### Layering rules

Strict call direction: **handler → service → repo**. Never skip, never reverse.

- **handler.go** — receive `*tool.Ctx`, read input via `c.Form(...)` / `c.Query(...)` / `c.PathValue(...)` / `c.BindJSON(&v)`, validate, call service, write response via `c.HTML(...)` / `c.JSON(status, v)` / `c.Redirect(url, code)`. Drop to `c.W` / `c.R` only when no helper fits. No business rules, no DB.
- **service.go** — holds `*repo` (or multiple), not `*gorm.DB`. Validation, orchestration, view-model shaping.
- **repo.go** — the **only** file allowed to import `gorm.io/gorm` for queries. One method per query. Returns entities or primitives, never query builders.

**When to skip `repo.go`:** pure-compute tools (convert-text, wa-me, json parser — no DB, no external API). `service.go` is then pure logic.

**Smell test:** if `service.go` imports `gorm.io/gorm` for anything other than the `*gorm.DB` param passed to construct the repo, that's a leak — extract to `repo.go`.

### Step-by-step: add a new tool

1. **Ask the configs question.** Lock in the fields + widgets before writing code.
2. **Read `tools/convert-text/` end-to-end** if you haven't already this session.
3. **Create** `tools/mytool/` and `tools/mytool/js/` (needs at least one real file — `//go:embed` fails on empty dir).
4. **`static.go`:** `//go:embed js` + `var StaticFS embed.FS`.
5. **`config.go`** (skip if no knobs): typed struct with `wick:"..."` tags.
6. **`service.go`** — pure Go. Mirror computation in `js/mytool.js` per JS-first rule.
7. **`view.templ`** — body only. No `<h1>` or description block — the shared ToolHeader above the main draws them from `Meta.Name` / `Meta.Description`. Pass `basePath` in from the handler via `c.Base()`.

   ```go
   package mytool

   templ IndexBody(basePath string /*, other view state */) {
       <main class="mx-auto w-full max-w-container px-6 pb-8">
           <!-- your form / content here -->
       </main>
       <script src={ basePath + "/static/js/mytool.js" }></script>
   }
   ```

8. **`handler.go`** — one top-level `Register(r tool.Router)` + handler funcs. Paths relative to `/tools/{Meta.Key}`.

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
9. **Register** in `main.go`:

   ```go
   app.RegisterTool(
       tool.Tool{
           Key:               "mytool",
           Name:              "My Tool",
           Description:       "One sentence, shown on home grid + Ctrl+K.",
           Icon:              "🔧",
           Category:          "Text",
           DefaultVisibility: entity.VisibilityPublic,
           DefaultTags:       []tool.DefaultTag{tags.Text},
       },
       mytool.Config{/* seeds */},
       mytool.Register,
   )
   // or for no-config tools:
   app.RegisterToolNoConfig(toolMeta, mytool.Register)
   ```
10. **Build:** `wick generate && go build ./...`

### Multi-instance

Same `Register` func, different `Meta.Key` + `Config` = second card. See how `main.go` registers `convert-text` and `convert-text-alt` from one `converttext.Register`.

### External links (tool cards that redirect)

For third-party links you want on the home grid, add an entry to `tools/external/registry.go` — `tool.Module{Meta: ..., Register: external.Register}` — and it gets picked up by the `main.go` loop. No view, no JS, no service needed.

## Jobs

### Module layout

```
jobs/myjob/
  handler.go      # Top-level Run(ctx) func. No NewJob(), no Handler struct.
  service.go      # Pure Go logic called from Run. Optional if Run is tiny.
  repo.go         # External I/O (DB, HTTP). Optional.
  config.go       # Typed Config struct with wick:"..." tags. Omit if no knobs.
```

No `view.templ`, no `static.go`, no `js/` — jobs have no HTTP surface of their own. Wick renders the operator page `/jobs/{key}` and admin page `/manager/jobs/{key}`.

### Run contract

```go
func Run(ctx context.Context) (string, error)
```

- Return `(markdown, nil)` on success — the string is shown verbatim in run history. Empty string = no output to display.
- Return `("", err)` on failure — scheduler stores `err.Error()` as result, marks `status=error`.
- Read config: `job.FromContext(ctx).Cfg("key")`, `.CfgInt("key")`, `.CfgBool("key")`.
- Log with `log.Ctx(ctx)` so the run ID is captured.
- Respect `ctx.Done()` — scheduler may cancel long-running jobs on shutdown.

### Step-by-step: add a new job

1. **Ask the configs question.**
2. **Read `jobs/auto-get-data/` end-to-end.**
3. **Create** `jobs/myjob/`.
4. **`config.go`** (skip if no knobs): same `wick:"..."` grammar as tools.
5. **`handler.go`:**

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

6. **`service.go` / `repo.go`** — split when there's real logic or I/O. Same layering rules as tools.
7. **Register** in `main.go`:

   ```go
   app.RegisterJob(
       job.Meta{
           Key:         "myjob",
           Name:        "My Job",
           Description: "One-sentence description.",
           Icon:        "🔄",
           DefaultCron: "*/30 * * * *",
           DefaultTags: []tool.DefaultTag{tags.Job},
       },
       myjob.Config{/* seeds */},
       myjob.Run,
   )
   ```
8. **Build:** `go build ./...` — jobs have no templ surface.

### Surfaces (you get both for free)

- **`/jobs/{key}`** — operator: Run Now + last-run + history. Rendered by wick.
- **`/manager/jobs/{key}`** — admin: cron, enabled, max runs, runtime configs. Admin-only.

## Default tags — group vs filter

Every new tool/job ships with at least one `IsGroup` tag in `DefaultTags` so it has a home on the home page.

### Group tags (auto)

1. **Check `tags/defaults.go` first.** Reuse `Text`, `API`, `Job`, etc. if one fits.
2. **If nothing fits,** propose a new group. Broad enough to catch siblings later.
3. Reference as `tags.Foo` — never inline `tool.DefaultTag{Name: "..."}` literals in module metadata.

### Filter tags (only on request)

`IsFilter` tags gate access. **Never add one on your own initiative.** Only when the user explicitly asks ("only the QA team should access this") AND you confirm visibility (they probably want `DefaultVisibility: entity.VisibilityPrivate` too).

If the request is ambiguous ("hide this from outsiders"), ask: *"Mau pakai filter tag, atau cukup di-set Private aja?"*

## Per-module JS pattern — the footgun

`//go:embed js` fails at compile time if `tools/mytool/js/` doesn't exist or is empty. Always create the folder AND at least one real file (not `.gitkeep` — must match the embed glob) before the first build. If a new module breaks with `pattern js: no matching files found`, this is why.

Route convention: `/tools/{Key}/static/js/…` — served by the module's own `Register()` via `r.Static("/static/", StaticFS)`.

Never reference a CDN URL from a templ file.

## UI conventions (tools)

See the `design-system` skill for the full token catalog. Most-used:

- **Container:** `<main class="mx-auto w-full max-w-container px-6 py-8">`
- **Cards:** `rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-8 shadow-md`
- **Inputs:** always pair with `<label>`. Focus: `focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800`
- **Primary button:** `bg-green-500 hover:bg-green-600 text-white-100 rounded-lg px-6 py-4`
- **Secondary button:** `border border-green-500 text-green-500 hover:bg-green-200 rounded-lg`
- **Spacing:** multiples of 4 — `gap-2`, `gap-4`, `mt-6`
- **Icons:** inline SVG, `h-4 w-4` or `h-5 w-5`, `fill="none" stroke="currentColor" stroke-width="2"`

## Verifying your work

After adding or changing a module:

1. **templ** (tools only) — ran if you touched a `.templ` file. Part of `wick generate`.
2. **Tailwind CSS** (tools only) — ran if you added/changed classes. Part of `wick generate`.
3. **Compile check** (both):
   ```bash
   go build ./...
   ```

Shortcut: `wick generate` (regenerates templ + tailwind). `wick dev` runs generate + server.

After smoke test: **kill the server on 8080** — this project does not use live reload.

Manual confirmation:
- **Tools:** card on home grid, entry in Ctrl+K palette, renders in light + dark + ≤375px viewport, JS-first computation runs without form submit, `/tools/{Key}/static/js/{name}.js` serves, `/tools/{Key}/static/` returns 404.
- **Jobs:** card on home grid links to `/jobs/{Key}`, Run Now works, schedule on `/manager/jobs/{Key}` persists, setup banner shows when Required configs are empty.

## When to ask before acting

- New external API or credential — confirm endpoint and secret storage approach.
- **Removing an existing tool/job** — confirm (visibility overrides and scheduled runs may exist in DB).
- Unfamiliar pattern not covered by `convert-text` or `auto-get-data` — pause and ask.
- Adding a new **default tag** — propose the name first.
- Adding an **`IsFilter`** tag — never on your own initiative.

## Escape hatch: wick framework API

This skill covers the 95% path. For wick framework APIs not documented here (edge-case `tool.Ctx` methods, advanced `Router` features, `entity.*` helpers), fetch:

```
https://yogasw.github.io/wick/llms.txt
```

Use only when something obviously isn't covered above — don't fetch on every task.
