---
outline: deep
---

# Custom Connectors

Build a connector from the admin UI — no Go code, no recompile, no redeploy. A custom connector is a definition stored in the database that wick replays into the same registry as built-in connectors at boot (and on save). From the LLM's point of view the two are indistinguishable: same `tool_id` shape in `wick_list` / `wick_execute`, same [encrypted fields](/reference/encrypted-fields) handling, same run audit trail, same [tag-based access control](/guide/connector-module#sharing-connectors-with-tags).

Use a custom connector when you want to **lock an LLM to specific endpoints and operations** of an API that wick hasn't wrapped in a typed connector yet. For one-off ad-hoc calls, the built-in [HTTP / REST](/connectors/httprest) connector is still the quicker tool.

## Custom vs built-in

| | Built-in connector | Custom connector |
|---|---|---|
| Defined in | Go code (`internal/connectors/*`, or your app via `app.RegisterConnector`) | A database row, created from the admin UI |
| Operations | Typed Go functions | Templated HTTP requests, or proxied MCP tool calls |
| Changing it | Code change + redeploy | Edit in the UI, click **Reload** |
| Pagination, retries, custom logic | Yes | No — single request per operation, response passed through as-is |
| Instances | One or many rows | One or many rows — `+ New row` / Duplicate, each with its own credentials (opt into "single instance only" per definition) |
| MCP surface, secrets, audit, tags | Same | Same |

## Three ways to create one

From **Connectors → + New connector** you can start from any of three sources. All three land on the same review form before anything is saved.

| Source | Best for | Cost |
|---|---|---|
| **Paste — cURL tab** | A real cURL command (DevTools "Copy as cURL") | Free — deterministic parser, no LLM call |
| **Paste — AI tab** | Anything else: `fetch()` snippets, axios calls, Postman fragments, raw API-docs prose | One LLM call per parse |
| **MCP server** | Teams that already host an internal MCP server and want selected tools governed by wick | Free |
| **Manual builder** | APIs with docs but no cURL or MCP spec handy | Free |

### Flow 1 — paste a cURL command

1. Open **Connectors → + New connector → From cURL** and paste a command, e.g.:

   ```bash
   curl -X POST 'https://api.example.com/v1/charges' \
     -H 'Authorization: Bearer sk_live_xxx' \
     -d 'amount=2000&currency=usd&customer=cus_123'
   ```

2. Click **Parse**. The parser understands the common DevTools surface: `-X`/`--request`, `-H`/`--header`, `-d`/`--data`/`--data-raw`/`--data-binary`/`--data-urlencode`, `-u`/`--user` (becomes a Basic `Authorization` header), `--url`, and the positional URL. Unknown flags like `--compressed` are ignored.

3. Wick splits what it found into two buckets on the review screen:

   - **Configs** — values that are stable across requests. The host becomes `base_url`; credential-looking headers (`Authorization`, `X-Api-Key`, values matching `Bearer …`/`Basic …`) become **secret** config fields, stored encrypted.
   - **Inputs** — values the LLM provides per call: query parameters, body fields, and trailing numeric/UUID path segments (e.g. `/users/42` becomes `/users/{{.in.user_id}}`).

   For the example above you get configs `base_url` + `auth_value` (secret), and an operation `post_charges` with inputs `amount`, `currency`, `customer`.

4. Review everything — rename keys, change widgets, toggle the secret flag, mark an operation **Destructive** (auto-suggested for `DELETE` requests), add descriptions and defaults.

5. Set the connector **Key** (a unique lowercase slug — it shares a namespace with built-in connector keys and cannot be changed later), **Name**, **Icon**, and a **Category**, then save. Wick registers the module and takes you to the connector page. **No instance is auto-created** — click **+ New row** to create the first one and fill in its config values; add more rows (or **Duplicate**) for other accounts, exactly like a built-in connector. Tick **Single instance only** on the review form if the definition should be locked to one row.

### The AI paste tab

When the paste box content is not a cURL command, switch to the **AI** tab. One LLM call extracts the same request shape from `fetch()` snippets, axios calls, Postman exports, or plain prose like "POST /users with name and email in the body". The result feeds the exact same review form.

Notes:

- The tab only appears when a configured provider supports **structured output**. No capable provider → tab hidden.
- Pastes are capped at **8 KB** (both tabs). Longer pastes return an error asking you to trim down to a single endpoint.
- The raw paste is **never stored** — only the extracted definition you approve on the review screen is persisted.
- If the paste mixes multiple endpoints, only the first complete one is extracted. Parse one endpoint at a time.

### Flow 2 — connect an MCP server

Wick can register an external MCP server (streamable HTTP only) and re-expose its tools as connector operations. **One server is one connector**: saving the registration form creates the connector immediately, and **every tool the server lists becomes an operation automatically** — nothing per-tool is stored, so tools added on the server side appear after a re-sync without touching wick. You control the surface with an **exclude list** (opt-out), not an import picker. Wick acts as a **forwarder**: it stores the URL and auth material and fires a JSON-RPC `tools/call` per execution — no process is spawned, nothing is supervised. Stdio MCP servers are out of scope; expose them through an HTTP sidecar (`mcp-proxy`, `supergateway`, or similar) first.

**Register the server** under **Connectors → + New connector → From MCP server**:

| Field | Notes |
|---|---|
| Label | Becomes the connector's name (and its key, slugified) |
| URL | Streamable-HTTP endpoint, e.g. `https://mcp.internal.example.com/v1` |
| Auth scheme | One of the four below |
| Extra headers | Optional rows (routing, tenancy) sent with every call on top of the scheme's headers; each row can be marked secret |
| Tools | Filled in after a successful test — tick a tool to **exclude** it; everything unticked is exposed |

**Auth schemes:**

| Scheme | What wick sends | Storage |
|---|---|---|
| `none` | `Content-Type` / `Accept` only | — |
| `bearer` | `Authorization: Bearer <token>` | Token encrypted at rest, decrypted per request |
| `custom_header` | Your key/value header rows (e.g. `X-Api-Key`) | Rows marked secret are encrypted at rest |
| `oauth` | `Authorization: Bearer <OAuth access token>` for the **instance's connected account** | Tokens per instance, encrypted at rest; refreshed automatically |
| `sso` | A short-lived signed JWT in `X-Wick-User` identifying the calling user | No shared secret stored at all |

**About `oauth`:** this is the standard MCP authorization flow — pick it when the server answers unauthenticated calls with `401 invalid_token`. Wick discovers the server's authorization server (RFC 9728/8414), registers an OAuth client automatically when the server supports dynamic registration (or use the manual client ID/secret/scopes fields), and signs you in through a browser popup with PKCE. **Accounts are per instance**: clicking **Test now** runs the login, saving creates the connector with that account as its first instance, and every additional instance connects its own account via the **Connect account →** button on its page. Access tokens refresh transparently when they expire. Use `sso` instead only for in-house servers that validate wick's own JWT.

**About `sso`:** per request, wick mints an ED25519-signed JWT for the user who triggered the call, with claims `sub` (user ID), `email`, `name`, `groups` (the user's tag IDs, for downstream RBAC), `aud` (defaults to the MCP URL host), `iss` (your wick base URL), and `iat`/`exp` (TTL defaults to 5 minutes). Your MCP server must validate the token against wick's public key, published at:

```
https://<your-wick-host>/.well-known/wick-pubkey.pem
```

::: warning SSO needs server-side support
Stock open-source MCP servers do not validate `X-Wick-User` — the `sso` scheme is for in-house servers you control. The win: no shared secret to rotate, per-user RBAC and audit on the MCP side, and revoking a wick user instantly revokes downstream access.
:::

**Test, then save:**

1. Click **Test connection**. Wick performs an `initialize` + `tools/list` round-trip with the configured auth and shows the discovered tools in the exclude list. **Save is blocked until at least one test succeeds** — half-broken registrations never reach the database.
2. Optionally tick tools to exclude, then save. The connector exists immediately — you land on its page, where **+ New row** creates the first instance.
3. Each exposed tool is one operation. Its JSON `inputSchema` is mapped to wick widgets (strings → text, enums → dropdown, numbers → number, booleans → checkbox, objects/arrays → raw-JSON textarea, password-ish fields → secret). Tools named `delete_*`, `remove_*`, `drop_*`, etc. are flagged **Destructive** (disabled by default per instance, like any destructive operation).

**Keeping it in sync:** the operation set mirrors the server's live `tools/list` at every module build (boot, save, re-sync) — the catalog is never cached. To pick up tools added or removed upstream, click **↻ Re-sync tools** on the connector's page. The operation set is connector-level (shared by every instance), so this is a single per-connector action available to any user who can open the connector. It re-fetches `tools/list` (using a connected account for `oauth` servers) and refreshes the stored connection status.

To reconnect or change auth/exclusions, open **Edit definition** (it leads to the server form), run **Test now**, and save — the module rebuilds atomically on save. Credential edits apply to calls immediately either way, because the server row is re-read on every execution. Deleting the connector definition also removes the server registration.

### Flow 3 — manual builder

**Connectors → + New connector → Blank** walks through three steps:

1. **Meta** — key, name, description, icon.
2. **Configs** — a table editor: key, label, widget, secret/required toggles, default, description.
3. **Operations** — for each operation: meta (key, name, description, destructive toggle), inputs (same table editor), and the request recipe — method, URL template, header rows, body template, content type.

A **Test** button fires the operation once against your current config values so you can verify the recipe before saving. Useful when you have API docs but neither a cURL string nor an MCP server.

## Health check

A definition can nominate **one operation as a health probe**. When set, every instance page gets a **Check Permissions** button and a status banner, exactly like a built-in connector — wick runs the probe with that instance's stored credentials and reports whether the connector is reachable.

Set it on the review / edit form, in the **Health check** block:

| Field | Meaning |
|---|---|
| **Probe operation** | The operation wick runs as the check. Pick a read-only call that needs no inputs (e.g. `get_account`, `ping`, `list_*` with no required parameters) — the probe runs with config only, no per-call inputs. Leave on *No health check* to disable. |
| **Expected text in response** *(optional)* | A substring the probe response must contain. Leave empty to accept any successful response. |

**Verdict.** The connector is **healthy** when the probe operation runs without error — and, when an expected text is set, the response also contains it. "Without error" is the same bar an `wick_execute` call clears, so it works for both connector sources:

- **HTTP operations** (cURL / manual) — the request returns a `2xx` status with no transport error.
- **MCP operations** (proxied tools) — the `tools/call` returns a result rather than an error.

So you do not have to assume an HTTP 200: an MCP-forwarded tool with no HTTP status of its own passes as long as the tool call itself succeeds.

The optional expected text catches the "200 but actually an error" case — an API that answers `200 OK` with `{"ok":false,"error":"bad key"}`. Set the expected text to something only a real success contains, e.g. `"ok":true`. The check serializes the response (decoded JSON is matched against its compact JSON form) and looks for the substring.

**What a failing probe does.** A custom connector instance carries one credential, so the probe's verdict applies to the whole connector: when it fails, **every operation is system-disabled** (not listable or callable) with the failure reason attached, until a later check passes and clears it. An admin can still override an individual operation by re-enabling it. A passing probe clears any locks the previous check set.

::: tip Probe ops with required inputs
The probe runs with config values only — it supplies no per-call inputs. If the operation you pick requires inputs, the probe will send them empty and likely fail. Choose (or add) an input-free operation for the health check.
:::

## Access control

Access rides the standard tag system — there is no separate sharing mechanism.

- On save, wick auto-creates a filter tag named `custom:<key>` (e.g. `custom:stripe`) and links it to the connector instance, together with the **Connector** group tag and your chosen category tag.
- **Default = admin-only.** No user carries the new tag yet, so the filter rule hides the connector from every non-admin — both in the manager UI and in MCP `wick_list`. Admins always see it.
- **Grant access:** open `/admin/tags`, pick the `custom:<key>` tag, and assign it to users or user groups. Tagged users see the connector and can call its operations.
- **Open to all:** remove the `custom:<key>` tag from the connector instance (Access section on the instance page). Without a filter tag, every approved user can see it. The tag row itself is kept, so you can re-attach it later to restore the restriction.
- Per-operation switches still apply on top: an operation can be disabled entirely, or marked admin-only even when the row is open to others. A call goes through only when the row tags allow it **and** the op is enabled **and** the op is not admin-only for a non-admin caller.

## Editing and reloading

Editing a definition does **not** affect the running connector:

1. **Edit** the definition — fields, inputs, templates, operations. The key is immutable.
2. The instance page shows a **needs reload** banner: the stored definition is now newer than the module currently serving. In-flight and new calls keep using the old definition.
3. Click **Reload** — wick rebuilds the module from the stored row and swaps it atomically. No restart, no downtime; in-flight calls finish on the old version.

**Status (MCP definitions):** the connector's page shows a connection chip — **● Connected**, **● Disconnected**, or **● Never tested** — for custom MCP connectors. The status refreshes on every module rebuild (boot, **↻ Re-sync tools**, server-form save), so it stays Connected until a re-sync, reconnect, or disable says otherwise. cURL/manual definitions have no connection to track and show no chip.

**Disable / enable:** the definition danger zone has a **Disable definition** toggle. A disabled definition keeps its card, pages, and instance rows, but serves zero operations — nothing is listable or callable — until re-enabled (MCP definitions re-probe on enable).

Deleting a connector removes the definition and its instances — and for MCP definitions, the server registration too (run history is kept for audit, and the `custom:<key>` tag survives so re-creating the same key restores prior grants). The old module stays in memory until the next restart, but calls against it fail immediately since the instance rows are gone.

## Allow per-session config override

A definition can opt into [**session workspace**](/guide/mcp#session-workspace) cloning — letting a user (or the agent, via `wick_session_workspace`) spin up a throwaway copy of the connector pointed at a different base URL or key for a single agent session, without touching the saved instance.

- On the review / edit form, tick **Allow per-session config override** (default off). This sets `allow_session_config: true` on the definition — the capability flag.
- It is a **two-layer opt-in**: the capability alone does nothing until an admin also flips the **Per-session config** toggle on a specific instance (Manager → Connectors → {instance}). Both must be on before the connector shows up as a base in the session Workspace tab or the `wick_session_workspace` `add` action.
- Best for cURL / manual API definitions whose config is a swappable base URL + key. Leave it off for OAuth / SSO-backed MCP definitions, whose "config" is a user token, not a replaceable value.

Session instances live only in the session that created them, store secrets under a system-only master key (never returned to the agent), and are purged when the session ends. See [Session workspace](/guide/mcp#session-workspace) for the full model.

## Template syntax

URL templates, header values, and body templates are Go `text/template` strings rendered per call:

| Syntax | Meaning |
|---|---|
| `{{.cfg.base_url}}` | A config value from the instance (secrets arrive already decrypted) |
| `{{.in.amount}}` | An input value supplied by the caller for this execution |
| `{{urlquery .in.q}}` | URL-encode a value — use for query strings and form bodies |
| `{{js .in.name}}` | Escape a value for safe embedding inside a JSON string |
| `{{printf "%s:%s" .cfg.user .cfg.pass}}` | Standard `printf` formatting |
| `{{default "usd" .in.currency}}` | Fall back to `"usd"` when the input is empty |
| `{{lower .in.code}}` / `{{upper .in.code}}` | Case conversion |
| `{{b64 (printf "%s:%s" .cfg.user .cfg.pass)}}` | Base64-encode — e.g. `Basic {{b64 (printf "%s:%s" .cfg.user .cfg.pass)}}` |

Referencing a key that does not exist is a **hard error** (`missingkey=error`): a typo like `{{.cfg.api_keyy}}` fails the call with a clear message in the run history instead of silently sending `<no value>` upstream.

Example request recipe:

```json
{
  "method": "POST",
  "url_template": "{{.cfg.base_url}}/v1/charges",
  "headers": { "Authorization": "Bearer {{.cfg.auth_value}}" },
  "body_template": "amount={{urlquery .in.amount}}&currency={{urlquery .in.currency}}",
  "content_type": "application/x-www-form-urlencoded"
}
```

## Limits and safety

- **8 KB** paste cap on both parser tabs.
- **1 MB** cap on each rendered template (URL, header value, body).
- Rendered URLs must be `http://` or `https://` — anything else is rejected.
- **No scripting.** Templates are logic-less beyond the whitelisted functions above — no shell, no file access, no `exec`, no JS/Lua transforms. Responses pass through as-is.
- Upstream responses are read up to 4 MB and returned as decoded JSON (or raw text when the upstream is not JSON); non-2xx statuses surface as errors with a body snippet in the run history.
- Raw AI-parser pastes are never persisted; only the reviewed definition is.
- MCP server credentials (bearer tokens, secret header values) are encrypted at rest and decrypted only for the lifetime of a request.

## See also

- [Connector Module](/guide/connector-module) — the Go contract custom connectors are replayed into.
- [Built-in Connectors](/connectors/) — what ships out of the box.
- [HTTP / REST](/connectors/httprest) — the quick ad-hoc alternative.
- [Encrypted Fields](/reference/encrypted-fields) — how secret values are stored and masked.
- [MCP for LLMs](/guide/mcp) — how operations surface to LLM clients.
