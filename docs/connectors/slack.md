---
outline: deep
---

# Slack

`slack` wraps the Slack Web API. One instance carries one set of Slack credentials (bot token + optional user OAuth) and exposes read + write ops over channels, threads, users, messages, and reactions.

This is the **outbound** Slack surface — what a workflow or LLM calls to *do* something on Slack. The **inbound** surface (events arriving in real time) is the [Slack channel](/guide/agents/channels#slack); the two are separate modules but normally configured together. The connector row also stores per-instance OAuth app credentials for the Connect Account user-token flow.

| | |
|---|---|
| **Source** | [`internal/connectors/slack/`](https://github.com/yogasw/wick/tree/master/internal/connectors/slack) |
| **Key** | `slack` |
| **Icon** | 💬 |
| **Tier** | builtin (every wick app) |
| **Health check** | ✅ — `Test Integration` button on the row runs every API the connector depends on |
| **OAuth** | ✅ — global app credentials live on this row |

## Configs

The Slack row holds credentials for both the connector ops and the [Slack channel](/guide/agents/channels#slack). The exact field set is form-rendered from the `Configs` struct — all fields are always visible in the admin form:

| Field | Type | Purpose |
|---|---|---|
| `AuthMode` | dropdown | `bot_token` (default) or `user_token` — selects which token the runtime reads when making API calls. |
| `BotToken` | secret | `xoxb-…` token used by every connector op when `AuthMode=bot_token`. Needs `files:read` and `reactions:read` (alongside the existing scopes) to use the Files ops and `get_reactions`, and `lists:read` / `lists:write` for the Lists ops. |
| `UserToken` | secret | `xoxp-…` user OAuth token, used when `AuthMode=user_token`. Set after the operator clicks **Connect Account** when `ClientID` is configured, or paste manually. |
| `ClientID` | string | Slack OAuth App Client ID. Required to activate the **Connect Account** button for the user-token OAuth flow. Lives on this instance row, not in a shared server setting. |
| `ClientSecret` | secret | Slack OAuth App Client Secret. Required for the token exchange step of the Connect Account flow. Lives on this instance row. |
| `CustomAPIMode` | dropdown | Governs `custom_api_call` (see [Custom API escape hatch](#custom-api-escape-hatch)). `allowlist` (default) or `all`. |
| `CustomAPIAllowlist` | kvlist | Method names `custom_api_call` may reach when `CustomAPIMode=allowlist`. Supports a trailing `*` wildcard, e.g. `admin.*`. |

OAuth app credentials (`ClientID` / `ClientSecret`) are now per-instance — different Slack connector rows can use different Slack apps. Enable the Connect Account flow by setting both fields and enabling `EnableSSO` in the Access Policy section.

The `Test Integration` button at the top of the row runs each API the connector needs in parallel (~5s budget) and reports only failures — `auth.test`, `users.list`, `conversations.list`, `chat.postMessage` dry-run, etc. See [Channels ▶ Integration health check](/guide/agents/channels#integration-health-check) for the equivalent on the channel side.

## Operations (read)

| Op | Input | What it does |
|---|---|---|
| `list_channels` | `types`, `exclude_archived`, `name_contains`, `limit`, `cursor` | List channels visible to the bot. Paginated via `cursor`. |
| `search_channels` | `query`, `limit` | Substring search by channel name (case-insensitive). |
| `get_channel_info` | `channel` | Metadata for one channel — topic, purpose, creator, created. |
| `get_channel_history` | `channel`, `limit`, `oldest`, `latest`, `cursor` | Recent messages. Top-level only — use `get_thread_replies` for threaded replies. |
| `get_thread_replies` | `channel`, `ts`, `limit`, `cursor` | Parent + every reply under a thread. |
| `list_users` | `limit`, `cursor` | Workspace members. Email requires `users:read.email` scope. |
| `get_user_info` | `user` | Profile for one user ID. |
| `get_user_by_email` | `email` | Resolve a workspace user by email. Pair with `channel:slack.open_dm` to DM them. |
| `get_permalink` | `channel`, `ts` | Permalink URL for a message ts. |
| `get_reactions` | `channel`+`ts` or `file`, `full` | Read the reactions already on a message or a file — emoji name, count, and (with `full=true`) reacting user IDs. Requires `reactions:read`. |

All read ops are `connector.Op` (non-destructive).

## Operations (files)

| Op | Input | What it does |
|---|---|---|
| `list_files` | `channel`, `user`, `ts_from`, `ts_to`, `types`, `limit`, `page` | List files visible to the bot. Page-based pagination (`page`/`pages`), not cursor-based like the ops above. |
| `get_file_info` | `file` | Metadata for one file ID — name, mimetype, size, channels, download/permalink URLs. Does not download bytes. |
| `read_file` | `file`, `max_bytes` | Download a file's bytes with the bot token and return them inline — UTF-8 text as a string, binary (images, PDFs) as base64. Refuses files over `max_bytes` (default 8 MiB). |

All three require the `files:read` scope. `read_file` downloads via `url_private_download` using the bot token as a Bearer header — `files.info` alone can't fetch bytes, the download URL is auth-gated. The bot must also be a member of a channel the file was shared to, otherwise Slack answers with an HTML login page instead of the file (surfaced as a clear error, not base64'd HTML).

## Operations (write — destructive, opt-in per row)

| Op | Input | What it does |
|---|---|---|
| `send_message` | `channel`, `text`, `blocks`, `thread_ts`, `reply_broadcast`, `unfurl_links`, `mrkdwn`, `session_id?` | Post a message to a channel / DM / thread. |
| `send_ephemeral` | `channel`, `user`, `text`, `blocks`, `thread_ts` | Visible only to `user`. |
| `update_message` | `channel`, `ts`, `text`, `blocks`, `session_id?` | Edit an existing message. Re-appends the "Sent using" footer. |
| `delete_message` | `channel`, `ts` | Delete by ts. |
| `add_reaction` | `channel`, `ts`, `name` | Emoji reaction (name without colons). |
| `remove_reaction` | `channel`, `ts`, `name` | Remove a reaction. |

Every write op is `connector.OpDestructive` — enabled by default on every new row. Admins can disable individual ops per (row, op) at `/manager/connectors/slack/{id}`. The MCP layer appends a destructive warning to these ops' descriptions so the LLM confirms before calling.

## Operations (Lists)

Slack Lists are the structured, spreadsheet-like records behind `slackLists.*` — **a paid-plan feature**, empty on free workspaces.

| Op | Input | What it does |
|---|---|---|
| `list_lists` | `channel`, `user`, `limit`, `page` | Enumerate Lists visible to the token. Rides on `files.list` with `types=lists` — Slack has no `slackLists.list` method — so this needs `files:read`, not `lists:read`. Page-based pagination (`page`/`pages`). |
| `get_list` | `list_id` | A List's metadata and column schema. Read this first — column `id`s (`Col…`) are what `create_list_item` / `update_list_item` need. Requires `lists:read`. |
| `list_list_items` | `list_id`, `limit`, `cursor`, `archived` | Read a List's rows. Cursor-based pagination via `response_metadata.next_cursor`. `archived=true` returns archived rows *instead of* live ones. Requires `lists:read`. |
| `get_list_item` | `list_id`, `item_id` | One row plus the parent List's schema and any subtasks. Returns the row under `record` (not `item` — that's `create_list_item`'s key). Requires `lists:read`. |
| `create_list` (destructive) | `name`, `schema`, `todo_mode`, `copy_from`, `copy_records` | Create a new List — with an explicit JSON column `schema`, in to-do mode, or duplicated from an existing List via `copy_from`. Requires `lists:write`. |
| `create_list_item` (destructive) | `list_id`, `initial_fields`, `parent_item_id`, `duplicate_from` | Add a row, optionally pre-filled, as a subtask, or duplicated from an existing row. `initial_fields` is keyed by `column_id`, not column name. Requires `lists:write`. |
| `update_list_item` (destructive) | `list_id`, `cells` | Write cell values into one or more rows in one call. Each cell needs `row_id`, `column_id`, and a value key matching the column type. Requires `lists:write`. |
| `delete_list_item` (destructive) | `list_id`, `item_id` | Permanently delete a row (and its subtasks). Requires `lists:write`. |

Cells and schema columns are addressed by Slack-generated IDs (`Col…` for columns, `Rec…` for rows) — always resolve them via `get_list` / `list_list_items` before writing, never guess them.

## Custom API escape hatch

`custom_api_call` calls any Slack Web API method the connector has no dedicated op for (e.g. `emoji.list`, `pins.add`, `conversations.members`), using the connector's own token. It's `connector.OpDestructive`, so it's **disabled by default** on every instance — an admin must enable it at `/manager/connectors/slack/{id}` before it can run at all.

| Input | Purpose |
|---|---|
| `method` | Bare Slack method name (no leading slash, no URL). Rejected if it contains a scheme, host, query string, `..`, or `/`. |
| `http_method` | `auto` (default), `get`, or `post`. On auto, methods ending in `.list`/`.info`/`.history`/`.replies`/`.members` go out as GET, everything else POST. |
| `params` | JSON object of request arguments. A `token` key is rejected outright — the connector's own credential is attached automatically. |

Which methods are reachable is governed by the two Custom API config fields above:

- **`allowlist` mode (default)** — only method names in `CustomAPIAllowlist` are permitted (trailing `*` wildcard supported, e.g. `admin.*`). An empty allowlist blocks every call.
- **`all` mode** — every Slack Web API method is permitted; the token's own granted scopes are still the real limit (a missing-scope call fails with Slack's `missing_scope` error regardless of the allowlist).

Prefer a dedicated op when one exists — it validates input and returns a tidied shape, while `custom_api_call` returns Slack's raw response verbatim.

```yaml
- id: pin
  type: connector
  module: slack
  op: custom_api_call
  arg_modes:
    params: expression
  args:
    method: pins.add
    params: '{"channel":"{{.Node.trigger.payload.channel_id}}","timestamp":"{{.Node.trigger.payload.ts}}"}'
```

## Quirks worth knowing

- **`session_id` on `send_message` / `update_message`** — optional field that tells wick which agent session owns this call. When set (or auto-injected via the `X-Wick-Session-Id` MCP header), the "Sent using @bot" footer names the bot that owns the session rather than falling back to the app name. Leave it empty when calling outside an agent session.
- `channel` accepts a channel ID (`C…`), DM ID (`D…`), user ID (`U…` — auto-opens DM), or `#name` (only resolves when the bot is already a member).
- `thread_ts` is always the **parent** message ts — replying to a reply still uses the root ts.
- `get_channel_history` returns only top-level messages. Walk thread replies with `get_thread_replies` against each parent `ts`.
- `oldest` / `latest` are Slack ts strings (`"1700000000.000100"`), not RFC3339.
- Pagination uses `cursor` from `response_metadata.next_cursor`; `limit` caps the per-call page (max 1000), not the total.
- Email lookup requires the `users:read.email` scope; without it the `profile.email` field is empty in `list_users` output.
- Rate limit: 1 msg/sec per channel for `send_message`. Bursts get queued then 429.
- `blocks` overrides `text` for rendering, but Slack still wants non-empty `text` for the notification preview — always set both.
- `list_files` paginates by `page`/`pages`, not `cursor` — different scheme from the channel/user/message read ops above.
- `ts_from` / `ts_to` on `list_files` are Unix **seconds** (e.g. `1700000000`), not Slack message ts strings.
- Lists ops need Slack Lists to be available on the workspace's plan — free workspaces return an empty `list_lists` result rather than an error.
- `custom_api_call` is disabled by default on every new row (it's `OpDestructive`) — enable it explicitly before it can be called, then configure `CustomAPIMode` / `CustomAPIAllowlist` on the same page.

## Workflow integration

Slack ops are a common right-hand side of a workflow `connector` node:

```yaml
- id: notify
  type: connector
  module: slack
  op: send_message
  arg_modes:
    text: expression
  args:
    channel: "#alerts"
    text: "New ticket from {{.Node.trigger.payload.user}}: {{.Node.trigger.payload.text}}"
```

### Channel-node actions

For Slack actions that aren't 1:1 with a plain Web API call — modals, ephemerals, App Home, slash-command replies — use a [`channel`](/workflow/nodes/channel) node (`channel: slack`) and pick the action with `op`. These are wired to the live Slack API and complement the connector ops above.

| `op` | Destructive | Inputs | Returns |
|---|---|---|---|
| `send_message` | no | `channel`, `text`, `thread_ts?` | `ts`, `channel` |
| `reply_thread` | no | `channel`, `thread`, `text` | `ts` |
| `send_dm` | no | `user`, `text` | `ts`, `channel` |
| `send_ephemeral` | no | `channel`, `user`, `text` | `ts` |
| `update_message` | yes | `channel`, `ts`, `text` | `ts` |
| `react` | no | `channel`, `message_ts`, `emoji` | `ok` |
| `open_modal` | no | `trigger_id`, `view` | `view_id`, `view_hash` |
| `update_modal` | no | `view_id`, `view`, `view_hash?` | `view_id` |
| `push_modal` | no | `trigger_id`, `view` | `view_id`, `view_hash` |
| `open_dm` | no | `user` | `channel` |
| `publish_home` | no | `user_id`, `view` | `view_id` |
| `respond_url` | no | `response_url`, `text?`, `replace_original?`, `delete_original?`, `response_type?` | `ok` |

```yaml
- id: ask
  type: channel
  channel: slack
  op: open_modal
  args:
    trigger_id: "{{.Node.trigger.payload.trigger_id}}"
    view: "{{.Node.build_view.result}}"
```

Notes:

- `view` is a Slack [Block Kit](https://api.slack.com/block-kit) view JSON (string or object). Build it with a [`transform`](/workflow/nodes/transform) node upstream.
- `open_modal` / `push_modal` need a fresh `trigger_id` — Slack expires it ~3s after the interaction, so the path from inbound event to the modal op must be short.
- `react` is idempotent — re-adding an existing emoji is a no-op, not an error.

See [Workflows ▶ channel node](/workflow/nodes/channel).

## See also

- [Channels ▶ Slack](/guide/agents/channels#slack) — inbound side (events, access control, picker, hot-reload).
- [Workflows](/workflow/) — using these ops + channel actions in a DAG.
- [HTTP / REST](./httprest) — fallback for any Slack Web API call wick hasn't typed yet.
