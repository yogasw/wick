---
outline: deep
---

# `switch`

Multi-case branching. First rule whose `when` is true wins; emits `verdict = case` so downstream edges route by `case:`.

| | |
|---|---|
| **Source** | [`internal/agents/workflow/nodes/switch.go`](https://github.com/yogasw/wick/blob/master/internal/agents/workflow/nodes/switch.go) |
| **Branching** | Yes — emits `verdict` |
| **When to use** | Routing with 2+ conditions where [`branch`](./branch) (single expr) is awkward. Each rule is independent; ordering matters (first match wins). |

## Schema

| Field | Type | Required | Notes |
|---|---|---|---|
| `cases` | list of `{when, case}` | ✅ | First rule whose `when` is true wins. UI uses a rows builder; YAML accepts the list directly. |
| `default_case` | string | | Verdict to emit when no rule matches. Leave blank to **fail closed** — the run errors. |

## Output

| Field | Type | What |
|---|---|---|
| `verdict` | string | Winning case label (or `default_case` fallback). |

## Example

```yaml
- id: route
  type: switch
  cases:
    - when: '{{index .Event.Payload "status"}} == "approved"'
      case: approve
    - when: '{{index .Event.Payload "status"}} == "rejected"'
      case: reject
  default_case: review
```

## Matching semantics

Same as [`branch`](./branch#operator-behavior) — binary ops or truthy string. Each rule is evaluated independently in order; the first `true` short-circuits.

Empty `default_case` with no match = error (fail closed) so the workflow halts instead of silently dropping the run.

## Pair with

- [`branch`](./branch) — single-expression sibling.
- [`classify`](./classify) — produces an LLM verdict that switch can re-route.
