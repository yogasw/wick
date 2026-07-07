---
outline: deep
---

# `connector`

Invoke a registered connector operation. Same code path as MCP `wick_execute`, with the run history feeding into `connector_runs` for audit.

| | |
|---|---|
| **Source** | [`internal/agents/workflow/nodes/connector.go`](https://github.com/yogasw/wick/blob/master/internal/agents/workflow/nodes/connector.go) |
| **When to use** | Call any registered external integration — the typed counterpart to [`http`](./http). |
| **Discovery** | `workflow_connectors` MCP op lists every module + op pair. |

## Schema

| Field | Type | Required | Notes |
|---|---|---|---|
| `module` | string | ✅ | Connector module key (e.g. `slack`, `github`, `httprest`, `wickmanager`). |
| `op` | string | ✅ | Operation key within the module (e.g. `send_message`, `create_issue`). |
| `instance_id` | string | | Connector row UUID. Optional — defaults to the only enabled row when there's exactly one. |
| `row_id` | string | | Same as `instance_id` — set by the canvas palette when you drill into a specific instance. |
| `account_id` | string | | Pins the node to one of the instance's connected SSO accounts (`EnableSSO` connectors, e.g. Slack). The account's OAuth token is injected as `user_token` at run time, overriding the row's own config. Leave empty to run with the instance's row-level ("Default credentials") config. |
| `args` | map (templated) | | Per-op input. Field set comes from the connector's `Input` struct — see [Connector Module ▶ Per-op Input](/guide/connector-module#per-operation-input-structs). |
| `arg_modes` | map | | Per-arg `fixed` / `expression`. Defaults to `fixed`; mark as `expression` to render the value as a Go template. |

## Output

Whatever the connector op returns — typically a typed Go struct serialised as JSON. The fields are merged into `.Node.<id>.*` for direct template access.

## Example

File a GitHub issue from a Slack thread:

```json
{
  "id": "file_bug",
  "type": "connector",
  "module": "github",
  "op": "create_issue",
  "arg_modes": {
    "title": "expression",
    "body": "expression"
  },
  "args": {
    "owner": "abc",
    "repo": "web",
    "title": "{{.Node.classify.parsed.summary}}",
    "body": "Reported in Slack by <@{{.Node.trigger.payload.user}}>:\n\n{{.Node.trigger.payload.text}}",
    "labels": "bug,from-slack"
  }
}
```

## Picking an instance and account from the palette

The canvas palette drills **connector → instance → op**. For a connector with more than one accessible instance you pick which row to bind first; for an `EnableSSO` instance (e.g. Slack) the picker expands further into one entry per connected account plus a **Default credentials** entry for the row's own config — dropping either sets `row_id` (and `account_id` when you picked an account) on the new node automatically. Only "ready" instances (fully configured) are shown, and ops disabled on that instance or account are hidden from the op list.

The palette only ever surfaces instances the current user can see — the same tag-based access filter as the connector manager's list.

## Tag visibility

The runtime calls `wick_execute` under the hood, so the same tag-filter rule applies — the workflow caller must have visibility on the (module, op) pair.

For **channel-bound** runs (Slack mention, webhook with a user context) the run identity is the triggering user. For **headless** runs (cron, webhook, manual trigger with no authenticated session) the connector node executes as the **workflow owner** — the user recorded in `created_by` at workflow creation time. Connector operations that require an authenticated user (e.g. `notifications.send_to_push_id`) therefore work correctly from headless runs without extra configuration; just ensure the workflow owner has the necessary tag access on the connector instance.

## Pair with

- [`channel`](./channel) — for Slack actions that aren't 1:1 with a Web API call (modals, `respond_url`, `publish_home`).
- [`http`](./http) — fallback for APIs not yet wrapped in a typed connector.
- [Built-in Connectors](/connectors/) — what `module` resolves to.
