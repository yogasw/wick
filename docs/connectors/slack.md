---
outline: deep
---

# Slack

`slack` wraps the Slack Web API. One instance carries one set of Slack credentials (bot token + optional user OAuth) and exposes read + write ops over channels, threads, users, messages, and reactions.

This is the **outbound** Slack surface — what a workflow or LLM calls to *do* something on Slack. The **inbound** surface (events arriving in real time) is the [Slack channel](/guide/agents/channels#slack); the two are separate modules but normally configured together. The connector row also stores the OAuth app credentials when you wire up the [Connect with Slack](/guide/agents/channels#slack) user-token flow.

| | |
|---|---|
| **Source** | [`internal/connectors/slack/`](https://github.com/yogasw/wick/tree/master/internal/connectors/slack) |
| **Key** | `slack` |
| **Icon** | 💬 |
| **Tier** | builtin (every wick app) |
| **Health check** | ✅ — `Test Integration` button on the row runs every API the connector depends on |
| **OAuth** | ✅ — global app credentials live on this row |

## Configs

The Slack row holds credentials for both the connector ops and the [Slack channel](/guide/agents/channels#slack). The exact field set is form-rendered from the `Configs` struct — common fields:

| Field | Type | Purpose |
|---|---|---|
| `BotToken` | secret | `xoxb-…` token used by every connector op. |
| `UserToken` | secret | `xoxp-…` user OAuth token, set after the operator clicks **Connect with Slack**. Lets the connector act as a real user (used by `open_dm`, signed DM footers, …) rather than the bot identity. |
| `ClientID` / `ClientSecret` / `SigningSecret` | secret | Slack app credentials. Required when the channel runs in `http` mode or when user-token OAuth is enabled. |

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

All read ops are `connector.Op` (non-destructive).

## Operations (write — destructive, opt-in per row)

| Op | Input | What it does |
|---|---|---|
| `send_message` | `channel`, `text`, `blocks`, `thread_ts`, `reply_broadcast`, `unfurl_links`, `mrkdwn` | Post a message to a channel / DM / thread. |
| `send_ephemeral` | `channel`, `user`, `text`, `blocks`, `thread_ts` | Visible only to `user`. |
| `update_message` | `channel`, `ts`, `text`, `blocks` | Edit an existing message. |
| `delete_message` | `channel`, `ts` | Delete by ts. |
| `add_reaction` | `channel`, `ts`, `name` | Emoji reaction (name without colons). |
| `remove_reaction` | `channel`, `ts`, `name` | Remove a reaction. |

Every write op is `connector.OpDestructive` — disabled on every new row, opt-in per (row, op) at `/manager/connectors/slack/{id}`.

## Quirks worth knowing

- `channel` accepts a channel ID (`C…`), DM ID (`D…`), user ID (`U…` — auto-opens DM), or `#name` (only resolves when the bot is already a member).
- `thread_ts` is always the **parent** message ts — replying to a reply still uses the root ts.
- `get_channel_history` returns only top-level messages. Walk thread replies with `get_thread_replies` against each parent `ts`.
- `oldest` / `latest` are Slack ts strings (`"1700000000.000100"`), not RFC3339.
- Pagination uses `cursor` from `response_metadata.next_cursor`; `limit` caps the per-call page (max 1000), not the total.
- Email lookup requires the `users:read.email` scope; without it the `profile.email` field is empty in `list_users` output.
- Rate limit: 1 msg/sec per channel for `send_message`. Bursts get queued then 429.
- `blocks` overrides `text` for rendering, but Slack still wants non-empty `text` for the notification preview — always set both.

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

For Slack actions that aren't 1:1 with a Web API call — modals, `respond_url`, App Home publish — use a `channel` node instead of `connector`. See [Workflows ▶ channel node](/workflow/nodes/channel).

## See also

- [Channels ▶ Slack](/guide/agents/channels#slack) — inbound side (events, access control, picker, hot-reload).
- [Workflows](/workflow/) — using these ops + channel actions in a DAG.
- [HTTP / REST](./httprest) — fallback for any Slack Web API call wick hasn't typed yet.
