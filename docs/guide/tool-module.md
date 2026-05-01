# Tool Module

Tools live in `tools/<name>/` and mount at `/tools/{key}`. The framework handles routing, admin config UI, tags, and visibility — the module only needs a `Register` func.

::: info Looking for LLM-facing modules?
Tools are designed for humans clicking a UI. For modules consumed by LLM clients (Claude, Cursor) over MCP, see [Connector Module](./connector-module).
:::

![Tool Detail](/screenshots/tool-detail.png)
*Example tool — Convert Text. Left panel lists modes, right panel handles input/output.*

![Tool Settings](/screenshots/tool-settings.png)
*Tool settings — runtime config values editable without redeploying.*

## File Structure

```
tools/my-tool/
├── handler.go    # Register func + HTTP handler funcs
├── service.go    # business logic (pure Go)
├── repo.go       # external I/O — DB, HTTP, S3 (stub if not needed)
├── view.templ    # templ HTML template
├── config.go     # typed Config struct (if tool has runtime knobs)
├── static.go     # //go:embed declaration (if JS assets)
└── js/
    └── mytool.js # tool-scoped JS, no CDN
```

## Register in main.go

```go
app.RegisterTool(
    tool.Tool{
        Key:               "my-tool",
        Name:              "My Tool",
        Description:       "What this tool does.",
        Icon:              "🔧",
        Category:          "Text",
        DefaultVisibility: entity.VisibilityPublic,
        DefaultTags:       []tool.DefaultTag{tags.Text},
    },
    mytool.Config{InitText: "hello"},
    mytool.Register,
)
```

One call = one card on the home grid. Call again with a different `Key` (and optionally a different `Config`) to get a second card backed by the same `Register` func.

For tools with no runtime config:

```go
app.RegisterToolNoConfig(
    tool.Tool{Key: "dashboard", Name: "Dashboard", Icon: "📊", ExternalURL: "https://grafana.example.com"},
    external.Register,
)
```

### tool.Tool fields

| Field | Description |
|-------|-------------|
| `Key` | Unique slug, kebab-case. Drives the mount path `/tools/{Key}` |
| `Name` | Display name shown on the card and page title |
| `Description` | Card subtitle |
| `Icon` | Emoji or short string shown on the card |
| `Category` | Groups cards visually on the home grid |
| `DefaultVisibility` | `entity.VisibilityPublic` or `entity.VisibilityPrivate` |
| `DefaultTags` | Slice of `tool.DefaultTag` from `tags/defaults.go` |
| `ExternalURL` | If set, card opens this URL in a new tab |

## Register Function

```go
package mytool

import "github.com/yogasw/wick/pkg/tool"

func Register(r tool.Router) {
    r.GET("/", index)
    r.POST("/", submit)
    r.Static("/static/", StaticFS) // only if you have JS assets
}
```

All paths are **relative** to `/tools/{key}` — never hardcode the full path.

## Handlers

Handlers are plain top-level funcs that receive `*tool.Ctx`:

```go
func index(c *tool.Ctx) {
    seed := c.Cfg("init_text")
    c.HTML(IndexBody(c.Meta().Name, c.Base(), seed))
}

func submit(c *tool.Ctx) {
    input := c.Form("input")
    c.HTML(IndexBody(c.Meta().Name, c.Base(), process(input)))
}
```

### Ctx helpers

| Helper | Description |
|--------|-------------|
| `c.Base()` | Absolute base path `/tools/{key}` |
| `c.Meta()` | The registered `tool.Tool` (Key, Name, Icon, …) |
| `c.Cfg(key)` | Read runtime config value for this instance |
| `c.CfgInt(key)` | Config value as int |
| `c.CfgBool(key)` | Config value as bool |
| `c.Missing()` | `required` config keys not yet set |
| `c.Form(key)` | Form field value |
| `c.Query(key)` | Query string value |
| `c.BindJSON(&v)` | Decode JSON body |
| `c.HTML(body)` | Write HTML response |
| `c.JSON(status, v)` | Write JSON response |
| `c.Redirect(url, code)` | Redirect |

## Runtime Config

Declare a `Config` struct in `config.go`:

```go
package mytool

type Config struct {
    InitText string `wick:"desc=Seed text on first load."`
    APIKey   string `wick:"desc=External API key.;secret;required"`
    MaxItems int    `wick:"desc=Max results.;number"`
    Mode     string `wick:"desc=Processing mode.;dropdown=fast|accurate|balanced"`
}
```

The framework reflects the struct into `configs` table rows at boot. Admin edits are live on the next request — no redeploy.

### wick tag reference

| Tag | Widget | Notes |
|-----|--------|-------|
| `desc=...` | — | Description shown in admin |
| `required` | — | `c.Missing()` returns this key until set |
| `secret` | Password | Masked in UI |
| `textarea` | Textarea | Multi-line |
| `dropdown=a\|b\|c` | Select | Pipe-separated options |
| `checkbox` | Checkbox | Auto-applied for `bool` fields |
| `number` | Number | Auto-applied for `int`/`float` fields |
| `email`, `url`, `color`, `date`, `datetime` | Typed input | HTML input type |
| `key=custom_name` | — | Override auto snake_case key |

Field names are auto snake-cased: `InitText` → `init_text`.

## JavaScript Assets

```go
// static.go
package mytool

import "embed"

//go:embed js
var StaticFS embed.FS
```

Mount and reference in handler + templ:

```go
r.Static("/static/", StaticFS)
```

```html
<script src={ base + "/static/js/mytool.js" }></script>
```

::: warning
`//go:embed js` fails if `js/` doesn't exist. Create the directory with at least one file before running `go build`.
:::

## Tags

Add shared tags in `tags/defaults.go`:

```go
var MyGroup = tool.DefaultTag{
    Name:        "MyGroup",
    Description: "Tools for X.",
    IsGroup:     true,
    SortOrder:   20,
}
```

Reference in `main.go`:

```go
DefaultTags: []tool.DefaultTag{tags.MyGroup},
```

::: tip
Check if an existing tag fits before adding a new one — fewer tags keeps the home grid clean.
:::
