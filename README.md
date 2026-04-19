# Wick

**Just Prompt. AI Does the Rest.**

Stop copy-pasting AI output into black-box editors. Wick gives AI a real Go project — you own everything it builds.

### Overview

A Go-based platform for building internal tools, admin panels, and background jobs. Provides a standardized structure with self-registering tool modules, a configurable admin UI, tag-based access control, and a themeable design system. The app name and branding are configurable at runtime via the admin Configs panel — no code changes needed to rebrand.

This boilerplate is designed for projects of medium to large complexity. **For simpler cases that only handle one or a few processes, it is not necessary to use this structure** — a single `main.go` or flat architecture works fine.

The codebase is organized by module rather than by function. This approach offers several advantages:

- **Single Responsibility Principle**: One of the SOLID principles of object-oriented design, states that a class or module should have only one reason to change.
- **Reusability**: Modules become more reusable across different clients, promoting code sharing and reducing duplication.
- **Loose Coupling, High Cohesion**: Two words that describe how easy or difficult it is to change a piece of software. Grouping by module enforces loose coupling between different parts of the code while promoting high cohesion within each module.
- **Faster Contribution**: Developers can contribute to specific modules without causing **collateral damage** in unrelated areas, speeding up the development process.
- **Ease of Understanding**: The codebase becomes more accessible and understandable as it's organized around modules and use cases. A use case repository clarifies what each module does.

### Create New Module/API

This section guides you through creating a new API module following the established patterns in this codebase.

#### Architecture Overview

This project follows "Clean" Architecture principles with these layers:

- **Handler** (`internal/{module}/handler.go`) - HTTP layer that handles requests/responses
- **Service** (`internal/{module}/service.go`) - Business logic layer
- **Repository** (`internal/{module}/repo.go`) - Data access layer
- **Entity** (`internal/entity/{module}.go`) - Domain models

#### Step-by-Step Guide

**1. Create the Entity**

Create your domain model in `internal/entity/{module}.go`:

```go
package entity

import "time"

type YourModule struct {
    ID        int64     `json:"id"`
    Name      string    `json:"name" gorm:"index"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}
```

**2. Create the Repository**

Create `internal/{module}/repo.go`:

```go
package yourmodule

import (
    "context"
    "github.com/yogasw/wick/internal/entity"
    "gorm.io/gorm"
)

type repo struct {
    db *gorm.DB
}

func NewRepository(db *gorm.DB) *repo {
    return &repo{db: db}
}

func (r *repo) Save(ctx context.Context, item *entity.YourModule) error {
    return r.db.WithContext(ctx).Save(item).Error
}

func (r *repo) FindByID(ctx context.Context, id int64) (*entity.YourModule, error) {
    var item entity.YourModule
    err := r.db.WithContext(ctx).First(&item, id).Error
    if err != nil {
        return nil, err
    }
    return &item, nil
}
```

**3. Create the Service**

Create `internal/{module}/service.go`:

```go
package yourmodule

import (
    "context"
    "errors"
    "fmt"
    "github.com/yogasw/wick/internal/entity"
    "gorm.io/gorm"
)

//go:generate mockery --with-expecter --case snake --name Repository
type Repository interface {
    Save(ctx context.Context, item *entity.YourModule) error
    FindByID(ctx context.Context, id int64) (*entity.YourModule, error)
}

type Service struct {
    repo Repository
}

func NewService(repo Repository) *Service {
    return &Service{repo: repo}
}

func (s *Service) GetByID(ctx context.Context, id int64) (*entity.YourModule, error) {
    item, err := s.repo.FindByID(ctx, id)
    if err != nil {
        if errors.Is(err, gorm.ErrRecordNotFound) {
            return nil, fmt.Errorf("item not found")
        }
        return nil, fmt.Errorf("failed to find item: %w", err)
    }
    return item, nil
}

func (s *Service) Create(ctx context.Context, req *CreateRequest) error {
    item := &entity.YourModule{
        Name: req.Name,
    }

    if err := s.repo.Save(ctx, item); err != nil {
        return fmt.Errorf("failed to save item: %w", err)
    }

    return nil
}

type CreateRequest struct {
    Name string `json:"name" validate:"required"`
}
```

**4. Create the Handler**

Create `internal/{module}/handler.go`:

```go
package yourmodule

import (
    "encoding/json"
    "github.com/yogasw/wick/internal/pkg/api/resp"
    "net/http"
    "strconv"

    "github.com/rs/zerolog/log"
)

type httpHandler struct {
    svc *Service
}

func NewHttpHandler(svc *Service) *httpHandler {
    return &httpHandler{svc: svc}
}

func (h *httpHandler) GetByID(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()

    id, err := strconv.Atoi(r.PathValue("id"))
    if err != nil {
        resp.WriteJSONFromError(w, err)
        return
    }

    item, err := h.svc.GetByID(ctx, int64(id))
    if err != nil {
        log.Ctx(ctx).Error().Msgf("failed to get item: %s", err.Error())
        resp.WriteJSONFromError(w, err)
        return
    }

    resp.WriteJSON(w, http.StatusOK, item)
}

func (h *httpHandler) Create(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()

    var req CreateRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        resp.WriteJSONFromError(w, err)
        return
    }

    if err := h.svc.Create(ctx, &req); err != nil {
        log.Ctx(ctx).Error().Msgf("failed to create item: %s", err.Error())
        resp.WriteJSONFromError(w, err)
        return
    }

    resp.WriteJSON(w, http.StatusCreated, "created")
}
```

**5. Register Routes in Server**

Add your module to `internal/api/server.go` in the `NewServer()` function:

```go
// YourModule
yourModuleRepo := yourmodule.NewRepository(db)
yourModuleSvc := yourmodule.NewService(yourModuleRepo)
yourModuleHandler := yourmodule.NewHttpHandler(yourModuleSvc)

// Add routes
r.Handle("GET /api/v1/yourmodule/{id}", authMidd.StaticToken(http.HandlerFunc(yourModuleHandler.GetByID)))
r.Handle("POST /api/v1/yourmodule", authMidd.StaticToken(http.HandlerFunc(yourModuleHandler.Create)))
```

**6. Add Database Migration (if needed)**

If your entity needs database tables, add migration to `internal/postgres/migrate.go`:

```go
func Migrate(db *gorm.DB) {
    db.AutoMigrate(
        &entity.Room{},
        &entity.YourModule{}, // Add your entity here
    )
}
```

#### Key Patterns to Follow

- **Error Handling**: Use `resp.WriteJSONFromError(w, err)` for consistent error responses
- **Logging**: Use `log.Ctx(ctx).Error().Msgf()` for contextual logging
- **Validation**: Use struct tags with `validate` for request validation
- **Database**: Always use `WithContext(ctx)` for database operations
- **Mocking**: Add `//go:generate mockery` comments for interfaces that need mocks

#### Available Response Utilities

- `resp.WriteJSON(w, statusCode, data)` - Standard JSON response
- `resp.WriteJSONFromError(w, err)` - Error response with proper status codes
- `resp.WriteJSONWithPaginate(w, statusCode, data, total, page, limit)` - Paginated response

### Sample Use Case

Example: building a tool that generates reports from a database, a background job that cleans up expired sessions on a schedule, and an admin page to manage tool access per team via tags.

### Adding a UI Tool Module

Tool modules (`internal/tools/{tool}/`) follow a lighter pattern than API modules — a `handler.go` + `view.templ` + `service.go`, wired up in `internal/tools/registry.go` (or a downstream `main.go`). Each tool package exposes one top-level `Register(r tool.Router)` function plus, if it has runtime-editable config, a `Config` struct. There is no per-module state-bearing struct — handlers are plain functions that read per-instance metadata via `c.Meta()` and config values via `c.Cfg(...)`.

Register an instance from `main.go`:

```go
app.RegisterTool(
    tool.Tool{Key: "convert-text", Name: "Convert Text", Icon: "Aa"},
    converttext.Config{InitText: "hello world", InitType: "uppercase"},
    converttext.Register,
)
```

One `app.RegisterTool` call = one card on the home grid. Register again with a different `meta.Key` (and, if you want, a different `Config`) to get a second card backed by the same `Register` function. Tools with no runtime-editable knobs (e.g. external redirect links) use `app.RegisterToolNoConfig(meta, register)` instead.

**Key-driven paths.** Every `tool.Tool` declares a `Key` (lowercase slug, no slashes). Wick mounts the instance at `/tools/{Key}` and fills in `meta.Path` before invoking `Register` — modules never set `Path` themselves and never hardcode `/tools/...`. Inside `Register`, declare routes **relative** to the base: `r.GET("/")`, `r.POST("/api/foo")`, `r.Static("/static/")`. Handlers then read the active instance from `*tool.Ctx`: `c.Base()` returns the absolute `/tools/{Key}` path, `c.Meta()` returns the full `tool.Tool` (Name, Icon, ExternalURL, …). Use those inside handlers instead of closing over values from `Register`.

Handlers receive a `*tool.Ctx` — a thin wrapper around `http.ResponseWriter` / `*http.Request` with helpers for the common shape (`c.Form(k)`, `c.Query(k)`, `c.BindJSON(&v)`, `c.HTML(body)`, `c.JSON(status, v)`, `c.Redirect(url, code)`). Wick owns the mux, injects the page renderer, and fails the boot with a pointed error if any two tools share a `Key` or if any two routes collide on `METHOD PATH`. Tools never import `net/http` for mounting.

**Per-module static assets (JS/CSS).** Each module embeds its own assets so the codebase doesn't accumulate one huge `app.js`:

1. Create `internal/tools/{tool}/js/` and drop your JS files in it (e.g. `mytool.js`).
2. Create `internal/tools/{tool}/static.go`:

   ```go
   package mytool

   import "embed"

   //go:embed js
   var StaticFS embed.FS
   ```

3. Mount the static tree via the Router inside `Register` — path is relative:

   ```go
   func Register(r tool.Router) {
       r.GET("/", index)
       r.Static("/static/", StaticFS)
   }
   ```

4. Reference the script from your templ page — use the base path read via `c.Base()` and threaded into the template:

   ```html
   <script src={ base + "/static/js/mytool.js" }></script>
   ```

**Footgun:** `//go:embed js` fails to compile if the `js/` directory doesn't exist. Always create the directory (with at least one file) before building.

`r.Static` blocks directory listings automatically — embedded asset trees can't be browsed.

**Runtime-editable config (typed `Config` + `wick` tags).** If a tool needs knobs that admins can edit without a redeploy — seed text, API base URLs, feature toggles, credentials — declare a typed `Config` struct and pass a seed value to `app.RegisterTool`:

```go
type Config struct {
    InitText string `wick:"desc=Seed dropped into the textarea."`
    InitType string `wick:"desc=Seed conversion type.;dropdown=uppercase|lowercase|titlecase"`
    APIKey   string `wick:"desc=External API key.;secret;required"`
}
```

Wick reflects the struct into `configs` rows once at register time via `entity.StructToConfigs` — the tool itself never implements a `Configs()` method. Each exported field with a `wick:"..."` tag becomes one row. Tag fields are split by `;`: `desc=...` describes the row; bare keys are flags (`required`, `secret`, `locked`, `regen`, `textarea`, `checkbox`, `number`, `email`, `url`, `color`, `date`, `datetime`); `dropdown=a|b|c` renders a `<select>` with the given options. When no widget flag is set, the framework picks one from the Go type: `bool` → checkbox, `int/float` → number, `string` → text. Field names are snake-cased into keys (`InitText` → `init_text`) — override with `key=custom_name` if you need a specific column name.

Rows are reconciled at boot with composite primary key `(owner, key)`, where `owner = meta.Key`. The Go value passed to `app.RegisterTool` is the first-boot seed; once a row exists it wins (admin edits stick). Handlers read the current value via `c.Cfg("init_text")` — scoped automatically to this instance's `Meta.Key`. Typed accessors: `c.CfgInt("max_items")`, `c.CfgBool("enable_cache")`. For explicit cross-tool reads use `c.CfgOf(owner, key)` (rare, intentional). Tag a field `required` when there's no sensible default; `c.Missing()` returns the keys still unset so the page can show a "setup required" prompt.

Because the seed is pulled from the cfg argument at register time, a single tool registered as multiple cards each seed their own defaults — `meta = {Key: "default"}` + `cfg = {InitText: "hello world"}` and `meta = {Key: "convert-text-alt"}` + `cfg = {InitText: "HELLO WORLD"}` give two cards with different initial state but shared logic.

### Adding a Background Job Module

Job modules (`internal/jobs/{job}/`) follow the same stateless pattern as tools — a top-level `Run(ctx) (string, error)` function plus, if the job has knobs, a typed `Config` struct. There is no `NewJob()` constructor, no `Handler` struct, no `Meta()` method. Metadata lives at the register call site.

Register an instance from `main.go`:

```go
app.RegisterJob(
    job.Meta{
        Key:         "auto-get-data",
        Name:        "Auto Get Data",
        Description: "Fetch a remote endpoint on a schedule.",
        Icon:        "🌐",
        DefaultCron: "*/30 * * * *",
        DefaultTags: []tool.DefaultTag{tags.Job},
    },
    autogetdata.Config{},
    autogetdata.Run,
)
```

One `app.RegisterJob` call = one row in the `jobs` table = one card on the home grid. Call again with a different `meta.Key` + `Config` to get a second scheduled instance backed by the same `Run` func. Jobs with no runtime knobs use `app.RegisterJobNoConfig(meta, run)`.

**Two surfaces per job.** A registered job gets two pages with clearly split audiences:

- `/jobs/{key}` — **operator surface.** Run Now button, last-run status, recent run history. What end users see.
- `/manager/jobs/{key}` — **admin surface.** Schedule (cron) + runtime Config editor. What admins see. Non-admin users who land here get a 404.

Both pages share `manager.Service` so run history and setup-required banners stay in sync.

**Reading config inside Run.** Run is a plain function — no struct to attach methods to. Read runtime config via the ctx:

```go
func Run(ctx context.Context) (string, error) {
    c := job.FromContext(ctx)
    url := c.Cfg("url")
    if url == "" {
        return "", errors.New("url not configured")
    }
    body, err := fetchRemote(ctx, url)
    if err != nil {
        return "", err
    }
    return body, nil
}
```

The returned string is stored on the run row as the result summary; the error (if any) marks the run as failed. Same `wick:"..."` tag grammar as tools — `required`, `secret`, `dropdown=a|b|c`, `textarea`, `url`, `number`, etc. Wick reflects the `Config` struct into `configs` rows at register time via `entity.StructToConfigs`.

**Worker vs web.** The web process (`go run main.go server`) mounts both surfaces; the worker process (`go run main.go worker`) owns the cron ticker that invokes `Run` on schedule. Both read from the same `configs` table, so admin edits land everywhere on the next tick.

### Environment Variables

Set up the environment file by copying .env.example:

Mac/Linux:
`cp .env.example .env`

Windows:
`copy .env.example .env`

Alternatively, you can create a copy of `.env.example` and rename it to `.env` in your project's root directory

### Run Locally

First time only:

```bash
make setup         # downloads the Tailwind CLI into ./bin/ (OS-detected)
```

**Development (with CSS + templ watching):**

Two terminals, run in parallel:

```bash
# Terminal 1 — Tailwind in watch mode
make css/watch

# Terminal 2 — the server
go run main.go server
```

Or use the combined target that runs both (Unix-like shells):

```bash
make dev
```

**One-shot run (no watch):** useful for quick verification, CI-style checks, or when an agent (e.g. Claude Code) needs to boot the server.

```bash
make css            # rebuild CSS once (minified)
go run main.go server
```

The server listens on `http://localhost:8080`.

See [AGENTS.md](AGENTS.md) for the rules coding agents follow (one-shot flow, process cleanup, Tailwind binary check).

Other useful targets live in the `Makefile` — `make test`, `make tidy`, `make generate` (runs `templ generate` + `go generate` + `make css`), `make run/live` (Air-powered live reload).

### Generate Mock from Interface

- Install [Mockery](https://github.com/vektra/mockery):

  ```bash
  go install github.com/vektra/mockery/v2@v2.53.4
  ```

  > **Note:** In this repo, we use Mockery v2.

- Add the following code in the interface code file: `//go:generate mockery --case snake --name XXXX`
- Run go generate using `make generate`

### Handle HTTP Client Exceptions

`client.Error` complies with Go standard error. which support Error, Unwrap, Is, As

```go
// Sample using errors.As
err := s.api.CreateResource(ctx, payload)
if err != nil {
    var cerr *client.Error
    if errors.As(err, &cerr) {
        fmt.Println(cerr.Message)          // General error message
        fmt.Println(cerr.StatusCode)       // HTTP status code e.g: 400, 401 etc.
        fmt.Println(cerr.RawError)         // Raw Go error object
        fmt.Println(cerr.RawAPIResponse)   // Raw API response body in byte
    }
}
```
