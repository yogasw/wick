# MCP for LLMs

Wick speaks the [Model Context Protocol](https://modelcontextprotocol.io) so any MCP-aware LLM client — Claude.ai, Claude Desktop, Cursor, custom agents — can call your [connectors](./connector-module) with structured input and output. There is no glue code on your side: register a connector, generate a token, paste a URL.

## Why MCP

Traditional REST integrations require the LLM (or the prompt author) to know URLs, headers, and response shapes upfront. MCP standardizes the discovery + invocation handshake so the LLM negotiates capabilities at runtime: the server advertises a tool list and JSON Schema; the client picks a tool, supplies typed arguments, and receives a typed response. Auth is bearer-token only — no per-request signing dance.

Wick layers two more wins on top:

- **Per-call audit** — every invocation writes a row to `connector_runs` with input, output, latency, IP, and user. See the [history page on a connector row](./connector-module#history-page).
- **Tag-filtered visibility** — a user only sees connector rows whose tags match. Sharing a row across teammates is `/admin/users` + `/admin/connectors` work, not code.

## Endpoint

```
POST /mcp                                   JSON-RPC 2.0 over Streamable HTTP
GET  /.well-known/oauth-protected-resource  RFC 9728 metadata
GET  /.well-known/oauth-authorization-server RFC 8414 metadata
```

`POST /mcp` accepts both JSON and Streamable HTTP responses:

| Request `Accept` | Response | Use case |
|------------------|----------|----------|
| `application/json` (default) | Single JSON-RPC frame | 90% of tool calls |
| `text/event-stream` | SSE — `notifications/progress` frames + final result | Long-running ops with progress events |

Heartbeat `:keepalive` frames every 15 seconds keep reverse proxies from reaping idle SSE connections.

## Meta-tool pattern

Wick does **not** advertise N×M static tools (one entry per connector × operation). It advertises four fixed tools:

| Tool | Annotation | Purpose |
|------|------------|---------|
| `wick_list` | `readOnlyHint` | List every (row × op) visible to the caller — without `input_schema` |
| `wick_search` | `readOnlyHint` | Substring search over label, name, description |
| `wick_get` | `readOnlyHint` | Fetch full detail for one `tool_id`, including `input_schema` |
| `wick_execute` | `destructiveHint` | Run an operation by `tool_id` + `params` |

Why not a static list?

- **Dynamic instances** — adding or removing a connector row in the admin UI must not invalidate the LLM client's cached tool list. With four fixed tools, the cache is always valid.
- **Token economy** — `wick_list` does not return per-op `input_schema`. The LLM only pays the schema cost when it commits to calling a specific op via `wick_get`.
- **Scale** — a server with hundreds of connectors still has a four-entry `tools/list` response.

### Tool ID format

```
conn:{connector_id}/{op_key}
```

UUID-based, opaque, stable across admin label renames. The `conn:` prefix is reserved for future module classes (e.g. `prompt:` for prompt templates).

### Typical LLM flow

```mermaid
sequenceDiagram
    autonumber
    participant LLM as Claude.ai / LLM client
    participant MCP as wick /mcp
    participant Auth as wick auth
    participant Op as connector ExecuteFunc
    participant API as upstream API

    LLM->>MCP: tools/list
    MCP-->>LLM: [wick_list, wick_search, wick_get, wick_execute]
    LLM->>MCP: wick_list
    MCP->>Auth: IsVisibleTo(user, tags, is_admin)
    Auth-->>MCP: filtered (row × op) set
    MCP-->>LLM: [{ tool_id, connector, name, description, destructive }]
    LLM->>MCP: wick_get({tool_id})
    MCP-->>LLM: input_schema + detailed description
    LLM->>MCP: wick_execute({tool_id, params})
    MCP->>Auth: re-check visibility + op enabled
    MCP->>Op: ExecuteFunc(ctx, c, params)
    Op->>API: HTTP / SDK call
    API-->>Op: response
    Op-->>MCP: result
    MCP-->>LLM: { result, audit_id }
```

```
wick_list                                 → list of { tool_id, connector, name, description, destructive }
wick_get({tool_id: "conn:abc/get"})       → schema + detailed description
wick_execute({tool_id, params: {...}})    → operation result
```

Or shortcut when the LLM already knows the schema from a prior turn:

```
wick_search({query: "loki query"})        → matched tool_id
wick_execute({tool_id, params: {...}})    → result
```

### Auth check on every call

`wick_execute` and `wick_get` re-validate `IsVisibleTo(connector_id, user_tag_ids, is_admin)` on every call — they never trust a list-time cache. The `connector_operations` enable state is also re-checked. Removing a user's tag or disabling an op takes effect on the very next call.

## Auth modes

The `/mcp` endpoint accepts two bearer-token formats, dispatched by prefix:

| Mode | Wire format | Use when |
|------|-------------|----------|
| **Personal Access Token** | `wick_pat_<32hex>` | The client cannot speak the OAuth dance — Claude Desktop, Cursor, cURL, custom CLIs. See [Access Tokens](./access-tokens). |
| **OAuth 2.1 access token** | `wick_oat_<32hex>` | The client supports OAuth (Claude.ai, web-based clients). See [OAuth Connections](./oauth-connections). |

Both formats are opaque (not JWT) and stored hashed (SHA-256). A DB leak does not expose tokens. Plaintext only crosses the wire at issue time:

- PAT: rendered once at `/profile/tokens` after the user clicks Create.
- OAuth: returned in the `/oauth/token` response body to the client.

## Install snippets

![MCP install page](/screenshots/mcp-install-page.png)

*`/profile/mcp` install snippets — OAuth section + Bearer section with all 4 ready-to-paste snippets.*

The in-app `/profile/mcp` page shows the URL and four install snippets ready to copy. Below is the same content for reference.

### Claude.ai (OAuth)

Paste this URL into Claude.ai's MCP server settings:

```
https://<your-wick-host>/mcp
```

Claude.ai will:

1. `POST /oauth/register` to obtain a `client_id` (Dynamic Client Registration, RFC 7591 — no pre-shared secret).
2. Redirect the browser through `/oauth/authorize` for consent.
3. Exchange the code at `/oauth/token` for an access + refresh token.
4. Cache the tokens; refresh on its own as needed.

If the user is not logged in to wick when the redirect lands, wick captures the destination, bounces through `/auth/login` (password or Google SSO), then resumes the consent flow.

### Claude Desktop / Cursor / VSCode (Bearer)

Generate a Personal Access Token at [`/profile/tokens`](./access-tokens), then paste it into the client config.

**Claude Desktop** (`~/Library/Application Support/Claude/claude_desktop_config.json` on macOS, `%APPDATA%\Claude\claude_desktop_config.json` on Windows):

```json
{
  "mcpServers": {
    "wick": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "https://<your-wick-host>/mcp",
               "--header", "Authorization: Bearer ${WICK_PAT}"],
      "env": {
        "WICK_PAT": "wick_pat_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
      }
    }
  }
}
```

**Cursor** (`~/.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "wick": {
      "url": "https://<your-wick-host>/mcp",
      "headers": {
        "Authorization": "Bearer wick_pat_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
      }
    }
  }
}
```

**VSCode** (settings.json):

```json
{
  "mcp.servers": {
    "wick": {
      "url": "https://<your-wick-host>/mcp",
      "headers": {
        "Authorization": "Bearer wick_pat_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
      }
    }
  }
}
```

### cURL

```bash
curl -X POST https://<your-wick-host>/mcp \
  -H "Authorization: Bearer wick_pat_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/list","id":1}'
```

Expect a response containing the four `wick_*` tools. Then call `wick_list` to enumerate the connector rows visible to your token's user.

```bash
curl -X POST https://<your-wick-host>/mcp \
  -H "Authorization: Bearer wick_pat_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tools/call","id":2,
       "params":{"name":"wick_list","arguments":{}}}'
```

## Local MCP (stdio)

Wick ships a built-in stdio transport so any MCP client that spawns a child process — Claude Desktop, Cursor, Gemini CLI, Codex CLI, **Claude Code** — can connect directly to your local project without a hosted server or PAT.

The local server runs as a synthetic `local` admin: all connectors are visible, no auth middleware, no token required.

### Commands

#### `wick mcp serve`

Starts the MCP JSON-RPC server over stdin/stdout. Normally invoked automatically by the client; you rarely run this by hand.

```
wick mcp serve [--mode auto|dev|build|rebuild]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--mode` | `auto` | Build mode (see table below) |
| `--project` | (cwd) | Project root — set automatically by `mcp install`, not needed when running from the project dir |

**Build modes:**

| Mode | Behavior |
|------|----------|
| `auto` | Rebuild only when HEAD commit changed or any `.go` file is newer than binary |
| `dev` | `go run .` — always recompiles; no binary cache. Good while actively developing connectors |
| `build` | Build once if binary missing, reuse otherwise |
| `rebuild` | Always force a full rebuild |

#### `wick mcp config`

Print the ready-to-paste `mcpServers` JSON snippet for any client, plus show config file locations for all supported clients.

```
wick mcp config [--name <server-name>] [--mode auto|dev|build|rebuild]
```

#### `wick mcp install`

Write the `mcpServers` entry directly into the target client's config file.

```
wick mcp install [--client <target>] [--name <server-name>] [--mode auto|dev|build|rebuild]
```

| `--client` | Config file written |
|------------|---------------------|
| `claude` | Claude Desktop — `claude_desktop_config.json` |
| `cursor` | Cursor IDE — `settings.json` |
| `gemini` | Gemini CLI — `~/.gemini/settings.json` |
| `codex` | Codex CLI — `~/.codex/config.toml` |
| `claude-code` | Claude Code (project) — `.mcp.json` in project root |
| `all` | All five targets |

Default `--client` is `claude`.

### Wiring Claude Code

From the project root:

```sh
wick mcp install --client claude-code
# ✓ Claude Code (project)
#   .mcp.json
```

Or for dev mode (always recompiles — no stale binary surprises):

```sh
wick mcp install --client claude-code --mode dev
```

The generated `.mcp.json`:

```json
{
  "mcpServers": {
    "myproject": {
      "command": "go",
      "args": ["run", ".", "mcp", "serve"],
      "cwd": "/path/to/myproject"
    }
  }
}
```

For `auto` / `build` / `rebuild` modes the entry uses the compiled wick binary with `--project` so the client can spawn it from any working directory:

```json
{
  "mcpServers": {
    "myproject": {
      "command": "/path/to/wick",
      "args": ["mcp", "serve", "--mode", "auto", "--project", "/path/to/myproject"]
    }
  }
}
```

To approve all servers from `.mcp.json` without per-server prompts, add to `.claude/settings.json`:

```json
{
  "enableAllProjectMcpServers": true
}
```

After saving, restart Claude Code (or reload MCP servers via `/mcp`). The four `wick_*` tools appear in Claude Code's tool list with no token required.

## End-to-end test from a fresh project

1. **Register a connector** — the scaffolded template ships [`connectors/crudcrud/`](./connector-module). Confirm it's registered in `main.go`.
2. **Boot:** `wick dev`. Wick auto-seeds one row at `/manager/connectors/crudcrud`.
3. **Fill credentials.** For crudcrud, claim a sandbox URL at <https://crudcrud.com> and paste it into the row's `BaseURL` field.
4. **Smoke test in-browser.** Open the row, click `[Test]` next to any operation, run, verify the result panel.
5. **Generate a PAT.** Visit `/profile/tokens`, click Create, copy the token from the render-once banner.
6. **Wire up Claude Desktop.** Drop the snippet above into `claude_desktop_config.json`, restart Claude Desktop.

![Claude Desktop tool permissions for wick](/screenshots/mcp-claude-desktop.png)

*Claude Desktop Tools dialog showing the 4 `wick_*` tools registered after wiring a PAT.*

7. **Try it.** Ask Claude: "Use wick_list to see what connectors are available, then use wick_execute to list documents from the books resource on crudcrud."

## Sessions

The `Mcp-Session-Id` header is generated on the first `initialize` call and held in-memory only — no DB row. On server restart, sessions are dropped; clients re-initialize and receive a fresh ID transparently. Auth (PAT or OAuth) is the load-bearing identity binding; the session ID is just a protocol marker.

## Streaming

Default response is `Content-Type: application/json` — single round-trip. Wick switches to `Content-Type: text/event-stream` when:

- The client requested it via `Accept: text/event-stream`.
- The connector calls `c.ReportProgress(...)` mid-execution.

Server-initiated push (`GET /mcp`) is not currently used. Because the meta-tool list is always four entries, `notifications/tools/list_changed` is unnecessary.

## Audit trail

Every MCP `tools/call` writes a row to `connector_runs` with:

- `connector_id`, `operation_key`, `user_id`
- `source = "mcp"` (vs `"test"` for the in-app panel, `"retry"` for prefill replays)
- Request and response JSON (truncatable)
- `status`, `error_msg`, `latency_ms`, `http_status`
- Caller IP and User-Agent
- `parent_run_id` for retry lineage

The data backs the [history page](./connector-module#history-page) on each connector row. Retention is enforced by the [Connector Runs Purge](./connector-runs-purge) job — default 7 days.

## Reference

- MCP spec: <https://modelcontextprotocol.io>
- Streamable HTTP transport: <https://spec.modelcontextprotocol.io/specification/2025-03-26/basic/transports/#streamable-http>
- OAuth 2.1: [Connections](./oauth-connections)
- PAT: [Access Tokens](./access-tokens)
