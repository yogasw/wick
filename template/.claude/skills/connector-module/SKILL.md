---
name: connector-module
description: Use for ANY work on a connector in this project — creating a new connector under connectors/, refactoring/improving an existing one, fixing bugs, adding new operations, editing the Configs or per-op Input structs, or wiring a connector up in main.go. Connectors are the third class of wick module beside Tools and Jobs, designed specifically to be consumed by LLM clients (Claude, Cursor, custom agents) over MCP. MANDATORY clarify+plan loop before writing any new connector or op — first reply is always a recommendation + plan, never files. For well-known APIs (Loki, Gmail, GitHub, Slack, Jira, Notion, Stripe, Datadog, …) explore on the user's behalf and propose a concrete recommended starter operation set with rationale, plus a translated `Configs` shape — don't ask the user to design the connector for you. For unknown / proprietary APIs, ask for sample requests + responses + auth scheme. Plan is presented as business flow (Scope, Configs, Operations, Inputs, auth/error-envelope, registration, open questions) — never as a per-file tree breakdown unless the user asks. Only after the user confirms does code get written. Enforces the full module contract — pkg/connector.{Meta, Module, Operation, Op, OpDestructive, ExecuteFunc, Ctx} — plus the wick:"..." tag grammar shared with Tools and Jobs (defers to tool-module skill for the full widget catalog), the destructive-opt-in model, the typed-response convention, the three-file split (connector.go / service.go / repo.go) mirroring tools, and the http.NewRequestWithContext rule that prevents goroutine leaks.
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

## Before writing any code — mandatory clarify + plan loop

**Hard rule: never start writing a new connector (or a new op on an existing one) in the same turn the user asks for it.** The first reply is always clarify + plan. Only after the user explicitly confirms ("ok" / "go ahead" / "proceed") do you touch any file. One-shot "build connector X" → "done, here are 4 files" is a failure mode — connectors are LLM-facing, the wrong `Configs` / `Input` / response shape costs more to fix than to gather upfront.

The loop is **three steps**, in order — same shape as the `tool-module` skill, adapted for the connector contract.

### Step 1 — Read the canonical example

Every new connector starts by reading [`connectors/crudcrud/`](../../../connectors/crudcrud/) end-to-end:

- `connectors/crudcrud/connector.go` — `Meta()`, `Configs`, per-op `Input` structs, `Operations()`, thin op handlers
- `connectors/crudcrud/service.go` — input validation, URL building, body checks (pure Go, no I/O)
- `connectors/crudcrud/repo.go` — `doRequest`: the **only** place `http.NewRequestWithContext` lives; non-2xx parsing; JSON decode
- `main.go` — how the connector is registered (`app.RegisterConnector(meta, configs, ops)`)

If the user's request doesn't obviously map onto this layout — e.g. multipart upload, custom auth dance, streaming response — **stop and ask before improvising**. Don't invent a new shape.

### Step 2 — Recommend first, then ask narrow clarifying questions

The goal of this step is to land a **business-flow proposal** the user can react to — not a blank checklist they have to fill in. Two modes depending on how well-known the upstream API is:

#### Mode A — Well-known API (Loki, Gmail, GitHub, Slack, Jira, Notion, Stripe, Datadog, …)

If you have working knowledge of the API — even from training data — **explore on the user's behalf and bring a concrete recommendation**. Don't pretend you don't know what Loki or Gmail does. The pattern:

1. **State what the API is for**, in one sentence (so the user can correct your mental model if it's wrong).
2. **Propose a recommended starter operation set** with rationale per op: which ones cover the most common LLM use cases, which ones are advanced/optional, which ones are destructive. Don't map the full upstream surface — reason about value per op and explain *why* each one belongs (or why you're skipping it).
3. **Propose the `Configs` shape** based on how the API typically authenticates and what's environment-variable in real deployments.
4. **Then ask** only the questions where there's a genuine ambiguity in the user's setup (single-tenant vs multi-tenant, instant vs range query, default time window, etc.).

Example for a Loki request:

> *"Loki is Grafana's log aggregator — LogQL queries against an HTTP API. For an LLM-facing connector I'd recommend starting with three ops:*
> - *`query_range` (read) — search logs across a time range. Covers ~80% of why an LLM would call Loki.*
> - *`list_labels` (read) — discover what labels exist on a stream, useful for the LLM to build smarter queries on follow-ups.*
> - *`label_values` (read) — given a label, list its values. Pairs with list_labels for query construction.*
>
> *All three are read-only, so plain `Op`, not `OpDestructive`. I'd skip `query` (instant) and `series` for v1 — they're rarely the right tool for an LLM.*
>
> *For `Configs` I'd default to `BaseURL` (url, required), `Token` (secret, required, Bearer auth). If you run multi-tenant Loki, add `OrgID` (X-Scope-OrgID header). Single-tenant most common — which one?*
>
> *Couple of things I can't infer: (a) typed `[]LogEntry{Timestamp, Labels, Line}` return vs raw Loki envelope — typed is friendlier for the LLM, fine? (b) default time window when the LLM doesn't pass `start`/`end` — last 1h or last 15m? (c) any default `limit` cap to protect against huge results?"*

This is what *"explore and recommend"* looks like. Bring opinions, with rationale; don't ask the user to design the connector for you.

#### Mode B — Unknown / proprietary / internal API

If you genuinely don't know the API (internal tooling, niche vendor, custom microservice), say so and ask the user to share what you'd otherwise look up:

1. **Sample request + response per op they want** — real cURL or HTTP examples.
2. **Auth scheme** — header name, value format.
3. **Error envelope** — happy path AND a typical error.

Don't fabricate ops or field names. For Mode B the questions list below stays mostly intact.

#### Translate every value into a concrete field, then ask back

Either mode — **translate every piece of info into a concrete config-field or input-field proposal so the user can correct you before code lands.** Don't wait for the user to spell out every field — propose the shape, then ask back.

#### How to classify a value

Any time a new value comes up — whether the user mentions it, the upstream docs imply it, or you spot it in a sample — classify it before going further:

| If the value... | It goes in... | Default widget |
|-----------------|---------------|----------------|
| Identifies the deployment / account / tenant / org / workspace and is stable across calls | `Configs` field | `text` (or `required`) |
| Is a credential, secret, or token | `Configs` field | `secret;required` (masked) |
| Is an endpoint, webhook target, callback URL, or base path | `Configs` field | `url` |
| Is a fixed enum (region, environment, mode, channel-type) | `Configs` field | `dropdown=a\|b\|c` |
| Is a numeric knob (timeout, page-size cap, retry count, polling interval) | `Configs` field | `number` (use `int`/`float`) |
| Is a feature toggle (enable cache, dry-run, follow redirects) | `Configs` field | `checkbox` (`bool`) |
| Varies per call from the LLM (resource id, query string, message body, target user) | per-op `Input` struct | same tag grammar |
| Is a constant the upstream API needs and never changes per environment | hardcoded in `repo.go` | n/a |

The bullet list below is **a starter set of common patterns, not the full menu** — apply the table above to anything new (e.g. `userId`, `webhookSecret`, `slackBotToken`, `defaultChannel`, `accountSid`, `pollIntervalSeconds`, `pageSizeCap`). When you spot something that fits the table, propose it back as a concrete field with `wick:"..."` tags filled in.

> *"Before I write anything, please confirm a few things so the shape lands right:*
> 1. *Endpoint + auth — what's the base URL, what's the auth scheme (bearer token / basic auth / header API key / OAuth), and what scopes does it need? I'll translate every answer into `Configs` fields. Common patterns (not exhaustive — I'll classify anything else you mention using the same rules):*
>    - *Base URL / webhook target → `BaseURL string \`wick:"url;required;desc=..."\`` (widget `url`)*
>    - *Username + password → two fields; password gets `secret;required`*
>    - *Bearer token / API key / client secret → `Token string \`wick:"secret;required;desc=..."\`` (masked input)*
>    - *Tenant ID / workspace slug / account ID / org ID → `TenantID string \`wick:"required;desc=..."\``*
>    - *Fixed set of regions / environments / modes → `Region string \`wick:"dropdown=us|eu|ap;desc=..."\`` (dropdown)*
>    - *Numeric knobs (timeout, polling interval, page-size cap) → `int`/`float` field, `number` widget*
>    - *Feature flags (enable cache, dry-run) → `bool` field, `checkbox` widget*
> 2. *Operations — which actions should the LLM be able to call? For each: name, what it does, and whether it mutates state (delete / post / send / force-push → `OpDestructive`, defaults off on every new row).*
> 3. **One concrete sample request per op** — method, URL, headers, query params, body. A real cURL or HTTP example, not prose.*
> 4. **One concrete sample response per op** — happy path AND a typical error envelope. The error envelope shape decides how `repo.go` parses non-2xx (e.g. `{"error":{"message":"..."}}` vs `{"errors":[{"detail":"..."}]}`).*
> 5. *Output shape — typed struct (recommended) or passthrough JSON? Typed gives the LLM a stable shape even when upstream adds fields.*
> 6. *Pagination / rate-limit / retry quirks — does an op need a cursor / page input? Should rate-limit headers surface as errors?*
> 7. *Default tags — group-only (default, every approved user sees it) or restricted by a filter tag?"*

#### Volunteer the recommendation, then verify

When something *looks* like it belongs in Configs but the user hasn't said so, **propose it explicitly and ask back** — don't silently add it, and don't silently leave it out. Pattern:

> *"I noticed the curl uses `X-Account-Id: abc123` — I'm planning to make `AccountID` a required Configs field so admins can set it per row. Sound right? Anything else in this auth flow I should pull out (e.g. an OAuth client secret, a default workspace, a region selector)?"*

If a value could plausibly be either `Configs` or per-op `Input`, name the trade-off and ask:

> *"`channel` could go either way — if every row of this connector targets one fixed channel, it's a Configs field; if the LLM should pick the channel per call, it's an Input on the `send_message` op. Which fits your case?"*

#### Smell-test rule when the user gives values inline

Any value that varies per environment (URL, token, tenant, region, account ID, polling interval, page-size cap) belongs in `Configs`, not hardcoded. If the user says "just use the URL `https://api.foo.com`", confirm: *"I'll make this a `BaseURL` field on `Configs` (seed value `https://api.foo.com`, admins can change per row without a redeploy) — sound right?"* Don't hardcode silently.

Same pattern for per-call values (resource id, query string, message body) → those go into the per-op `Input` struct, not `Configs`.

### Step 3 — Present the business-flow plan, wait for explicit confirm

After clarifications land, write a short plan back to the user **before** touching files. **The plan describes the connector's business flow — not its file tree.** Mechanical scaffolding (`connector.go does X`, `service.go does Y`, `repo.go does Z`) is internal-implementation noise the user doesn't need to read; the three-file split is fixed by this skill, mention it once at the bottom or skip entirely. Only spell out file responsibilities if the user explicitly asks how the code is structured, or if you're departing from the standard split for a reason that needs sign-off.

**All operations recommended in Step 2 must appear in the plan — don't silently narrow the op set between clarification and plan.** If you proposed 3 ops in Step 2, the plan lists all 3.

A good plan covers, in this order:

- **Scope** — connector key, name, one-sentence description, icon, default tags
- **`Configs` struct** — the exact Go struct with every `wick:"..."` tag spelled out (widget, `required`, `secret`, `dropdown`, `desc`). Inline-comment the fields that are still pending an answer. This is the one place Go struct syntax belongs in the plan — admins need to confirm the field names and widgets before they're baked into the DB schema.
- **Operations** — per op: key, display name, `Op` vs `OpDestructive`, the LLM-facing `Description` you plan to use, the typed return shape, and a short note on what each one is good for
- **Per-op inputs** — listed under each operation (not a separate section): one line per field as `field_name (type, widget) — description`. No Go struct code — the user is confirming the *field names and their meaning*, not the implementation syntax.
- **Auth + error-envelope behavior** — one sentence: which header carries auth, what the error envelope looks like that `repo.go` will parse. This is the part of the implementation that affects the user's contract; surface it. Don't expand into a per-file breakdown.
- **Open questions** — anything still unclear, in a numbered list the user can answer with one short message.

Skip these from the plan unless asked: per-file responsibility ("`service.go` validates inputs"), folder tree, generic golden-rule restatements, anti-patterns, Registration snippet (the skill generates this automatically — no Go import/call code in the plan). The user already trusts the skill to follow the file split — don't repeat it back at them.

Then **stop** and wait for the user to say "ok" / "go ahead" / "proceed" / equivalent. Silence means wait, not proceed. Never jump straight to writing files — the user has explicitly required a recommendation + confirmation step before any code is generated.

This catches "no, the API actually needs both `app_id` and `tenant_id`", "delete is reversible for 30 days, treat it as `Op` not `OpDestructive`", "actually region should be a free-text string not dropdown — they keep adding new regions" while the cost is still one chat round, not a refactor.

**Exception — trivial edits.** Typo in a `desc=...`, op-description copy tweak, renaming a local variable, fixing an obvious bug with no shape change → skip the plan step and just do it. The clarify+plan loop fires when files get added, signatures change, configs get added/removed, or ops get added/removed/marked destructive. When unsure whether the edit is trivial, default to asking.

When auditing an existing connector as part of a change: same loop. Ask before adding/removing an op, flipping `Op` ↔ `OpDestructive`, or changing the shape of `Configs` / `Input`.

## Module layout

The default layout is a three-file split — same shape as tools (`handler.go` / `service.go` / `repo.go`):

```
connectors/myconn/
  connector.go    # Meta + Configs + per-op Input structs + Operations() + thin op handlers
  service.go      # pure Go — input validation, URL/body construction, response shaping
  repo.go         # outbound I/O — HTTP calls, DB, S3 (everything that touches the network)
```

The shipped reference at [`connectors/crudcrud/`](../../../connectors/crudcrud/) follows exactly this split. Mirror it.

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

**Tag grammar is shared with Tools and Jobs.** The full widget catalog (every `wick:"..."` flag, the type → widget table, key derivation rules) lives in the [`tool-module`](../tool-module/SKILL.md) skill under "Widget catalog" — don't duplicate it here, defer to it. The most common flags you'll reach for in connectors:

- `required` — admin must fill before any op can run
- `secret` — masked input (passwords, API keys, bearer tokens, OAuth client secrets)
- `url` — URL input widget (base URLs, webhook targets)
- `dropdown=a|b|c` — restricted choice (region, environment, mode)
- `desc=...` — help text shown next to the field; the only hint the admin sees
- `key=custom_name` — override the auto snake_cased column name

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

### Op handlers — three-file example

The handler in `connector.go` is a dispatch skeleton — five lines max: validate, dispatch, return. Validation logic lives in `service.go`; HTTP lives in `repo.go`.

```go
// connector.go — thin handler only
func listRepos(c *connector.Ctx) (any, error) {
    params, err := validateListRepos(c)
    if err != nil {
        return nil, err
    }
    return fetchRepos(c, params)
}
```

```go
// service.go — pure-Go validation and URL construction; no network, unit-testable
type listReposParams struct {
    BaseURL string
    Org     string
    PerPage int
}

func validateListRepos(c *connector.Ctx) (listReposParams, error) {
    org := strings.TrimSpace(c.Input("org"))
    if org == "" {
        return listReposParams{}, errors.New("org is required")
    }
    perPage := c.InputInt("per_page")
    if perPage <= 0 || perPage > 100 {
        perPage = 30
    }
    return listReposParams{
        BaseURL: strings.TrimRight(c.Cfg("base_url"), "/"),
        Org:     org,
        PerPage: perPage,
    }, nil
}
```

```go
// repo.go — the ONLY file that calls http.NewRequestWithContext
func fetchRepos(c *connector.Ctx, p listReposParams) (any, error) {
    url := fmt.Sprintf("%s/orgs/%s/repos?per_page=%d", p.BaseURL, p.Org, p.PerPage)
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

See [`connectors/crudcrud/`](../../../connectors/crudcrud/) for the full reference implementation of all three files together.

### Golden rules

1. **`repo.go` MUST** use `http.NewRequestWithContext(c.Context(), ...)` for every outbound call — never plain `http.NewRequest`. Without the context, MCP cancellations (client disconnect, deadline) cannot abort the upstream request and the goroutine leaks until the upstream responds. Concentrating all HTTP in `repo.go` makes this rule trivial to audit.
2. **`service.go` MUST** validate inputs (presence, sane bounds) before dispatching to `repo.go`. Errors bubble up as `connector_runs.error_msg` on the history page. Keeping validation in `service.go` makes it unit-testable without network fixtures.
3. **MUST** read configs via `c.Cfg(...)` / inputs via `c.Input(...)` at call time — never from package-level vars set at init. Each connector row carries its own credential values; reading from a singleton breaks the multi-row model.
4. **MUST** use `c.HTTP` as the HTTP client (carries a 30s default timeout). Replace transport locally only when you have a documented reason.
5. **SHOULD** wrap upstream errors with `fmt.Errorf("...: %w", err)` so the chain reads cleanly in the history detail panel.
6. **SHOULD** transform the upstream response into a typed struct that's stable across upstream changes. Raw body passthrough exposes LLMs to pagination cursors, debug fields, and envelope noise that breaks on shape changes.
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

- Canonical example: [`connectors/crudcrud/`](../../../connectors/crudcrud/) — 5 ops (4 read/write + 1 destructive) split across `connector.go` (handlers), `service.go` (JSON validation, URL builder), `repo.go` (HTTP call + error wrapping).
- Full guide: <https://yogasw.github.io/wick/guide/connector-module>
- MCP transport + auth modes: <https://yogasw.github.io/wick/guide/mcp>
- Personal Access Tokens: <https://yogasw.github.io/wick/guide/access-tokens>
- OAuth 2.1 connections: <https://yogasw.github.io/wick/guide/oauth-connections>
