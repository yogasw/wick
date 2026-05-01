# Connector Module

Connectors are the third class of wick module beside Tools and Jobs. They wrap one external API and expose it to LLM clients (Claude, Cursor, custom agents) over [MCP](./mcp). Where Tools are designed for humans clicking a UI and Jobs run on a schedule, connectors exist so an LLM can call your APIs with structured input/output and full audit logging — no protocol code on your side.

![Connector instances list with kebab menu](/screenshots/connector-list.png)

*`/manager/connectors/{key}` list page with stacked rows + tag chips + kebab menu.*

## Mental model

- One connector module wraps **one external API**.
- It carries one shared `Configs` struct (URL, token, …) plus **N `Operations`** — small, named actions an LLM can call (`query`, `list_repos`, `create_issue`).
- Each operation has its own typed `Input` struct (turned into MCP JSON Schema) and its own `ExecuteFunc`.
- An admin can spawn **many rows per definition** at runtime through `/manager/connectors/{key}` — each row carries its own credentials, label, and tags. Same Go code, different rows = different (env, team, account).
- LLMs do not see N×M static tools. The MCP server exposes a fixed meta surface (`wick_list`, `wick_search`, `wick_get`, `wick_execute`); each (row × operation) pair is addressed by an opaque `tool_id` of the form `conn:{connector_id}/{op_key}`. See the [MCP guide](./mcp) for the full transport story.

## File structure

```
connectors/my-connector/
├── connector.go    # Meta + Configs + per-op Input structs + Operations() + ExecuteFunc impls
├── service.go      # optional split when the file outgrows ~400 lines
└── repo.go         # rare — only for external I/O beyond direct HTTP (e.g. a DB cache)
```

Most connectors fit in a single `connector.go`. The shipped example [`connectors/crudcrud/connector.go`](https://github.com/yogasw/wick/blob/master/template/connectors/crudcrud/connector.go) is ~290 lines and covers five operations including one destructive — that's a good upper bound for the single-file pattern.

## Register in main.go

```go
import "<projectmod>/connectors/myconnector"

app.RegisterConnector(
    myconnector.Meta(),
    myconnector.Configs{
        BaseURL: "https://api.example.com", // seed; admins can change after first boot
    },
    myconnector.Operations(),
)
```

One call = one connector definition. Wick auto-seeds **one empty row** in the `connectors` table on first boot. Admins fill in the credential values at `/manager/connectors/{key}/{id}` before any operation can run.

- Existing rows are never overwritten on subsequent boots.
- Two registrations with the same `Meta.Key` cause a fatal boot error.
- A `Key` whose module backing has been removed is tolerated: the row stays in the DB, marked "Module not registered" in the admin UI; calls return an error.

## Connector contract

### `Meta()`

```go
func Meta() connector.Meta {
    return connector.Meta{
        Key:         "github",
        Name:        "GitHub",
        Description: "Read repos, issues, and pull requests on GitHub.",
        Icon:        "🐙",
    }
}
```

| Field | Description |
|-------|-------------|
| `Key` | Unique slug across all connectors. Drives the admin URL `/manager/connectors/{Key}` |
| `Name` | Display name on the admin card and detail page |
| `Description` | Shown to admins. The LLM never reads this — see per-`Operation` Description below |
| `Icon` | Emoji or short string |

### `Configs` struct

Per-instance credentials and endpoints, shared across every operation. Reflected by `entity.StructToConfigs` into the admin form.

```go
type Configs struct {
    BaseURL string `wick:"url;required;desc=GitHub API base URL. Default: https://api.github.com"`
    Token   string `wick:"secret;required;desc=Personal access token with the scopes you intend to use."`
}
```

The `wick:"..."` tag grammar is shared with Tools and Jobs. See the [Tool Module](./tool-module#runtime-config) reference for the full table. Common modifiers:

| Tag | Effect |
|-----|--------|
| `required` | Admin must fill before any op can run |
| `secret` | Masked in the UI; never returned to the form after first save |
| `url`, `email`, `textarea`, `dropdown=a\|b\|c` | Widget overrides |
| `desc=...` | Help text shown next to the field |
| `key=custom_name` | Override the snake_cased field name |

Read at runtime via `c.Cfg("base_url")`, `c.CfgInt("port")`, `c.CfgBool("use_tls")`.

### Per-operation `Input` structs

Each operation has its own input schema. Same tag grammar; the framework reflects each one into the JSON Schema the MCP client sees in `wick_get`.

```go
type ListReposInput struct {
    Org        string `wick:"required;desc=Organization login. Example: anthropics"`
    Visibility string `wick:"dropdown=all|public|private;desc=Filter by repo visibility."`
    PerPage    int    `wick:"desc=Page size (1-100). Default 30."`
}
```

Read at runtime via `c.Input("org")`, `c.InputInt("per_page")`, `c.InputBool("include_archived")`.

### `Operations()`

```go
func Operations() []connector.Operation {
    return []connector.Operation{
        connector.Op(
            "list_repos",
            "List Repositories",
            "List repositories under {org}. Returns repo name, full_name, default_branch, visibility, and updated_at.",
            ListReposInput{},
            listRepos,
        ),
        connector.OpDestructive(
            "close_issue",
            "Close Issue",
            "Close the given issue on {owner}/{repo}. Reversible only by reopening; comments and history are preserved.",
            CloseIssueInput{},
            closeIssue,
        ),
    }
}
```

| Constructor | When to use |
|-------------|-------------|
| `connector.Op(...)` | Read-only or idempotent writes that can be safely retried |
| `connector.OpDestructive(...)` | Actions you do not want the LLM to fire by mistake — DELETE, send-message, post-comment, force-push |

A destructive op is **disabled by default** on every new row. Admins opt in per (row, operation) at `/manager/connectors/{key}/{id}`.

### Description discipline

`Operation.Description` is **load-bearing**: it appears verbatim in the MCP `wick_list` / `wick_search` payload and is the primary signal an LLM uses to decide whether to call the op.

Write action verbs and be specific:

- ✅ "Search Loki using LogQL. Returns log lines with timestamp + labels. Empty result = empty array."
- ✅ "Send a Slack message to {channel}. Returns the posted message timestamp on success."
- ❌ "query loki"
- ❌ "send slack"

### `ExecuteFunc`

```go
func listRepos(c *connector.Ctx) (any, error) {
    org := strings.TrimSpace(c.Input("org"))
    if org == "" {
        return nil, errors.New("org is required")
    }

    base := strings.TrimRight(c.Cfg("base_url"), "/")
    url := fmt.Sprintf("%s/orgs/%s/repos", base, org)

    req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, url, nil)
    if err != nil {
        return nil, fmt.Errorf("build request: %w", err)
    }
    req.Header.Set("Authorization", "Bearer "+c.Cfg("token"))
    req.Header.Set("Accept", "application/vnd.github+json")

    resp, err := c.HTTP.Do(req)
    if err != nil {
        return nil, fmt.Errorf("call github: %w", err)
    }
    defer resp.Body.Close()
    raw, _ := io.ReadAll(resp.Body)
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return nil, fmt.Errorf("github %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
    }

    var out []struct {
        Name          string `json:"name"`
        FullName      string `json:"full_name"`
        DefaultBranch string `json:"default_branch"`
        Visibility    string `json:"visibility"`
    }
    if err := json.Unmarshal(raw, &out); err != nil {
        return nil, fmt.Errorf("decode response: %w", err)
    }
    return out, nil
}
```

### Ctx helpers

| Helper | Description |
|--------|-------------|
| `c.Cfg("key")` | Read a credential / config value (string) |
| `c.CfgInt("key")` | Same, parsed as int (0 on parse failure) |
| `c.CfgBool("key")` | Same, parsed as bool (`true`/`1`/`yes`/`on`) |
| `c.Input("key")` | Read an LLM-supplied input (string) |
| `c.InputInt("key")` | Same, as int |
| `c.InputBool("key")` | Same, as bool |
| `c.Context()` | The cancellation context. **Always** pass into `http.NewRequestWithContext` |
| `c.HTTP` | Default `*http.Client` with a 30s timeout |
| `c.InstanceID()` | Connector row ID — useful for structured logging |
| `c.ReportProgress(progress, total, message)` | Emit progress for long-running calls (no-op on JSON transport) |

### Best practices

1. **`http.NewRequestWithContext` is mandatory.** Plain `http.NewRequest` leaks the goroutine when an MCP call is cancelled (client disconnect, deadline). This is the single most common bug in custom connectors.
2. **Validate input early.** Do presence and bound checks before the HTTP call so the error message lands cleanly in the run history.
3. **Wrap upstream errors with `%w`.** The error chain is rendered in the history detail panel; `errors.Is` / `errors.As` keeps working downstream.
4. **Return typed shapes when you can.** A struct with explicit `json:` tags gives the LLM a clean, stable schema independent of upstream cosmetics. Returning the raw upstream body works but breaks the moment upstream tweaks its envelope.
5. **Mark destructive ops.** Anything that mutates state in a hard-to-undo way — DELETE, post, send, force-push — should be `OpDestructive`. This defaults the toggle off on every new row.

### Anti-patterns

- ❌ `http.NewRequest` (no context) — goroutine leak.
- ❌ Returning `*http.Response.Body` reader directly — the framework cannot marshal it. Always `io.ReadAll` + decode.
- ❌ Storing config values in package-level vars — connectors are multi-row; every call must read fresh from `c.Cfg(...)`.
- ❌ Op keys with hyphens or spaces — slug only (`a-z0-9_`).
- ❌ Sharing mutable state across `Execute` invocations — concurrent calls.
- ❌ Polling tight loops without honoring `c.Context().Done()`.

## Per-row management UI

![Connector instance detail page](/screenshots/connector-detail.png)

*`/manager/connectors/{key}/{id}` detail page — identity, action bar, label form, credentials, operations table.*

`/manager/connectors/{key}/{id}` is the per-row settings page. Five sections:

1. **Identity** — label, status badge, opaque row ID.
2. **Top actions** — History, Duplicate, Disable/Enable, Delete.
3. **Label form** — rename without redeploying.
4. **Credentials** — auto-rendered from your `Configs` struct via `entity.StructToConfigs`.
5. **Operations** table — one row per operation. Each row carries:
   - `[Test]` link → opens the test panel (`/test?op=<key>`)
   - `[History]` link → opens the audit log filtered to that op
   - `Enable / Disable` toggle — admins explicitly opt in to destructive ops here.

### Test panel (Postman-style)

![Connector test runner with success result](/screenshots/connector-test.png)

*`/manager/connectors/{key}/{id}/test?op=...` Postman-style runner — input form + Run button + success result panel.*

`GET /manager/connectors/{key}/{id}/test?op=<op_key>` is the in-app runner. It uses the exact same code path as the MCP `tools/call` — verify behavior end-to-end without leaving the browser.

- **URL-synced operation dropdown** — switching the operation rewrites `?op=...` via `history.replaceState`. Refresh and back-button preserve the choice; deep links from the detail page can pre-select.
- **Prefill from history** — the History page's "Retry" link points at `/test?op=<op>&prefill=<run_id>`. The handler decodes the prior run's input and pre-fills every matching input field. You review and edit before clicking Run — never auto-replay.
- Every Run writes a row to `connector_runs` (source = `test`).

### History page

![Connector run history with expanded error row](/screenshots/connector-history.png)

*`/manager/connectors/{key}/{id}/history` audit log — filter chips + table + expanded row showing Request/Response JSON + Retry link.*

`GET /manager/connectors/{key}/{id}/history?op=...&source=...&status=...&user=...` is a paginated audit log.

- Filter chips are URL-driven — every change rewrites the query string. Links are shareable; refresh preserves filters.
- 10 rows per page, server-side pagination.
- The User column resolves IDs to display names in batch (no N+1 queries).
- Click a row to expand inline — full Request JSON, Response JSON, run ID, IP, user agent, and HTTP status, no extra round trip.
- The expanded panel includes a "Retry in test panel" link that navigates to the test page with the prior input prefilled. Manual replay only — wick deliberately does not offer a one-click POST replay so you can review and adjust before re-running.

## Sharing connectors with tags

Every connector row is **private by default at the transport level** — the `/mcp` endpoint always requires a bearer token; there is no anonymous access. Within authenticated users, visibility is gated by tag filter (the same mechanism Tools and Jobs use):

- A row with **0 filter tags** is visible to every approved user.
- A row with **≥1 filter tag** is visible only to users who carry at least one matching tag.
- Admins bypass the filter — they always see every row.

Tag strings are arbitrary and admin-defined. Conventional prefixes like `team:platform`, `env:prod`, `user:alice@example.com` are just naming conventions, not enforced by code.

When the user asks for a "private" connector:

- If they mean "not exposed to anonymous users" — that's already the default, no action needed.
- If they mean "only people on team X" — add a filter tag. Apply the tag both to the connector row (at `/manager/connectors/{key}/{id}`) and to the team members' user records (at `/admin/users`).

The fixed MCP tools `wick_list`, `wick_get`, and `wick_execute` re-check tag visibility on every call — they never trust a list-time cache. Removing a user's tag takes effect on the next call.

## Verifying your connector

After registering and filling credentials:

1. **Compile and boot:**
   ```bash
   wick dev
   ```
2. **Smoke test in the browser:**
   - Open `/manager/connectors/{key}` — your auto-seeded row appears.
   - Open the row, fill in `Configs`, save.
   - Open `/test?op=<op>`, fill input, click Run, confirm a success result.
3. **Check the audit log:**
   - Open `/history`, find the run row.
   - Expand to confirm Request/Response JSON, latency, and run ID.
4. **Smoke test from MCP** (optional but recommended):
   - Generate a Personal Access Token at [`/profile/tokens`](./access-tokens).
   - Add to your Claude Desktop config — see the [MCP guide](./mcp#install-snippets) for the snippet.
   - Restart Claude Desktop, ask it to call your connector.

## Reference

- Public API: `pkg/connector` — `Meta`, `Module`, `Operation`, `Op`, `OpDestructive`, `ExecuteFunc`, `Ctx`
- Canonical example: [`connectors/crudcrud/connector.go`](https://github.com/yogasw/wick/blob/master/template/connectors/crudcrud/connector.go)
- MCP transport: [MCP for LLMs](./mcp)
- Auth modes: [Access Tokens (PAT)](./access-tokens), [OAuth Connections](./oauth-connections)
- Audit retention: [Connector Runs Purge](./connector-runs-purge)
