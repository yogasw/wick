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
| `secret` | Password input | Masked; value never sent to browser. Shows `••••••••` when set |
| `email` | Email input | HTML `type="email"` |
| `url` | URL input | HTML `type="url"` |
| `color` | Color picker | HTML `type="color"` |
| `date` | Date picker | HTML `type="date"` |
| `datetime` | Date-time picker | HTML `type="datetime-local"` |
| `kvlist=col1\|col2` | Editable table | Value stored as JSON array — see below |
| `picker=<source>` | Searchable typeahead with chips | Value stored as JSON `[{id,name},...]`. Requires the parent module to implement a `LookupProvider`. See below. |

## Modifiers (any widget)

| Modifier | Effect |
|----------|--------|
| `desc=...` | Help text shown below the field in the admin UI |
| `default=...` | Seed value used when the Go field is its zero value (`""`, `0`, `false`) |
| `required` | `c.Missing()` / `job.Missing()` returns this key until it is set |
| `locked` | Read-only in admin UI — set once at boot, not editable post-deploy |
| `regen` | Shows a regenerate button in admin UI — key must have a registered generator |
| `key=custom_name` | Override the auto-derived snake_case key (`InitText` → `init_text`) |
| `visible_when=field:value` | Show this field in the admin UI only while another field equals the named value. Pure presentation hint — value is still seeded / saved normally. |
| `hidden` | Skip the field in the default admin Settings page. Row is still seeded to DB and readable via `c.Cfg(...)`, so runtime works normally — use for fields managed by a dedicated page (e.g. channel setup composers). |

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

## picker — searchable typeahead widget

Use `picker` when the legal values come from an **upstream directory** (a Slack workspace, a Discord guild, a customer table) and the operator should pick chips by name instead of pasting raw IDs.

```go
type Config struct {
    AllowedUsers    string `wick:"picker=slack.users;desc=Allowed users."`
    AllowedChannels string `wick:"picker=slack.channels;desc=Allowed channels."`
}
```

**Value format** — identical to a 2-column `kvlist=id|name`:

```json
[{"id":"U123","name":"Yoga"},{"id":"U456","name":"Deva"}]
```

So any whitelist check is just `id`-membership, and the same parser reads either widget.

**Lookup source** — the value after `=` is a registry key (e.g. `slack.users`). The parent module — typically a channel — must implement `LookupProvider`:

```go
type LookupProvider interface {
    Lookup(source, query string) ([]LookupItem, error)
}
type LookupItem struct{ ID, Name string }
```

The admin UI debounces 250 ms per keystroke and fires `GET /channels/<slug>/lookup?source=<src>&q=<q>`. Implementations should cap results (~20) and cache aggressively — see [`slack/lookup.go`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/slack/lookup.go) for a 60 s in-memory cache example.

**Admin UI behaviour:** a search input with a debounced dropdown; click → chip. Chips have an `×` to remove. Like kvlist, changes auto-save with no Save button.

::: tip When to use picker vs kvlist
- IDs come from an upstream directory the user knows by name → `picker`
- IDs are arbitrary / freeform / configured locally → `kvlist=id|name`
:::

## visible_when — conditional fields

Hide a field from the admin form until another field equals a target value:

```go
type Config struct {
    Mode    string `wick:"dropdown=all|whitelist;default=all"`
    Allowed string `wick:"picker=slack.users;visible_when=mode:whitelist;desc=Allowed users."`
}
```

The field still seeds and persists normally — `visible_when` only toggles the form row. Useful for cutting noise in config pages with many feature-flagged dependants.
