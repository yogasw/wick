---
outline: deep
---

# `transform`

Pure data shaping. No I/O — reshape between nodes via gotemplate / jsonpath / jq.

| | |
|---|---|
| **Source** | [`internal/agents/workflow/nodes/transform.go`](https://github.com/yogasw/wick/blob/master/internal/agents/workflow/nodes/transform.go) |
| **When to use** | Reshape data between nodes without spending an LLM turn. |

## Schema

| Field | Type | Required | Notes |
|---|---|---|---|
| `engine` | dropdown | ✅ | `gotemplate` (default) / `jsonpath` / `jq`. |
| `expression` | template | ✅ | Transform expression rendered against the run's render context. |
| `input` | template | | Optional input expression. Defaults to the full render context. |

## Output

| Field | Type | What |
|---|---|---|
| `result` | string | Rendered output. |

## Example

```yaml
- id: build
  type: transform
  engine: gotemplate
  expression: '{{index .Event.Payload "text" | upper}}'
```

## Engines

| Engine | Status | Notes |
|---|---|---|
| `gotemplate` | ✅ | Default. Same template engine + helpers as every other templated field. |
| `jsonpath` | ⚠️ placeholder | Minimal walker. Useful for trivial extractions; not full JSONPath. |
| `jq` | ❌ not implemented | Reserved — emits an error at runtime. |

For anything beyond a one-line reshape, reach for [`go_script`](./go-script) instead — it gives you real Go with full stdlib.

## Pair with

- [`go_script`](./go-script) — when `gotemplate` runs out of expressiveness.
- [`http`](./http), [`connector`](./connector) — typical upstream of a transform.
