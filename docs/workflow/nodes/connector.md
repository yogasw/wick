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
| `args` | YAML map (templated) | | Per-op input. Field set comes from the connector's `Input` struct — see [Connector Module ▶ Per-op Input](/guide/connector-module#per-operation-input-structs). |
| `arg_modes` | YAML map | | Per-arg `fixed` / `expression`. Defaults to `fixed`; mark as `expression` to render the value as a Go template. |

## Output

Whatever the connector op returns — typically a typed Go struct serialised as JSON. The fields are merged into `.Node.<id>.*` for direct template access.

## Example

File a GitHub issue from a Slack thread:

```yaml
- id: file_bug
  type: connector
  module: github
  op: create_issue
  arg_modes:
    title: expression
    body: expression
  args:
    owner: abc
    repo: web
    title: "{{.Node.classify.parsed.summary}}"
    body: |
      Reported in Slack by <@{{.Node.trigger.payload.user}}>:

      {{.Node.trigger.payload.text}}
    labels: bug,from-slack
```

## Tag visibility

The runtime calls `wick_execute` under the hood, so the same tag-filter rule applies — the workflow caller must have visibility on the (module, op) pair. Workflow runs as the user who triggered the run (channel-bound) or the system user (cron / webhook); align tags accordingly.

## Pair with

- [`channel`](./channel) — for Slack actions that aren't 1:1 with a Web API call (modals, `respond_url`, `publish_home`).
- [`http`](./http) — fallback for APIs not yet wrapped in a typed connector.
- [Built-in Connectors](/connectors/) — what `module` resolves to.
