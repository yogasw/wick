# Config Tag Reference

Every exported field in a `Config` / `Configs` struct that carries a `wick:"..."` tag becomes one editable row in the `configs` table at boot time, scoped to that module's key. Fields without a tag are ignored.

## Tag grammar

Tags are semicolon-separated. A `key=value` pair sets a named attribute; a bare key is a boolean flag.

```go
type Config struct {
    // text input + description
    Title string `wick:"desc=Card title shown in the UI."`

    // url widget + required + description
    Endpoint string `wick:"url;required;desc=API base URL. Example: https://api.example.com"`

    // dropdown with fixed options
    Mode string `wick:"desc=Conversion mode.;dropdown=uppercase|lowercase|titlecase"`

    // multi-line textarea
    Template string `wick:"desc=Prompt template.;textarea"`

    // number input (also auto-applied for int/float fields)
    MaxRows int `wick:"desc=Max rows returned per query.;required"`

    // secret/password field
    APIKey string `wick:"desc=External API key.;secret;required"`

    // checkbox toggle (also auto-applied for bool fields)
    EnableCache bool `wick:"desc=Cache results across requests."`

    // editable table — see kvlist section below
    Groups string `wick:"kvlist=id|name;desc=Visible group definitions."`

    // override the auto snake_case column name
    LegacyKey string `wick:"key=legacy_api_key;secret;desc=Deprecated. Kept for v1 clients."`
}
```

## Widget reference

| Tag | Widget rendered | Notes |
|-----|----------------|-------|
| _(none / default string)_ | Text input | |
| `textarea` | Textarea | Multi-line |
| `dropdown=a\|b\|c` | Select | Pipe-separated options |
| `checkbox` | Checkbox toggle | Auto-applied for `bool` fields |
| `number` | Number input | Auto-applied for `int` / `float` fields |
| `secret` | Password input | Masked; never returned to form after first save |
| `email` | Email input | HTML `type="email"` |
| `url` | URL input | HTML `type="url"` |
| `color` | Color picker | HTML `type="color"` |
| `date` | Date picker | HTML `type="date"` |
| `datetime` | Date-time picker | HTML `type="datetime-local"` |
| `kvlist=col1\|col2` | Editable table | Value stored as JSON array — see below |

## Modifiers (any widget)

| Modifier | Effect |
|----------|--------|
| `desc=...` | Help text shown below the field in the admin UI |
| `required` | `c.Missing()` / `job.Missing()` returns this key until it is set |
| `locked` | Read-only in admin UI — set once at boot, not editable post-deploy |
| `key=custom_name` | Override the auto-derived snake_case key (`InitText` → `init_text`) |

## Key derivation

Field names are automatically snake-cased:

| Field | Key |
|-------|-----|
| `InitText` | `init_text` |
| `APIBaseURL` | `api_base_url` |
| `MaxRetries` | `max_retries` |

Override with `key=...` when the derived name is wrong or you need to keep a legacy key stable.

## kvlist — editable table widget

Use `kvlist` when a config field holds a **dynamic list of structured rows** — a set of IDs with labels, a mapping of endpoints, a table of question groups. Not for free-form text; use `textarea` for that.

```go
type Config struct {
    // multi-column: [{"id":"1","name":"Sales"},{"id":"2","name":"Support"}]
    Groups string `wick:"kvlist=id|name;desc=Visible question groups."`

    // single-column (bare kvlist): [{"value":"ID_001"},{"value":"ID_002"}]
    Allowlist string `wick:"kvlist;desc=Allowed sender IDs."`
}
```

**Value format** — stored in the `configs.value` column as a JSON array of string-keyed objects:

```json
[{"id":"1","name":"Sales"},{"id":"2","name":"Support"}]
```

**Read in Go:**

```go
var rows []map[string]string
if err := json.Unmarshal([]byte(c.Cfg("groups")), &rows); err == nil {
    for _, row := range rows {
        fmt.Println(row["id"], row["name"])
    }
}
```

**Admin UI behaviour:** renders an inline editable table. Rows can be added with **+ Add Row** (or Tab from the last cell) and removed with **×**. Changes auto-save 800 ms after the last keystroke — no Save button needed.

::: tip When to use kvlist
- Two or more columns per row → `kvlist=col1|col2|col3`
- One column only → bare `kvlist` (defaults to a `value` column)
- Free-form multi-line text → use `textarea` instead
:::
