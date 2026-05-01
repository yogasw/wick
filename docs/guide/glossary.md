# Glossary

Quick lookup for terms used across wick docs. Listed in rough order from broadest to most specific.

## Module classes

**Tool** — a wick module designed for humans clicking a UI. Lives at `tools/<name>/`. Renders a templ + Tailwind page mounted at `/tools/{key}`. See [Tool Module](./tool-module).

**Job** — a wick module that runs on a cron schedule (or via "Run Now"). Lives at `jobs/<name>/`. Operator page at `/jobs/{key}`, admin page at `/manager/jobs/{key}`. See [Background Job](./job-module).

**Connector** — a wick module designed for LLM consumption via MCP. Lives at `connectors/<name>/`. Carries one shared `Configs` struct and N typed `Operations`. Admin manages rows at `/manager/connectors/{key}`. See [Connector Module](./connector-module).

## Connectors

**Module** (in connector context) — the Go package under `connectors/<name>/`. Carries `Meta()`, a `Configs` struct, per-op `Input` structs, and `Operations()`.

**Operation** — a single LLM-callable action declared in a connector module — `query`, `list_repos`, `create_issue`. Each operation has its own typed `Input` struct + `ExecuteFunc`.

**Destructive operation** — an operation marked with `connector.OpDestructive(...)`. Mutates state in a hard-to-undo way (DELETE, send-message, post-comment). The framework defaults the per-row toggle off so admins must opt in explicitly.

**Connector row** (or **connector instance**) — one row in the `connectors` table. Pairs a connector definition (by `Key`) with a label, credential values, tags, and an optional `Disabled` flag. An admin can spawn N rows per definition.

**ExecuteFunc** — the per-operation handler function. Signature `func(c *connector.Ctx) (any, error)`. Returns a Go value that wick marshals into the MCP `tools/call` response.

**Ctx** — the per-call handle wick passes to `ExecuteFunc`. Carries the resolved per-row credential map, the per-call input arguments, an `*http.Client`, a `context.Context`, and a progress reporter. Read configs via `c.Cfg(...)`, inputs via `c.Input(...)`.

**Connector run** — one execution of an operation. Recorded as a row in `connector_runs` with input, response, latency, status, IP, user-agent, and (for retries) `parent_run_id`. Source is `mcp`, `test`, or `retry`.

## Tags

**Tag** — a label that can be attached to module rows (Tool, Job, Connector) and to users. Stored in the `tags` table with three orthogonal flags: `IsGroup`, `IsFilter`, `IsSystem`.

**Group tag** — a tag with `IsGroup=true`. Used to bucket modules visually on the home grid (e.g. "Text", "Messaging", "JSON"). Does not gate access.

**Filter tag** — a tag with `IsFilter=true`. Gates access: a row with ≥1 filter tag is visible only to users who carry at least one matching tag. Admins bypass.

**System tag** — a tag with `IsSystem=true`. Owned by the wick source tree, not the database. Cannot be edited or detached via the UI. Used by code-managed entities like the `connector-runs-purge` job.

## MCP

**MCP** — [Model Context Protocol](https://modelcontextprotocol.io). The standard wick uses to expose connectors to LLM clients. JSON-RPC 2.0 over HTTP, with optional Streamable HTTP for progress events.

**Meta-tool** — one of the four fixed tools wick advertises in its MCP `tools/list` response: `wick_list`, `wick_search`, `wick_get`, `wick_execute`. The LLM uses these to discover and call connector operations dynamically.

**Tool ID** — opaque identifier of the form `conn:{connector_id}/{op_key}`. Stable across admin label renames; addresses one (row × operation) pair.

**Streamable HTTP** — the MCP transport wick uses. Endpoint `POST /mcp` accepts both JSON (default) and SSE (`Accept: text/event-stream`) responses on the same path.

**`Mcp-Session-Id`** — header generated on the first `initialize` call, held in-memory only. Auth (PAT or OAuth) is the load-bearing identity binding.

## Auth

**PAT** (Personal Access Token) — static bearer token a user generates at `/profile/tokens`. Format `wick_pat_<32hex>`. Stored as SHA-256 hash. For clients that cannot speak OAuth. See [Access Tokens](./access-tokens).

**OAuth grant** — one (user × OAuth client) pair holding active access + refresh tokens. Access `wick_oat_<32hex>` (1h TTL), refresh `wick_ort_<64hex>` (30d TTL). See [OAuth Connections](./oauth-connections).

**DCR** (Dynamic Client Registration, RFC 7591) — the OAuth flow wick uses to onboard MCP clients without pre-shared secrets. Client `POST`s `/oauth/register` and gets back a `client_id`.

**PKCE** (Proof Key for Code Exchange) — OAuth security extension that ties an authorization code to the requesting client without a client secret. Wick mandates `code_challenge_method=S256` per OAuth 2.1.

**Refresh rotation** — every successful refresh-token exchange mints a fresh refresh token and revokes the old one. Replaying a rotated refresh token revokes the entire chain (replay detection).

## Access surfaces

**`/profile/tokens`** — user manages own PATs.

**`/profile/connections`** — user manages own OAuth grants (Connected Apps).

**`/profile/mcp`** — install snippets for Claude.ai, Claude Desktop, Cursor, VSCode, cURL.

**`/admin/connectors`** — admin cross-user view of connector rows.

**`/admin/access-tokens`** — admin cross-user view of PATs.

**`/admin/connections`** — admin cross-user view of OAuth grants.

## Maintenance

**Bootstrap** — wick's startup phase that registers built-ins, creates default rows for new connector definitions, syncs system tags, and force-enables auto-enabled jobs. Idempotent on subsequent boots.

**`connector-runs-purge`** — built-in System-tagged job that trims old `connector_runs` rows daily. Default 7-day retention. See [Connector Runs Purge](./connector-runs-purge).
