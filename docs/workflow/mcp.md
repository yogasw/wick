---
outline: deep
---

# MCP authoring

Every workflow primitive is reachable from an LLM via the [wick MCP server](/guide/mcp). The catalog is **self-documenting**: every executor declares its schema next to its `Execute` method, so adding a new node type makes it appear in `workflow_node_types` and the canvas palette automatically — no separate registration.

The full op catalog with destructive flags and per-op semantics is the [Workflow connector page](/connectors/workflow). This page covers the **authoring flow** — how to drive that surface end-to-end.

## Recommended sequence

When an LLM creates a workflow from scratch:

```
workflow_workspace            ← entry point, returns base_dir + node_types + trigger_types + templates
  → workflow_node_types        ← know what types are available
  → workflow_check_name        ← surface conflicts up front
  → workflow_create            ← scaffold from template
  → workflow_add_node × N      ← validates type + schema per node
  → workflow_connect × N       ← add edges, with case: for branch sources
  → workflow_validate          ← static + schema + cycle + guard dry-run
  → workflow_simulate          ← dry-run with synthetic event
  → workflow_test              ← if __tests__/ fixtures exist
  → workflow_request_review    ← admin enables manually
```

After every edit (`workflow_add_node`, `workflow_connect`, `workflow_set_triggers`, `workflow_update_node`, …) the change lands in the workflow's draft slot. Call `workflow_publish` to promote — and **always ask the user first**.

## Two ways to edit the same workflow

The canvas and MCP write through the same engine validator. The LLM scaffolds a graph over MCP, you tighten it in the canvas, both go through the validator before the body lands in the draft slot of the `workflows` table.

```
Canvas ─┐
         ├─→ engine validator ─→ body_draft (DB) ─→ publish ─→ body_published (DB)
MCP op ─┘
```

No separate "AI mode" / "canvas mode". The validator is the source of truth; both paths fail the same way on a broken edge or schema mismatch. Every save also appends a row to `workflow_versions` for history + compare + restore.

## Discovery ops

These are the read-only ops the LLM should call **before** any edit, in this order:

| Op | Why |
|---|---|
| `workflow_workspace` | One call returns base directory, every node type, every trigger type, every workflow template. Most efficient way to orient. |
| `workflow_node_types` | Full schema + example body + `when_to_use` per node type — what the LLM needs before `workflow_add_node`. |
| `workflow_trigger_types` | Same for triggers. |
| `workflow_integration` | Per-channel event + action catalog with `match_schema` / `payload_schema` / `input_schema` / `output_schema`. Use to know exact filter shapes for `trigger.match` and exact arg shapes for `channel` nodes. |
| `workflow_connectors` | Every connector module + its operations — needed for `connector` nodes. |
| `workflow_skills`, `workflow_providers` | Available skills + provider instances for `agent` nodes. |

## Diagnostics ops

| Op | Use |
|---|---|
| `workflow_describe` | Human-readable summary: triggers, graph shape, deps, dangling-edge + template-reference warnings. **Call before editing** to orient on an existing workflow. |
| `workflow_validate` | Parse + validate. Errors decorated with `did_you_mean` / hint pointers when wick recognises the failure (lowercase key, misspelt match field, picker scalar vs object shape). |
| `workflow_template_test` | Render a Go template against a synthetic context. On missing-key errors, lists available keys at the offending path plus did-you-mean. Sample events: `slack.message`, `slack.block_action`, `slack.view_submission`, `cron`. |
| `workflow_picker_resolve` | Resolve a picker source (e.g. `slack.channels`) to `[{id, name}]`. Use when populating Match filter picker fields so the LLM passes valid IDs instead of guessing. |
| `workflow_simulate` | Dry-run with a synthetic event. No state persisted, no external calls. Returns per-node outputs + `path_taken` + `final_result`. |
| `workflow_get_run_log` (with `diagnose=true`) | One-shot summary of a failed run — classifies the error (`template_missing_key`, `channel_action_missing`, `connector_op_missing`, `secret_leak`, `branch_no_edge`, `agent_session_invalid`, `provider_skill_missing`) and surfaces available keys + suggested fix. |

## Test fixtures

Test cases live in the `workflow_test_cases` table — each case has a slug-safe `name`, a JSON `body` with the pinned event + per-node assertions. Author through these ops:

- `workflow_save_test_case` — create or update one case by name.
- `workflow_list_test_cases` — list case names + assertion counts.
- `workflow_record_test` — capture a real run's event + per-node outputs into a fresh case.
- `workflow_capture_fixture` — snapshot one node's output as a unit-test case.

The `workflow_test` runner re-executes the graph against the recorded payload and reports `[{case, pass, error, diff}]` plus coverage.

## History, compare, restore

Every save appends to `workflow_versions`. AI surface:

- `workflow_versions` — list snapshots newest first; metadata only (kind, message, created_at, by).
- `workflow_version_detail` — fetch one snapshot's full body.
- `workflow_diff_versions` — return two snapshots' bodies side-by-side for diff rendering.
- `workflow_restore_version` — copy a snapshot back into the draft slot (no auto-publish).

## Canvas lock + guard

- `workflow_lock` — freeze edits on a workflow. The engine ignores the flag (runs continue); every edit op rejects the write while locked. Use for production workflows.
- `workflow_guard` — separate from `workflow_validate`. Runs the safety rule pack (destructive shell, secret leak, unparameterized SQL, network allowlist, prompt injection patterns) and returns `{ok, violations[], content_hash}`. Call before `workflow_publish` on anything that touches data egress.

## Execute step

- `workflow_exec_node` — run one node in isolation (n8n's "Execute step" pattern). Pass the node JSON, optional `input`, optional `event`, optional `node_outputs` map keyed by upstream node id. Returns `{ok, latency_ms, output}`. Nothing persists to runs/.

## Watching live runs

`workflow_watch` is the streaming side: bounded read over recent runs returning only `[run_id, workflow_id, status, started_at, ended_at, trigger_id]`. With `wait_seconds > 0` (server caps at 30s) it subscribes to the live event stream and returns the moment `expect` / `stop_on_first` is met — cheaper than `workflow_get_run` for the "what happened on the last cron tick?" pattern.

Multi-dim filter: `workflow_id`, `trigger_id`, `node_id`, `status`, `since`.

## See also

- [Built-in Workflow connector](/connectors/workflow) — full op catalog with destructive flags + per-op input details.
- [MCP for LLMs](/guide/mcp) — transport layer (`wick_list` / `wick_execute`).
- [Canvas editor](./canvas) — visual counterpart to the MCP authoring flow.
