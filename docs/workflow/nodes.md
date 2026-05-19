---
outline: deep
---

# Node catalog

Every workflow primitive is a typed node. Schema for each type is **self-documenting** via the executor's `Descriptor()` — the canvas inspector, MCP `workflow_node_types`, and these docs all read the same source.

When a new node type lands in `internal/agents/workflow/nodes/`, add a page under `docs/workflow/nodes/` mirroring the schema + output map from the descriptor. The [`workflow-node-module` skill](https://github.com/yogasw/wick/tree/master/.claude/skills/workflow-node-module) is the authoring guide for the executor side.

## Index

### Logic & routing

| Type | What it does |
|---|---|
| [`classify`](./nodes/classify) | LLM picks one of `output_cases` for free-text input. Verdict drives outgoing edges by `case:`. |
| [`branch`](./nodes/branch) | Single Go-template expression → verdict. Routes to the edge whose `case:` matches. |
| [`switch`](./nodes/switch) | Multi-rule list. First rule whose `when` is true wins. |
| [`transform`](./nodes/transform) | Pure data shaping — gotemplate / jsonpath / jq. No I/O. |
| [`end`](./nodes/end) | Explicit terminator with a final result expression. |

### Execution

| Type | What it does |
|---|---|
| [`agent`](./nodes/agent) | Spawn an agent turn through the existing pool. Templated prompt, optional skills + tools whitelist. |
| [`session_init`](./nodes/session_init) | First-turn context injection for agent nodes — mirrors the channel session-context pattern. |
| [`shell`](./nodes/shell) | Run a local command. Captures stdout / stderr / exit_code. Same gate policy as agents. |
| [`go_script`](./nodes/go-script) | Run a Go program under the yaegi interpreter. Stdin = run context JSON, stdout = result JSON. |

### Integrations

| Type | What it does |
|---|---|
| [`connector`](./nodes/connector) | Call one operation on a registered connector row. Same code path as MCP `wick_execute`. |
| [`channel`](./nodes/channel) | Call a channel action (Slack `send_message`, `open_modal`, `add_reaction`, …) without going through an agent. |
| [`http`](./nodes/http) | Outbound HTTP call. Templated URL / headers / query / body. Retry + `parse_response: raw\|json\|bytes`. |
| [`db_query`](./nodes/db-query) | Parameterized SQL against a configured DSN. Returns `rows` + `row_count` + `columns`. |

### State

| Type | What it does |
|---|---|
| [`dataset_get`](./nodes/dataset) | Load one row by primary key. Branches on found/not_found. |
| [`dataset_exists`](./nodes/dataset) | Check whether any row matches. Branches on true/false. |
| [`dataset_query`](./nodes/dataset) | Multi-row search with where / order_by / limit. |
| [`dataset_count`](./nodes/dataset) | Count without loading. |
| [`dataset_insert`](./nodes/dataset) | Insert; fail on PK conflict. |
| [`dataset_upsert`](./nodes/dataset) | Insert or update; returns `action: insert|update`. |
| [`dataset_delete`](./nodes/dataset) | Delete rows matching where. |

## Render context

Every node's templated fields render against the run's render context. Top-level keys:

| Key | What |
|---|---|
| `.Event` | Trigger payload — `.Event.Payload`, `.Event.User`, `.Event.Channel`, etc. Per-trigger shape; see [Triggers](./triggers). |
| `.Node.<id>` | Output of an upstream node. Object keys are merged so `.Node.classify.verdict`, `.Node.classify.confidence` work directly. |
| `.Env` | Workflow env block. Resolves `env:` references with secret-leak guard. |
| `.Run` | Run-scoped metadata: `.Run.id`, `.Run.workflow_id`, `.Run.started_at`, `.Run.final_result`. |
| `.Workflow` | Workflow metadata: `.Workflow.id`, `.Workflow.name`, `.Workflow.version`. |

Template engine = Go's `text/template` with wick-specific helpers (`jsonEscape`, `upper`, `lower`, `default`, …). Test a template against a synthetic context with `workflow_template_test` — on missing-key errors it lists available keys at the offending path plus a did-you-mean hint.

## Output shape convention

Every node sets at minimum `result` (string for routing) and may add typed fields. When `result` is an object the fields are merged into `.Node.<id>.*` for direct template access. Branching nodes (`classify`, `branch`, `switch`) emit `verdict` — the engine filters outgoing edges by `edge.case == verdict`.

## See also

- [Triggers](./triggers) — what populates `.Event`.
- [MCP authoring](./mcp) — `workflow_node_types` + `workflow_node_detail` ops the LLM uses to discover this same catalog.
- [Canvas editor](./canvas) — visual palette of these types.
