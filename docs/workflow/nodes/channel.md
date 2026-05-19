---
outline: deep
---

# `channel`

Invoke a channel action — Slack `send_message`, `open_modal`, `add_reaction`, … — without going through an agent turn.

| | |
|---|---|
| **Source** | [`internal/agents/workflow/nodes/channel.go`](https://github.com/yogasw/wick/blob/master/internal/agents/workflow/nodes/channel.go) |
| **When to use** | Send messages, open modals, react, reply via Slack / Telegram / etc. Use over [`connector`](./connector) when the action is channel-flavoured (interactive surfaces, `response_url`, App Home). |
| **Discovery** | `workflow_integration` MCP op returns the full per-channel event + action catalog with `input_schema` / `output_schema` per action. |

## Schema

| Field | Type | Required | Notes |
|---|---|---|---|
| `channel` | string | ✅ | Channel adapter key (`slack`, `telegram`, …). |
| `op` | string | ✅ | Action key registered by the adapter. |
| `args` | YAML map (templated) | | Per-action input. Field set comes from the action's `input_schema`. |
| `arg_modes` | YAML map | | Per-arg `fixed` / `expression`. |

## Output

| Field | Type | What (action-dependent) |
|---|---|---|
| `ts` | string | Slack: message timestamp. |
| `channel` | string | Channel ID the action landed in. |
| `view_id` | string | `open_modal` only. |
| `view_hash` | string | `open_modal` only. |

Per-action output schemas are in `workflow_integration` — call it for the exact shape.

## Slack action registry

| Action | What |
|---|---|
| `send_message` | Post a message in a channel / DM / thread, with optional Block Kit blocks. |
| `update_message` | Edit an existing message by `ts`. |
| `send_ephemeral` | Visible only to one user. |
| `add_reaction` | Emoji reaction. |
| `open_dm` | Open (or reuse) a DM channel with a user, returning the channel ID. |
| `open_modal`, `push_modal`, `update_modal` | Open / push / update a Slack modal (uses `trigger_id` from an interaction payload). |
| `publish_home` | Update a user's App Home tab. |
| `respond_url` | Reply to an interaction using its `response_url` (works even when the bot lacks `chat:write` on that channel). |

See [Channels ▶ Slack](/guide/agents/channels#slack) for the transport details these actions ride on.

## Example

```yaml
- id: send_reply
  type: channel
  channel: slack
  op: send_message
  arg_modes:
    channel: expression
    text: expression
  args:
    channel: '{{index .Event.Payload "channel_id"}}'
    text: 'Got it, looking into {{.Node.classify.verdict}}.'
    thread_ts: '{{index .Event.Payload "thread_ts"}}'
```

## Pair with

- [`agent`](./agent) — agent generates the reply, channel posts it.
- [`connector`](./connector) — for raw Slack Web API calls without channel-side hooks.
