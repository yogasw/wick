---
outline: deep
---

# `end`

Terminator. Captures a final result template so <code v-pre>{{.Run.final_result}}</code>-style reads can pick it up.

| | |
|---|---|
| **Source** | [`internal/agents/workflow/nodes/end.go`](https://github.com/yogasw/wick/blob/master/internal/agents/workflow/nodes/end.go) |
| **When to use** | Explicit end-of-flow with a result payload. Implicit `end` is fine for fire-and-forget workflows — only declare it when you want a final value stored in run metadata. |

## Schema

| Field | Type | Required | Notes |
|---|---|---|---|
| `result` | template | | Final result expression. Rendered and stored in <code v-pre>{{.Run.final_result}}</code> on `meta.json`. |

## Output

`result` from the rendered template. When omitted, the node sets `result: ""`.

## Example

```yaml
- id: done
  type: end
  result: 'Resolved {{.Node.triage.verdict}} for {{.Event.User.Name}}'
```

## Implicit end

Any node with no outgoing edges acts as an end node. Declaring `type: end` is only needed when you want to capture a templated final result distinct from the last executed node's output.
