---
name: connector-module
description: Use for ANY work on a connector in the wick core repo — creating a new connector under internal/connectors/, refactoring/improving an existing one, fixing bugs, adding operations, editing the Configs or per-op Input structs, or touching anything that affects the MCP surface (internal/mcp/) or the registry (internal/connectors/registry.go). Covers the full module contract — pkg/connector.{Meta, Module, Operation, Op, OpDestructive, ExecuteFunc, Ctx} — plus the wick:"..." tag grammar shared with Tools and Jobs, the destructive-opt-in model, the typed-response convention, the http.NewRequestWithContext rule that prevents goroutine leaks, and the Bootstrap auto-seed contract. Also mandates the "ask before adding ops" question whenever a new connector is requested.
allowed-tools: Read, Grep, Glob, Edit, Write, Bash
paths:
  - "internal/connectors/**"
  - "internal/mcp/**"
  - "internal/jobs/connector-runs-purge/**"
  - "pkg/connector/**"
  - "internal/docs/connectors-design.md"
  - "AGENTS.md"
---

# Connector Module — wick core (upstream)

> **Scope:** this skill is for connectors that ship inside **wick itself** — modules under `internal/connectors/` registered from `internal/connectors/registry.go::RegisterBuiltins()`. If you are building a connector in a downstream project (a scaffold generated from `template/`), use the downstream skill at `template/.claude/skills/connector-module/SKILL.md` — paths, registration site, and verification differ.

Activate this skill whenever the user touches a connector in the wick lab binary or the connector framework itself — creating, improving, fixing, refactoring, or adding operations. When editing an existing connector, audit it against the rules below and bring it up to spec as part of the change.

## Mental model

A connector wraps **one external API** for LLM consumption.

- One `Module` carries one shared `Configs` struct (URL, token, ...) and **N `Operations`** (`query`, `list_repos`, `create_issue`, ...).
- Each `Operation` has its own typed `Input` struct (turned into the MCP JSON Schema) and its own `ExecuteFunc`.
- An admin can create many rows per definition at runtime; each row carries its own credential values, label, and tags. Same Go code, different rows = different (env, team, account).
- LLMs do not see N×M static tools. The MCP server exposes a fixed meta surface (`wick_list`, `wick_search`, `wick_get`, `wick_execute`); each (row × operation) pair is addressed by an opaque `tool_id` of the form `conn:{connector_id}/{op_key}`.

The full design lives in [`internal/docs/connectors-design.md`](../../../internal/docs/connectors-design.md). This skill is the operational summary; that document is the source of truth for any architectural question.

## Applies to (non-exhaustive triggers)

- "Add a new built-in connector for X"
- "Add operation Y to internal connector X"
- "Refactor internal/connectors/X"
- "Fix bug in MCP dispatch / connector ctx / connector runs"
- Any edit under `internal/connectors/{name}/`, `pkg/connector/`, `internal/mcp/`
- Changes to `internal/connectors/registry.go::RegisterBuiltins`

## Before building: ALWAYS gather the contract

Whenever the user asks for a **new** connector — or for a new operation on an existing one — STOP and ask before writing code. Connectors are LLM-facing; the wrong input/output shape costs more to fix than to gather upfront.

The very first reply back is a short numbered checklist asking the user (or the upstream docs) for:

> *"Before I write anything, please share these so the shape is right:*
> 1. *Endpoint + auth — base URL, auth scheme (bearer / basic / header key / OAuth), required scopes. These become `Configs` fields.*
> 2. *Operations — which actions should the LLM call? For each: name, what it does, and whether it mutates state (delete, post, send → `OpDestructive`, defaults off on every new row).*
> 3. **One concrete sample request per operation** — method, URL, headers, query params, body. A real cURL or HTTP example, not prose.*
> 4. **One concrete sample response per operation** — both happy path and a typical error envelope. The error shape decides how `repo.go` parses non-2xx.*
> 5. *Output shape — typed struct (recommended) or passthrough JSON? Typed gives the LLM a stable shape independent of upstream cosmetics.*
> 6. *Pagination / rate-limit / retry quirks — anything an op needs to surface as input or error.*
> 7. *Default tags — group-only (default, every approved user sees it) or restricted by filter tag?"*

Then **explain back** what you'll build: list the ops you're going to write, the `Configs` fields you'll declare, and the typed return shapes — and wait for confirmation before generating code. This catches "no, the API actually needs both `app_id` and `tenant_id`" or "delete is reversible for 30 days, treat it as `Op` not `OpDestructive`" while it's still cheap.

Don't silently invent operations, skip the destructive flag, or guess request/response shapes from a description.

## Module layout

The default layout is a three-file split — same shape as tools (`handler.go` / `service.go` / `repo.go`):

```
internal/connectors/myconn/
  connector.go    # Meta + Configs + per-op Input structs + Operations() + thin op handlers
  service.go      # pure Go — input validation, URL/body construction, response shaping
  repo.go         # outbound I/O — HTTP calls, DB, S3 (everything that touches the network)
  doc.go          # optional — package-level godoc when connector.go gets crowded
```

The shipped reference at [`internal/connectors/crudcrud/`](../../../internal/connectors/crudcrud/) follows exactly this split. Mirror it.

- **`connector.go`** stays scannable — a reader sees Meta, Configs, every Input struct, every Operation description, and the dispatch outline of every handler without scrolling past validation logic or HTTP wiring.
- **`service.go`** is unit-testable in isolation. Validators and URL builders take a `*connector.Ctx` (or plain values) and return data — no network, no fixtures, fast tests.
- **`repo.go`** is where the goroutine-leak rule lives. Concentrating every `http.NewRequestWithContext` in one file makes the rule trivial to enforce and audit.

Handlers in `connector.go` should read like five lines: validate via `service.go`, dispatch via `repo.go`, return. Anything past "parse, validate, dispatch" goes in `service.go`. Trivial single-op connectors (no validation, one HTTP call) MAY collapse into a single `connector.go`, but default to splitting.

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

- `Key` is a unique slug across every connector (kebab-case allowed; snake_case discouraged for op keys but allowed for connector keys).
- `Description` is shown to admins. The LLM reads per-`Operation` Description, not this.

### `Configs` struct

Per-instance credentials and endpoints, shared across every operation. Admins fill these in at `/manager/connectors/{Key}/{id}` after wick auto-seeds the first row at boot.

```go
type Configs struct {
    BaseURL string `wick:"url;required;desc=GitHub API base URL. Default: https://api.github.com"`
    Token   string `wick:"secret;required;desc=Personal access token with the scopes you intend to use."`
}
```

Read `SKILL.md` from the `config-tags` folder (sibling of this skill's folder) for the full tag reference.

**Fields tagged `secret` opt into the encrypted-fields layer** — wick auto-decrypts incoming `wick_enc_` tokens before `ExecuteFunc` runs and auto-masks the plaintext in the response back to the LLM. Use it for anything the LLM should pass forward without ever learning the value: tokens, API keys, session credentials. See `SKILL.md` from the `encrypted-fields` folder for the full contract (manual `c.Mask` / `c.MaskIgnoreCase` for dynamic API responses, `wick_enc_` token format, per-user keys, MCP redirect tools).

**Read at runtime via `c.Cfg("base_url")`, `c.CfgInt("port")`, `c.CfgBool("use_tls")`** — keys are the snake_cased field name unless overridden with `key=`. Reads always return plaintext; the encrypted-fields layer happens around `ExecuteFunc`, not inside it.

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

A destructive op is **disabled by default** on every new row. Admins opt in per (row, operation).

### Description discipline

`Operation.Description` is the **load-bearing** signal the LLM uses to decide whether to call this op. It shows up verbatim in `wick_list` / `wick_search` payloads.

Write action verbs and be specific about input/output shape:

- ✅ "Search Loki using LogQL. Returns log lines with timestamp + labels. Empty result = empty array."
- ✅ "Send a Slack message to {channel}. Returns the posted message timestamp on success. Idempotent if {client_msg_id} is supplied."
- ❌ "query loki"
- ❌ "send slack"

### `ExecuteFunc`

See [`internal/connectors/crudcrud/`](../../../internal/connectors/crudcrud/) for the canonical reference — five operations split across `connector.go` (handlers), `service.go` (validation, URL building, JSON body checks), `repo.go` (HTTP). One op is destructive; error wrapping and happy-path + sad-path coverage are in `repo.go::doRequest`.

### Golden rules for `ExecuteFunc`

1. **MUST** build requests with `http.NewRequestWithContext(c.Context(), ...)` — never plain `http.NewRequest`. Without this, MCP cancellations (client disconnect, deadline, SSE per-call timeout in `internal/mcp`) cannot abort the upstream request and the goroutine leaks for the duration of whatever the upstream eventually returns. This is documented on `pkg/connector/Ctx.Context` and is the single most common bug in custom connectors.
2. **MUST** validate `Input` early before doing the HTTP call. Errors returned here become `connector_runs.error_msg`.
3. **MUST** read configs via `c.Cfg(...)` / inputs via `c.Input(...)`. Never via process-level singletons or env vars at call time — that breaks the multi-row model.
4. **MUST** use `c.HTTP` as the starting client (carries a 30s default timeout from `pkg/connector.DefaultHTTPTimeout`). Replace it locally if you need a different transport, but document why.
5. **SHOULD** wrap upstream errors with `fmt.Errorf("...: %w", err)` so the chain reads cleanly in the history detail panel.
6. **SHOULD** transform the upstream response into a typed struct or map shape that's stable across upstream changes. Returning the raw upstream body works but means LLMs see noise (envelopes, pagination cursors, debug fields) and break when upstream tweaks the shape. See [`connectors-design.md` § 10.1](../../../internal/docs/connectors-design.md).
7. **SHOULD** mark destructive operations with `OpDestructive(...)` — the framework defaults the toggle off so it's an explicit admin opt-in.
8. **MAY** emit progress with `c.ReportProgress(progress, total, message)` for long-running calls. Safe to call from any goroutine; no-op on the JSON transport.

### Anti-patterns

- ❌ `http.NewRequest` (no context) — goroutine leak.
- ❌ Returning `*http.Response.Body` reader directly — the framework cannot marshal it. Always `io.ReadAll` + decode.
- ❌ Storing config values in package-level vars set at init — connectors are multi-row, every call must read fresh from `c.Cfg(...)`.
- ❌ Op keys with hyphens or spaces — slug only (`a-z0-9_`).
- ❌ Sharing mutable state across `Execute` invocations — connector calls are concurrent.
- ❌ Polling tight loops inside `Execute` without honoring `c.Context().Done()`.

## Registration

Built-in wick connectors are appended in `internal/connectors/registry.go::RegisterBuiltins()`:

```go
func RegisterBuiltins() {
    extra = append(extra,
        connector.Module{
            Meta:       crudcrud.Meta(),
            Configs:    entity.StructToConfigs(crudcrud.Configs{}),
            Operations: crudcrud.Operations(),
        },
        connector.Module{
            Meta:       myconn.Meta(),
            Configs:    entity.StructToConfigs(myconn.Configs{}),
            Operations: myconn.Operations(),
            OAuth:      myconn.OAuthMeta(), // optional — see OAuth section below
        },
    )
}
```

Downstream projects use `app.RegisterConnector(meta, configs, ops)` from `main.go` instead — that's the path the downstream skill covers.

## OAuth (user token acquisition)

Connectors that need per-user OAuth tokens (e.g., Slack user token `xoxp-`, Google OAuth, GitHub app) can opt into wick's generic OAuth framework by setting `Module.OAuth`.

### When to use

Use `Module.OAuth` when:
- Your connector needs a **user identity token** (not a shared service account token).
- The user must explicitly grant permission via an OAuth consent page.
- Examples: Slack DM-as-user, Google Drive per-user, GitHub app installations.

Do **not** use for bot/service tokens pasted by admins — those go in `Configs` as a `secret`-tagged field.

### How to add OAuth to a new connector

**Step 1 — Create `internal/connectors/myconn/oauth.go`:**

```go
package myconn

import (
    "context"
    "github.com/yogasw/wick/pkg/connector"
)

// OAuthMeta returns the OAuth configuration for MyConn.
func OAuthMeta() *connector.OAuthMeta {
    return &connector.OAuthMeta{
        // AuthorizeURL is the provider's consent page.
        AuthorizeURL: "https://myservice.com/oauth/authorize",
        // Scopes is the comma or space-separated list of requested user scopes.
        Scopes: "read:user,write:messages",
        // DisplayName appears on the "Connect" button in the UI.
        DisplayName: "MyService",
        // Icon is an inline SVG or emoji rendered on the Connect button.
        Icon: "🔗",
        // GetUserIdentity is called after the token exchange to resolve
        // who the token belongs to. Return a stable unique ID (not display name)
        // as userID — it is used to route tokens to the right connector row.
        GetUserIdentity: func(ctx context.Context, accessToken string) (userID, displayName string, err error) {
            // Call the provider's "who am I" endpoint with the new token.
            // Example: GET https://myservice.com/api/me
            // return resp.UserID, resp.Username, nil
        },
    }
}
```

**Step 2 — Register with `OAuth` field in `registry.go`:**

```go
connector.Module{
    Meta:       myconn.Meta(),
    Configs:    entity.StructToConfigs(myconn.Configs{}),
    Operations: myconn.Operations(),
    OAuth:      myconn.OAuthMeta(), // ← this is all that's needed
},
```

**That's it.** The manager UI and OAuth handler pick up everything automatically:

| What appears automatically | Where |
|---|---|
| "OAuth App" section (Client ID + Client Secret fields) | `/manager/connectors/myconn` list page (admin-only) |
| "Connect with MyService" button | `/manager/connectors/myconn/{id}` detail page (any user with access) |
| OAuth start + callback routes | `GET /manager/connectors/myconn/oauth/start` and `.../callback` |
| Token saved to connector row on success | Automatic via `oauthSaveToken` in `internal/manager/oauth.go` |
| In-memory token map refresh | Automatic via `RefreshTokenMap` on the Slack channel |

**Step 3 — Add Redirect URI to provider app settings:**

```
http://localhost:9425/manager/connectors/myconn/oauth/callback
```

Replace `myconn` with `Module.Meta.Key` and `localhost:9425` with the production `app_url` from wick settings.

### How the framework works (internals)

```
Admin sets Client ID + Secret
  → stored in configs table with owner "connector_oauth:{key}"

User clicks "Connect with MyService"
  → GET /manager/connectors/{key}/oauth/start?connector_id={rowID}
  → reads OAuthMeta.AuthorizeURL + Scopes
  → generates HMAC-signed state token (stored with 10-min TTL)
  → redirects to provider consent page

Provider redirects back
  → GET /manager/connectors/{key}/oauth/callback?code=...&state=...
  → validates state token
  → exchanges code via standard POST to provider token endpoint
  → calls OAuthMeta.GetUserIdentity(token) → (userID, displayName)
  → saves token to connector row (updates existing row or creates new one)
  → refreshes in-memory token map immediately
  → redirects back to /manager/connectors/{key}/{rowID}?oauth=success
```

### Reference files

| File | Role |
|---|---|
| `pkg/connector/oauth.go` | `OAuthMeta` struct + `OAuthProvider` interface definition |
| `internal/manager/oauth.go` | Generic handler (start, callback, state management, token save) |
| `internal/connectors/slack/oauth.go` | Slack reference implementation of `OAuthMeta` |
| `internal/manager/connectors.go` | How `mod.OAuth != nil` drives the UI sections |

### Caveats

- The generic callback uses `slackgo.GetOAuthV2ResponseContext` — currently Slack-specific. If your provider uses a different token exchange endpoint, update `internal/manager/oauth.go::oauthCallback` to detect by connector key or add a `TokenURL` field to `OAuthMeta`.
- `GetUserIdentity` is called with the **access token**, not the refresh token. Store the access token in the connector row; add a `RefreshToken` field to `Configs` if your provider issues long-lived refresh tokens.
- The stored token is written to the connector row's `user_token` config key. Your `Configs` struct must have a `UserToken string` field tagged `wick:"secret;..."` for the admin UI to show it (and for `c.Cfg("user_token")` to work in operations).

## Bootstrap

Documented in `connectors-design.md` § 5.4. Highlights:

- First boot per `Meta.Key` with `CountByKey(key) == 0` → auto-create one empty row (`Label = Meta.Name`, `Configs = "{}"`).
- Existing rows are never overwritten on subsequent boots.
- Two registrations with the same `Key` are a fatal boot error.
- A registered `Key` whose module has been removed is tolerated: row stays, marked "Module not registered" in the admin UI; calls return an error.

## MCP surface — what to know when editing

The MCP layer (`internal/mcp/`) exposes a fixed **meta-tool** set: `wick_list`, `wick_search`, `wick_get`, `wick_execute`. Connector instances are not advertised as N×M static tools. That choice is deliberate (see `connectors-design.md` § 7.3) — adding or removing a connector row never invalidates the LLM client's cached tool list.

When editing the MCP layer:

- `wick_execute` resolves `tool_id` of the form `conn:{connector_id}/{op_key}` back to a single `Service.Execute` call.
- Auth check (`IsVisibleTo(connectorID, tagIDs, isAdmin)`) and operation enable state are re-validated on every `wick_execute` and `wick_get` — never trust list-time caching.
- Streamable HTTP (Accept: text/event-stream) is supported for progress events. JSON transport is the default.
- Auth modes (PAT, OAuth access, OAuth refresh) are dispatched in `internal/mcp/auth.go` by token prefix (`wick_pat_`, `wick_oat_`, `wick_ort_`).

## Connector runs (audit trail)

Every `Execute` call (MCP, panel-test, retry) writes a row to `connector_runs`. See `connectors-design.md` § 5.3 for the full schema.

The retention job [`internal/jobs/connector-runs-purge`](../../../internal/jobs/connector-runs-purge/handler.go) is the in-tree example of a System-tagged auto-enabled job. When touching that job:

- It is registered from BOTH `internal/pkg/worker/server.go` (for the scheduler tick) and `internal/pkg/api/server.go` (so `/admin/jobs` and `/manager/jobs` see the row). Skipping either breaks the surface.
- It carries `tags.System` with `IsSystem=true; IsFilter=true; IsGroup=true` — protected by access-control mechanics in `connectors-design.md` § 9.8.
- `Meta.AutoEnable=true` causes `manager.Service.Bootstrap` to call `repo.ForceEnable` on every restart so the job is always live.

## Verifying your work

1. **Compile:**
   ```bash
   go build ./...
   ```
2. **Templ + Tailwind regen** (only if you touched `internal/connectors`'s manager UI under `internal/manager/...`):
   ```bash
   templ generate
   ./bin/tailwindcss* -i web/src/input.css -o web/public/css/app.css --minify
   ```
3. **Boot the lab binary, smoke-test, kill the port** (per the AGENTS.md one-shot flow):
   ```bash
   go run main.go server &
   # exercise the connector via /manager/connectors/{key}/{id}/test?op=...
   # then kill the port — don't leave the server running
   ```
4. **MCP smoke (optional):**
   - Generate a Personal Access Token at `/profile/tokens`.
   - Use cURL to hit `POST /mcp` with `Authorization: Bearer wick_pat_xxx` and `{"jsonrpc":"2.0","method":"tools/list","id":1}`. Expect the four `wick_*` tools.
   - Call `wick_list` to see the new connector's `tool_id` entries.

## When to ask before acting

- New connector that needs per-user OAuth tokens — ask: (a) does it use standard OAuth 2.0 authorization code flow? (b) what scopes are needed? (c) does the provider return a `user_id` from a "who am I" endpoint after token exchange? (d) are refresh tokens issued and how long do they live? If all yes → use `Module.OAuth` + `OAuthMeta`. If non-standard (e.g., device flow, PKCE-only, custom token endpoint) — confirm before building.
- **Removing an existing connector or operation** — confirm: removing an op orphans `connector_operations` rows and breaks any active MCP client that listed the old `tool_id`. Migration plan needs to land in the same change.
- Adding an operation that needs `multipart/form-data` upload — wick's connector path is JSON-first; this is doable but uncommon, flag it.
- Adding `IsFilter` tags — never on your own initiative.
- Touching `internal/mcp/` JSON-RPC dispatch — this is shared by every connector; pause and confirm scope before editing.
- Changing the `pkg/connector` public API — downstream projects depend on it; treat as a breaking change.

## Reference

- Canonical example: [`internal/connectors/crudcrud/connector.go`](../../../internal/connectors/crudcrud/connector.go)
- Design source of truth: [`internal/docs/connectors-design.md`](../../../internal/docs/connectors-design.md)
- Public API: [`pkg/connector/`](../../../pkg/connector/) — `Meta`, `Module`, `Operation`, `Op`, `OpDestructive`, `ExecuteFunc`, `Ctx`
- MCP server: [`internal/mcp/`](../../../internal/mcp/)
- Retention job: [`internal/jobs/connector-runs-purge/`](../../../internal/jobs/connector-runs-purge/)
