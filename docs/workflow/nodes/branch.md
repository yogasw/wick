---
outline: deep
---

# `branch`

Single Go-template expression → verdict. The engine routes to the outgoing edge whose `case:` matches.

| | |
|---|---|
| **Source** | [`internal/agents/workflow/nodes/branch.go`](https://github.com/yogasw/wick/blob/master/internal/agents/workflow/nodes/branch.go) |
| **Branching** | Yes — emits `verdict` |
| **When to use** | Routing logic is structured (no natural language). |

## Schema

| Field | Type | Required | Notes |
|---|---|---|---|
| `expr` | template | ✅ | Go template expression that returns a case-label string matching downstream edge `case:` values. |

## Output

| Field | Type | What |
|---|---|---|
| `verdict` | string | Resolved case label. Engine filters outgoing edges by `edge.case == verdict`. |
| `result` | string | Same value as `verdict` — kept for downstream nodes that prefer the `.result` reference. |

## Examples

Route by upstream classify:

```yaml
- id: route
  type: branch
  expr: '{{.Node.triage.verdict}}'
```

Boolean check:

```yaml
- id: vip_check
  type: branch
  expr: '{{.Node.user_lookup.profile.is_admin}} == true'
```

## Operator behavior

- Binary operators (`==`, `!=`, `<`, `<=`, `>`, `>=`) auto-detected — verdict becomes `"true"` / `"false"`.
- Without an operator, the rendered string **is** the verdict. Match against `case:` labels exactly (case-sensitive).
- Numeric compare auto-detects when both sides parse as numbers; falls back to string compare otherwise.

Engine routes to one matching edge. If no edge matches, the run dead-ends — add a default edge for catch-all.

## Pair with

- [`classify`](./classify) — produces a verdict you can route here.
- [`switch`](./switch) — multi-rule alternative when one expression is awkward.
- [`end`](./end) — explicit terminator for branches that should not chain further.

## Common pitfalls

- Forgetting the `case:` label on an outgoing edge — without it, the edge never fires.
- Multi-line template in `expr` — branch expects a single short expression.
