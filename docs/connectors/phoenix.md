---
outline: deep
---

# Phoenix

`phoenix` wraps the [Arize Phoenix](https://phoenix.arize.com/) observability API for **debugging LLM behaviour**. One instance = one Phoenix project (base URL + API token + project id).

Operations are **read-only**: they list LLM spans for a room or an app and drill into a single span's full prompt / messages / tool calls / token usage — the signals needed to answer "why did the agent answer X" without ever mutating Phoenix data.

| | |
|---|---|
| **Source** | [`internal/connectors/phoenix/`](https://github.com/yogasw/wick/tree/master/internal/connectors/phoenix) |
| **Key** | `phoenix` |
| **Icon** | 🔥 |
| **Tier** | builtin (every wick app) |

## Configs

| Field | Type | Required | Notes |
|---|---|---|---|
| `BaseURL` | URL | ✅ | Phoenix base URL — scheme + host only, no `/graphql` path. |
| `APIToken` | secret | ✅ | Phoenix API token (JWT). Sent as a `Bearer` token on every request. |
| `ProjectID` | text | ✅ | Phoenix project global id in base64 form, e.g. `UHJvamVjdDoyOA==` (decodes to `Project:28`). Copy it from the project URL or the GraphQL API. |

All traffic goes through the single GraphQL endpoint (`BaseURL` + `/graphql`).

## Operations

| Op | Destructive | Input | What it does |
|---|---|---|---|
| `list_spans_by_room` | no | `room_id`, `time_range`, `llm_only` | List LLM spans for a room (Phoenix session). Returns per-span model, status, tokens, latency, and previews of system prompt / last user input / output. |
| `list_spans_by_app` | no | `app_id`, `time_range`, `max_spans`, `root_only` | List root spans by `metadata['app_id']`, paged server-side. Previews come from raw input/output. |
| `get_span` | no | `span_node_id` | Full detail of one span: every message (system, user, tool), the **tool catalog** the model could choose from, output, `tool_calls`, token usage, `invocation_parameters`, span `metadata`, latency, cost. |

All three are read-only — there are no destructive ops on this connector.

### What `get_span` returns

Beyond the message list and usage, `get_span` surfaces the rest of the span's `attributes` blob so the full "what was sent to the model" picture is visible:

| Field | What it carries |
|---|---|
| `tools` | The **catalog** of tools the model could choose from — each with `name`, `description` (which holds the selection preconditions), and `parameters` (raw JSON schema). Absent when the model was bound no tools. This is *distinct* from a message's `tool_calls` (what the model actually invoked) — compare the two to see whether it picked the right tool. |
| `invocation_parameters` | Model call settings (`temperature`, `reasoning_effort`, `tool_choice`, …). The redundant `tools` array is stripped — read the `tools` field instead. |
| `metadata` | Arbitrary span metadata emitted by the application that produced the span — e.g. `request_id`, `room_id`, `user_id`, `app_id`, the producing node. Keys vary by producer; the handles for cross-referencing logs / DB. |
| `tokens.cache_read`, `tokens.reasoning` | Prompt-cache and reasoning token breakdown, when the provider reports them. |

## Typical debugging flow

1. **Find the span.** Call `list_spans_by_room` with the room id (matched against the Phoenix *session id* for that conversation). Scan the previews to spot the span that answered wrong.
2. **Drill in.** Take that span's `span_node_id` and call `get_span` — read the system prompt, the user turn, the `tools` catalog the model was given, and which `tool_calls` it actually fired. Comparing the catalog against the call is how you tell *why* the model picked (or ignored) a tool.
3. **Widen if needed.** No room id, only an app? Start from `list_spans_by_app` and drill into a `span_node_id` the same way.

```
list_spans_by_room(room_id) → pick span_node_id → get_span(span_node_id)
```

## Quirks worth knowing

- `room_id` is matched against the Phoenix **session id**, not a metadata filter — the room id value cannot be filtered any other way.
- `list_spans_by_room` walks sessions → traces → spans, so a busy room costs several GraphQL round-trips.
- `llm_only` defaults to **true**. Set it false to also see chain / agent / tool spans.
- `list_spans_by_app` filters on `metadata['app_id']` — only **string** metadata is filterable in Phoenix; `root_only` defaults to true.
- The handle `get_span` needs is the **`span_node_id`** (base64 global id), not the hex `span_id`.
- `tool_calls` are parsed from the OpenInference `message.tool_calls[].tool_call` envelope; `arguments` stays as the raw JSON string the model emitted.
- Span `attributes` arrive as a stringified JSON blob with a **nested** shape (`attrs.llm.input_messages[].message`); `spanKind` is lowercase over GraphQL.

## See also

- [Connector Module](/guide/connector-module) — module contract.
- Loki connector (`loki`) — server-log side of an investigation; pairs with Phoenix spans for full-stack debugging.
