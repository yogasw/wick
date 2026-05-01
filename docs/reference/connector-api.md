---
outline: deep
---

# Connector API

Public surface of the `github.com/yogasw/wick/pkg/connector` package — the contract every connector module satisfies. This is the API reference; for the conceptual guide and worked example, see [Connector Module](../guide/connector-module).

## `Meta`

Static metadata for a connector definition.

```go
type Meta struct {
    Key         string  // unique slug — drives /manager/connectors/{Key} and connector_runs.connector_id resolution
    Name        string  // display name (admin UI)
    Description string  // shown to admins; LLMs read per-Operation Description, not this
    Icon        string  // emoji or short string
}
```

`Key` must be unique across every connector. Collisions cause a fatal boot error. Once registered, the `Key` is stamped onto every connector row and `connector_runs` audit row — renaming requires a data migration.

## `ExecuteFunc`

The per-operation handler signature.

```go
type ExecuteFunc func(c *Ctx) (any, error)
```

The returned value is JSON-marshaled into the MCP `tools/call` result. Return a typed struct or slice for a stable, ramping shape rather than the raw upstream payload. Errors are stored verbatim in `connector_runs.error_msg` and surfaced in the [history page](../guide/connector-module#history-page) detail panel.

## `Operation`

One named action exposed by a connector definition.

```go
type Operation struct {
    Key         string          // slug a-z0-9_
    Name        string          // display name
    Description string          // LOAD-BEARING — LLM reads this in wick_list / wick_search
    Input       []entity.Config // reflected from the input struct
    Execute     ExecuteFunc
    Destructive bool            // defaults the per-row toggle off when true
}
```

`Description` is the primary signal an LLM uses to decide whether to call the op. Use action verbs and be specific about input/output shape:

- ✅ "Search Loki using LogQL. Returns log lines with timestamp + labels."
- ❌ "query loki"

`Destructive=true` ensures the operation defaults to disabled on every new connector row — admins must explicitly opt in at `/manager/connectors/{key}/{id}`.

## `Op` and `OpDestructive`

Constructors that reflect a typed input struct into an `Operation`.

```go
func Op[I any](key, name, description string, input I, exec ExecuteFunc) Operation
func OpDestructive[I any](key, name, description string, input I, exec ExecuteFunc) Operation
```

Pass `struct{}{}` for `input` when the operation takes no arguments.

```go
// Read-only / idempotent
connector.Op(
    "query", "Query Logs",
    "Search Loki using LogQL.",
    QueryInput{}, queryExec,
)

// Destructive (defaults off in every new row)
connector.OpDestructive(
    "delete_repo", "Delete Repository",
    "Permanently delete a GitHub repository. Cannot be undone.",
    DeleteRepoInput{}, deleteRepoExec,
)
```

## `Module`

The internal, fully-resolved registration record. Constructed by `app.RegisterConnector` — downstream code does not build `Module` directly.

```go
type Module struct {
    Meta       Meta
    Configs    []entity.Config // reflected from the Configs struct
    Operations []Operation
}
```

## `Ctx`

The per-call handle passed to every `ExecuteFunc`.

```go
type Ctx struct {
    HTTP *http.Client
    // ... internal fields
}
```

### Methods

```go
func (c *Ctx) Context() context.Context
```

Returns the cancellation context bound to this call. **Always** pass into `http.NewRequestWithContext` so the call aborts when MCP cancels (client disconnect, deadline). Skipping this is the single most common goroutine-leak source in custom connectors.

```go
func (c *Ctx) InstanceID() string
```

Returns the `connector_instances.id` this call is bound to. Useful for structured logging.

```go
func (c *Ctx) Cfg(key string) string
func (c *Ctx) CfgInt(key string) int
func (c *Ctx) CfgBool(key string) bool
```

Read a credential value declared on the connector's `Configs` struct. `key` is the snake_cased field name unless overridden with `wick:"key=..."`. Returns zero-value when absent.

```go
func (c *Ctx) Input(key string) string
func (c *Ctx) InputInt(key string) int
func (c *Ctx) InputBool(key string) bool
```

Read an LLM-supplied argument declared on the operation's `Input` struct. Same key derivation rules as `Cfg`.

```go
func (c *Ctx) ReportProgress(progress, total int, message string)
```

Emit a progress event to the active MCP session. Safe to call from any goroutine. No-op when the call is on the JSON transport (no SSE). Pass `total = 0` when the total is unknown — clients render the message + spinner.

## `NewHTTPClient` and `DefaultHTTPTimeout`

```go
const DefaultHTTPTimeout = 30 * time.Second

func NewHTTPClient() *http.Client
```

`Ctx.HTTP` is built from `NewHTTPClient()` by default. Connectors that need a different timeout can build their own `*http.Client` and assign it back to `Ctx.HTTP` inside `Execute` — `HTTP` is a public field, not a method.

## `ProgressReporter`

```go
type ProgressReporter interface {
    Report(progress, total int, message string)
}
```

The MCP layer wires an implementation that pushes JSON-RPC `notifications/progress` frames over the active SSE response. The JSON transport supplies no reporter and `ReportProgress` becomes a no-op. Implementations MUST NOT block — they drop events when the client is slow or disconnected.

## `NewCtx`

```go
func NewCtx(
    ctx context.Context,
    instanceID string,
    configs, input map[string]string,
    httpClient *http.Client,
    progress ProgressReporter,
) *Ctx
```

Used by wick when dispatching an MCP `tools/call` or a panel test. Downstream code does not call this directly.

## See also

- [Connector Module](../guide/connector-module) — concept guide with worked example
- [MCP for LLMs](../guide/mcp) — transport, dispatch, install snippets
- [Glossary](../guide/glossary) — quick term lookup
