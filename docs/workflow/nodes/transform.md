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
| `jq` | ✅ | Full [jq](https://jqlang.github.io/jq/) via [`gojq`](https://github.com/itchyny/gojq). The `expression` is a jq program. |

For anything beyond a one-line reshape, reach for [`go_script`](./go-script) instead — it gives you real Go with full stdlib.

## jq engine

Set `engine: jq` and put a jq program in `expression`. The program runs against the JSON parsed from `input` — or, when `input` is blank, against the full render context marshalled to JSON.

```yaml
- id: shape
  type: transform
  engine: jq
  input: '{{.Node.fetch.body}}'
  expression: '{items: [.data[] | {id, name}]}'
```

```yaml
- id: active_only
  type: transform
  engine: jq
  input: '{{.Node.list.body}}'
  expression: '.[] | select(.status == "active") | {id, name}'
```

Output rules:

- **One result** → `result` holds that value bare.
- **Multiple results** (a stream, like the `active_only` example above) → `result` is an array of them.
- **No result** → `result` is `null`.

A jq compile error, non-JSON `input`, or a runtime error fails the node — the message names the offending program or input.

## Pair with

- [`go_script`](./go-script) — when `gotemplate` runs out of expressiveness.
- [`http`](./http), [`connector`](./connector) — typical upstream of a transform.
