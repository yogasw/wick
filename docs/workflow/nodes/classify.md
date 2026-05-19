---
outline: deep
---

# `classify`

Ask an LLM to bucket free-text input into one of a fixed set of cases. The verdict drives outgoing edges by `case:`.

| | |
|---|---|
| **Source** | [`internal/agents/workflow/nodes/classify.go`](https://github.com/yogasw/wick/blob/master/internal/agents/workflow/nodes/classify.go) |
| **Branching** | Yes — emits `verdict` |
| **When to use** | Input is free text and needs to be routed into a small set of cases. |

## Schema

| Field | Type | Required | Notes |
|---|---|---|---|
| `output_cases` | list (YAML) | ✅ | Enum labels the LLM must pick from. Each becomes a JSON Schema enum value passed to the provider's structured output. |
| `input` | template | ✅ | Text to classify. Use a template expression like <code v-pre>{{index .Event.Payload "text"}}</code>. |
| `provider` | string | | Provider name. Optional — falls back to the default. |
| `prompt_file` | path (template) | | Optional prompt file path to override the built-in classify prompt. |
| `fuzzy_match` | bool | | Allow Levenshtein / substring fallback when the model returns a variant (e.g. `"bugs"` for `bug`). |
| `retry_on_mismatch` | int | | Retry count when the LLM returns an unrecognized label. Each retry tightens the system prompt — costs tokens. Keep ≤ 2. |

## Output

| Field | Type | What |
|---|---|---|
| `verdict` | string | Matched `output_cases` label. Edge `case:` filters route on this. Falls back to `"default"` after retries fail. |
| `confidence` | float | 0.0–1.0 score from the provider's structured output. 0 when the provider didn't return one. |
| `reasoning` | string | Short explanation. Useful as Slack reply or audit; not a routing input. |
| `raw` | any | Raw provider response — debugging only. |
| `fuzzy` | bool | `true` when verdict resolved via fuzzy match instead of exact. |

## Example

```yaml
- id: triage
  type: classify
  output_cases: [bug, feature, question]
  input: '{{index .Event.Payload "text"}}'
  provider: claude
```

## Reliability stack

Six layers, in order:

1. **Structured output** — provider returns one of `output_cases` directly.
2. **Normalize** — strip whitespace, lowercase, collapse to enum.
3. **Exact match** vs `output_cases`.
4. **Fuzzy match** (if `fuzzy_match: true`) — Levenshtein + substring.
5. **Retry on mismatch** — re-prompt with a tightened system message.
6. **Confidence threshold fallback** → emit `"default"` so the downstream branch can catch it.

Add a `"default"` case in your downstream branch to handle the "model gave up" path — otherwise the run dead-ends.

## Pair with

- [`branch`](./branch) / [`switch`](./switch) — route the verdict.
- [`agent`](./agent) — hand the input to a more capable model once routed.

## Common pitfalls

- Forgetting a `"default"` edge → run dead-ends when the LLM can't decide.
- High `retry_on_mismatch` (>2) — burns tokens for diminishing returns. Better to widen `output_cases` or add `fuzzy_match`.
