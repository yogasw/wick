---
outline: deep
---

# `go_script`

Run a Go program under the [yaegi](https://github.com/traefik/yaegi) interpreter. Stdin is the run context JSON, stdout is the result JSON.

| | |
|---|---|
| **Source** | [`internal/agents/workflow/nodes/go_script.go`](https://github.com/yogasw/wick/blob/master/internal/agents/workflow/nodes/go_script.go) |
| **When to use** | Logic that needs real Go — string manipulation, math, JSON shaping, custom predicates. Use [`http`](./http) / [`transform`](./transform) for I/O; this node is pure compute. |

## Schema

| Field | Type | Required | Notes |
|---|---|---|---|
| `code` | textarea (template) | ✅ | Go source. Standard library available; no third-party imports. |
| `timeout_sec` | int | | Per-call timeout. Default 30. |

## Output

| Field | Type | What |
|---|---|---|
| `result` | any | JSON value parsed from script stdout. When `result` is an object, its keys are merged into `.Node.<id>.*` for direct template access. |
| `stderr` | string | Captured stderr. Useful for debugging. |

## Example

```yaml
- id: shape_payload
  type: go_script
  code: |
    package main
    import ("encoding/json"; "os")
    func main() {
      var ctx map[string]any
      json.NewDecoder(os.Stdin).Decode(&ctx)
      ev := ctx["Event"].(map[string]any)["Payload"].(map[string]any)
      json.NewEncoder(os.Stdout).Encode(map[string]any{
        "upper": ev["text"],
      })
    }
```

Downstream nodes can then reach <code v-pre>{{.Node.shape_payload.upper}}</code>.

## Why yaegi, not exec

The interpreter runs in-process — no spawn cost, no PATH dependency, no PR for a missing Go toolchain. The cost: stdlib only, slightly slower than compiled Go. For long-running compute, write a connector or job instead.

## Pair with

- [`transform`](./transform) — when a Go template would do.
- [`shell`](./shell) — when you actually do need to spawn a process.
