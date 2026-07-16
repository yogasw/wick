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

    // native checkbox (auto-applied for bool fields)
    EnableCache bool `wick:"desc=Cache results across requests."`

    // toggle switch — clearer on/off visual; opt-in via `bool` flag
    DebugMode bool `wick:"bool;desc=Verbose logging."`

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
| `checkbox` | Native checkbox | Auto-applied for Go `bool` fields. Compact, classic style. |
| `bool` / `boolean` | Toggle switch | Boolean with clear on/off track + knob. Use when state should read at a glance. |
| `number` | Number input | Auto-applied for `int` / `float` fields |
| `secret` | Password input | Masked; value never sent to browser. Shows `••••••••` when set. **For connector Configs/Input, also opts the field into the encrypted-fields layer** — see [`encrypted-fields`](../encrypted-fields/SKILL.md): wick auto-decrypts incoming `wick_enc_` tokens and auto-masks the plaintext in the response back to the LLM |
| `email` | Email input | HTML `type="email"` |
| `url` | URL input | HTML `type="url"` |
| `color` | Color picker | HTML `type="color"` |
| `date` | Date picker | HTML `type="date"` |
| `datetime` | Date-time picker | HTML `type="datetime-local"` |
| `kvlist=col1\|col2` | Editable table | Value stored as JSON array — see below |
| `picker=<source>` | Searchable typeahead with chips | Value stored as JSON `[{id,name},...]`. Backed by a `LookupProvider`. **Channels only** — there is no `LookupProvider` path for connectors (they run out-of-process over gRPC, and only channels wire the `/lookup` endpoint). On a **connector**, use `html=<op>` for a picker instead (see next row). A bare `picker` with no source degrades to plain text. |
| `html=<op>` | Server-rendered widget | Fetches markup from connector op `<op>` (`{html:"..."}`), renders it read-only. Buttons in the HTML drive behaviour via a `data-op`/`data-arg` convention: `data-op="__select"` stores `data-arg` as the value; `data-op="<opKey>"` runs that op via the admin `/test` path then re-fetches. The widget always calls the op with the current field value in an arg named `browser`, and auto-selects when the markup offers exactly one `__select` option. Core is domain-agnostic — all layout/logic live in the connector's HTML. **Declare the backing op with `connector.OpConfigOnly(...)`** (NOT `AdminOnly`) so it's hidden from the whole MCP surface (wick_list / wick_get / wick_search) yet still runnable from the config UI via `/test` by any user who can configure the instance — see the connector-module skill. AdminOnly is wrong here: it does NOT hide the op from the catalog and it blocks non-admin config users. |

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
| `group=Title` / `group=Title\|Description` / `group=Title\|Description\|collapsed` | Cluster fields into a titled card on the admin Settings page (fields sharing a `Title` render together, first-seen order; no `group` → default "Configuration" card). Optional pipe-separated `Description` is written once at the card top. A 3rd `\|collapsed` segment makes the card **start collapsed** (click header to expand) — use for advanced/rarely-edited groups; `Title\|\|collapsed` collapses with no description. Only the group's first-seen field needs the description / collapsed flag. Not persisted, pure presentation. |
| `mode=fixed` / `mode=expression` | **Workflow editor only.** Locks the per-field Fixed ⇄ Expression toggle to that mode — the pill renders greyed out and can't be changed. Omit (default) to leave the toggle free (defaults to fixed). Use `mode=fixed` for values that must never be templated (a literal enum, a fixed endpoint path) and `mode=expression` for values that only make sense as a template. Any value other than `fixed`/`expression` is ignored (treated as free). Pure UI hint — the admin Settings page (templ) renders no toggle. |

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
