---
name: connector-module
description: Use for ANY work on a connector in this project — creating a new connector under connectors/, refactoring/improving an existing one, fixing bugs, adding new operations, editing the Configs or per-op Input structs, or wiring a connector up in main.go. Connectors are the third class of wick module beside Tools and Jobs, designed specifically to be consumed by LLM clients (Claude, Cursor, custom agents) over MCP. Enforces the full module contract — pkg/connector.{Meta, Module, Operation, Op, OpDestructive, ExecuteFunc, Ctx} — plus the wick:"..." tag grammar shared with Tools and Jobs, the destructive-opt-in model, the typed-response convention, and the http.NewRequestWithContext rule that prevents goroutine leaks. Also mandates the "ask before adding ops" question whenever a new connector is requested.
allowed-tools: Read, Grep, Glob, Edit, Write, Bash
paths:
  - "connectors/**"
  - "main.go"
  - "tags/**"
  - "AGENTS.md"
  - "README.md"
---

# Connector Module — downstream

> **Scope:** this skill is for connectors in your downstream wick project — modules that live under `connectors/` and are registered from `main.go` via `app.RegisterConnector(...)`. If you are editing **wick itself** (the framework source under `internal/connectors/`), use the upstream skill at `.claude/skills/connector-module/SKILL.md` in the wick repo — paths, registration site, and verification steps differ.

Activate this skill whenever the user touches a **connector** — creating, improving, fixing, refactoring, or adding operations. When editing an existing connector, first audit it against the rules below and bring it up to spec as part of the change; don't leave it half-compliant.

## Mental model

A connector wraps **one external API** for LLM consumption.

- One `Module` carries one shared `Configs` struct (URL, token, ...) and **N `Operations`** (`query`, `list_repos`, `create_issue`, ...).
- Each `Operation` has its own typed `Input` struct (turned into the MCP JSON Schema) and its own `ExecuteFunc`.
- An admin can create **many rows per definition** at runtime through `/manager/connectors/{key}` — each row carries its own credential values, label, and tags. Same Go code, different rows = different (env, team, account).
- LLMs do not see N×M static tools. The MCP server exposes a fixed meta surface (`wick_list`, `wick_search`, `wick_get`, `wick_execute`); each (row × operation) pair is addressed by an opaque `tool_id` of the form `conn:{connector_id}/{op_key}`.

If the request is shaped like "let humans click a UI", use the `tool-module` skill instead — that's a Tool. If it's "run on a schedule", that's a Job. Connectors are LLM-shaped only.

## Applies to (non-exhaustive triggers)

- "Add a connector for X API"
- "Add a new operation Y to connector X"
- "Fix a bug in connector X"
- "Refactor connector X"
- Any edit under `connectors/{name}/`
- Changes to `main.go` that involve `app.RegisterConnector(...)`

## Before building: ALWAYS ask 4 questions

Whenever the user asks for a **new** connector, the very first reply back is:

> *"Before I write anything, please confirm four things so the shape is right:*
> 1. *Endpoint + auth — what does this API need? (base URL, API key, OAuth token, etc. — these become fields on `Configs`)*
> 2. *Operations — which actions should the LLM be able to call? List name, input arguments, and whether each one is destructive (delete, post, send — these default to off on every new row).*
> 3. *Output shape — typed struct or passthrough JSON? (Recommended: typed struct so the LLM gets a clean, stable shape independent of upstream changes.)*
> 4. *Default tags — group-only, or do you also want a filter tag to restrict who can use this connector? (Default is group-only — every approved user sees it.)"*

Don't silently invent operations or skip the destructive flag. The destructive flag is the single most important per-op signal — it defaults the row toggle off so admins must opt in.

When auditing an existing connector as part of a change, similarly ask before adding a new op or removing one.

## Module layout

```
connectors/myconn/
  connector.go    # Meta + Configs + per-op Input structs + Operations() + every ExecuteFunc
  service.go      # optional split when business logic outgrows connector.go
  repo.go         # rare — only for external I/O beyond direct HTTP (e.g. a DB cache)
```

Most connectors fit in a single `connector.go`. Reach for split files only when the file passes ~400 lines or the helpers stop being incidental.

## File contract

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

- `Key` is a unique slug across all connectors in this project (kebab-case).
- `Description` is shown to admins. The LLM never reads this — it reads per-`Operation` Description instead.

### `Configs` struct

Per-instance credentials and endpoints, shared across every operation. Admins fill these in at `/manager/connectors/{Key}/{id}` after wick auto-seeds the first row at boot.

```go
type Configs struct {
    BaseURL string `wick:"url;required;desc=GitHub API base URL. Default: https://api.github.com"`
    Token   string `wick:"secret;required;desc=Personal access token with the scopes you intend to use."`
}
```

Tag grammar (same as Tools/Jobs — `tool-module` skill has the full table):

| Field | Default widget | Override flags |
|---|---|---|
| `string` | `text` | `textarea`, `dropdown=a\|b\|c`, `email`, `url`, `color`, `date`, `datetime` |
| `bool` | `checkbox` | — |
| `int`/`float` | `number` | — |

Common modifiers: `required`, `secret`, `desc=...`, `key=custom_name`.

**Read at runtime via `c.Cfg("base_url")`, `c.CfgInt("port")`, `c.CfgBool("use_tls")`** — keys are the snake_cased field name unless overridden with `key=`.

### Per-operation `Input` structs

Each operation has its own input schema. Same `wick:"..."` grammar; the framework reflects it into the MCP JSON Schema clients see in `wick_get`.

```go
type ListReposInput struct {
    Org        string `wick:"required;desc=Organization login. Example: anthropics"`
    Visibility string `wick:"dropdown=all|public|private;desc=Filter by repo visibility."`
    PerPage    int    `wick:"desc=Page size (1-100). Default 30."`
}
```

**Read at runtime via `c.Input("org")`, `c.InputInt("per_page")`, `c.InputBool("include_archived")`.**

### `Operations()`

```go
func Operations() []connector.Operation {
    return []connector.Operation{
        connector.Op(
            "list_repos",
            "List Repositories",
            "List repositories under {org}. Returns repo name, full_name, default_branch, visibility, and updated_at. Pagination is single-page only.",
            ListReposInput{},
            listRepos,
        ),
        connector.Op(
            "get_issue",
            "Get Issue",
            "Fetch one issue from {owner}/{repo} by number. Returns title, body, state, labels, assignees, and comments_url.",
            GetIssueInput{},
            getIssue,
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

**`Op` vs `OpDestructive`:**
- `Op(...)` for read-only or idempotent writes that can be safely retried.
- `OpDestructive(...)` for actions the user does not want the LLM to fire by mistake — DELETE, send-message, post-comment, force-push, anything that touches money or user-visible state.

A destructive op is **disabled by default** on every new row. Admins opt in per (row, operation) at `/manager/connectors/{key}/{id}`.

### Description discipline

`Operation.Description` is the **load-bearing** signal the LLM uses to decide whether to call this op. It shows up verbatim in `wick_list` / `wick_search` payloads.

Write action verbs and be specific about input/output shape:

- ✅ "Search Loki using LogQL. Returns log lines with timestamp + labels. Empty result = empty array."
- ✅ "Send a Slack message to {channel}. Returns the posted message timestamp on success. Idempotent if {client_msg_id} is supplied."
- ❌ "query loki"
- ❌ "send slack"

### `ExecuteFunc`

```go
func listRepos(c *connector.Ctx) (any, error) {
    org := strings.TrimSpace(c.Input("org"))
    if org == "" {
        return nil, errors.New("org is required")
    }
    perPage := c.InputInt("per_page")
    if perPage <= 0 || perPage > 100 {
        perPage = 30
    }

    base := strings.TrimRight(c.Cfg("base_url"), "/")
    url := fmt.Sprintf("%s/orgs/%s/repos?per_page=%d", base, org, perPage)

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
        UpdatedAt     string `json:"updated_at"`
    }
    if err := json.Unmarshal(raw, &out); err != nil {
        return nil, fmt.Errorf("decode response: %w", err)
    }
    return out, nil
}
```

### Golden rules for `ExecuteFunc`

1. **MUST** build requests with `http.NewRequestWithContext(c.Context(), ...)` — never plain `http.NewRequest`. Without this, MCP cancellations (client disconnect, deadline) cannot abort the upstream request and the goroutine leaks for the duration of whatever the upstream eventually returns. This is the single most common bug in custom connectors.
2. **MUST** validate `Input` early (presence, sane bounds) before doing the HTTP call. Errors returned here become `connector_runs.error_msg` on the history page.
3. **MUST** read configs via `c.Cfg(...)` / inputs via `c.Input(...)`. Never via process-level singletons or env vars at call time — that breaks the multi-row model.
4. **MUST** use `c.HTTP` as the starting client (carries a 30s default timeout). Replace it locally if you need a different transport, but do it in a comment-explained way.
5. **SHOULD** wrap upstream errors with `fmt.Errorf("...: %w", err)` so the chain reads cleanly in the history detail panel.
6. **SHOULD** transform the upstream response into a typed struct or map shape that's stable across upstream changes. Returning the raw upstream body works but means LLMs see noise (envelopes, pagination cursors, debug fields) and break when upstream tweaks the shape.
7. **SHOULD** mark destructive operations with `OpDestructive(...)` — the framework defaults the toggle off so it's an explicit admin opt-in.

### Anti-patterns

- ❌ Using `http.NewRequest` (no context) — goroutine leak.
- ❌ Returning `*http.Response.Body` reader directly — the framework cannot marshal it to JSON. Always `io.ReadAll` + decode.
- ❌ Storing config values in package-level vars set at init — connectors are multi-row, every call must read fresh from `c.Cfg(...)`.
- ❌ Op keys with hyphens or spaces — slug only (`a-z0-9_`).
- ❌ Sharing mutable state across `Execute` invocations — connector calls are concurrent.
- ❌ Sleeping or polling inside `Execute` — for long work, push progress with `c.ReportProgress(...)` and respect `c.Context().Done()`.

## Registration in `main.go`

```go
import myconn "<projectmod>/connectors/myconn"

app.RegisterConnector(
    myconn.Meta(),
    myconn.Configs{
        BaseURL: "https://api.example.com", // seed; admins can change after first boot
    },
    myconn.Operations(),
)
```

- One call = one connector definition.
- `app.RegisterConnector` is generic over the `Configs` type — pass the typed struct, not a `map[string]string`.
- Pass an empty `Configs{}` (or `struct{}{}` if your connector has no credentials) when there's nothing meaningful to seed.

## Bootstrap

The first time wick boots after registration, it auto-creates **one empty row** per connector definition in the `connectors` table. Admins fill in the credential values at `/manager/connectors/{key}/{id}` before any operation can run.

- Existing rows are never overwritten on subsequent boots — admin edits are durable.
- A registered `Key` with no module backing (e.g. you removed the import) is tolerated: the row stays in the DB but is marked "Module not registered" in the admin UI; calls return an error.
- Two registrations with the same `Key` are a fatal boot error.

## Verifying your work

1. **Compile:**
   ```bash
   go build ./...
   ```
2. **Boot the dev server, smoke-test the connector, then kill the port:**
   ```bash
   wick dev   # or `go run . server`
   # in another terminal:
   curl http://localhost:8080/healthz
   # exercise the connector via /manager/connectors/{key}/{id}/test?op=...
   # then kill the port — never leave the dev server running after the smoke test
   ```
3. **Manual confirmation:**
   - Card visible on `/manager/connectors` admin overview.
   - First row auto-seeded at `/manager/connectors/{key}`.
   - Filling in `Configs` makes the test panel runnable.
   - `/manager/connectors/{key}/{id}/test?op=<op>` runs the op end-to-end and writes a row to `connector_runs`.
   - History page shows the run with status, latency, and request/response JSON when expanded.
   - Destructive ops are flagged off in the operations table; toggling on requires an explicit click.
4. **MCP smoke (optional but recommended):**
   - Generate a Personal Access Token at `/profile/tokens`.
   - Paste it into a Claude Desktop config, restart Claude Desktop, confirm `wick_list` returns the new (row × op) entries.

## Tags

A connector row can carry tags (group + filter) just like a tool. By default a fresh row has no tags — visible to every approved user. Admins can add filter tags at `/manager/connectors/{key}/{id}` to restrict access to specific user-tags.

When the user asks for a "private" connector, ask whether they want a filter tag applied or whether the default (no tags = visible to all approved users) is fine. Don't add `IsFilter` tags on your own initiative.

## When to ask before acting

- New external API that requires its own OAuth dance (separate from wick's OAuth surface) — confirm authn flow, where tokens go, and refresh strategy before writing code.
- **Removing an existing connector or operation** — confirm: removing an op orphans `connector_operations` rows and breaks any active MCP client that listed the old `tool_id`.
- Adding an operation that needs `multipart/form-data` upload — wick's connector path is JSON-first; this is doable but uncommon, flag it.
- Adding `IsFilter` tags — never on your own initiative.

## Reference

- Canonical example: [`connectors/crudcrud/connector.go`](../../../connectors/crudcrud/connector.go) — 5 ops (4 read/write + 1 destructive), JSON validation helper, error wrapping, full happy-path + sad-path coverage.
- Full guide: <https://yogasw.github.io/wick/guide/connector-module>
- MCP transport + auth modes: <https://yogasw.github.io/wick/guide/mcp>
- Personal Access Tokens: <https://yogasw.github.io/wick/guide/access-tokens>
- OAuth 2.1 connections: <https://yogasw.github.io/wick/guide/oauth-connections>
