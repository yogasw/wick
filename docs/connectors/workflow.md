---
outline: deep
---

# Workflow

`workflow` exposes the workflow engine itself as a connector. Every operation in the workflow design (Tier 1 / 2 / 3) is reachable via `wick_execute` so any MCP-aware LLM — Claude Desktop, ChatGPT, Gemini — can introspect node types, scaffold a graph, edit files, validate, simulate, run tests, replay, and request review without native file access.

This is the LLM-facing surface for the [Workflows](/workflow/) feature. If you're building a workflow in the canvas, you don't touch these directly — the canvas calls them under the hood. If you're driving wick from outside (Claude Desktop, a custom MCP client), these are the ops you'll see in `wick_list`.

| | |
|---|---|
| **Source** | [`internal/connectors/workflow/`](https://github.com/yogasw/wick/tree/master/internal/connectors/workflow) |
| **Key** | `workflow` |
| **Icon** | ⚙ |
| **Tier** | runtime (wired with the workflow engine at boot) |
| **Fixed** | ✅ — single row, no per-instance credentials |

## Configs

Empty (`type Configs struct{}`). The connector talks to the in-process workflow engine, not an external API. The row exists so it appears in `/manager/connectors/workflow/{id}` for tag-based access control + audit log.

## Operation catalog

Operations are grouped by **tier** — Tier 1 is read-only introspection (every LLM can call freely), Tier 2 is graph editing, Tier 3 is running + tests. Destructive ops are flagged.

### Tier 1 — introspection (read-only)

| Op | Purpose |
|---|---|
| `workflow_workspace` | Entry point. Returns `{base_dir, node_types[], trigger_types[], templates[]}`. **Always call this first** to orient the LLM. |
| `workflow_node_types` | All node types with schema + example YAML + `when_to_use`. |
| `workflow_trigger_types` | All trigger types with schema + example. |
| `workflow_channels` | Configured channel integrations + trigger/action schemas (legacy specs). |
| `workflow_integration` | **Full** per-channel event + action catalog from the integration registry — `match_schema`, `payload_schema`, `input_schema` / `output_schema` per action, destructive flag. More complete than `workflow_channels`. |
| `workflow_node_detail` | Per-type detail — input schema, output fields, examples, pair-with hints, common pitfalls. |
| `workflow_connectors` | Every connector module + its operations (for `type:connector` nodes). |
| `workflow_skills` | AI provider skills available for `type:agent` nodes (optional `provider` filter). |
| `workflow_providers` | Configured AI providers (claude / codex / gemini) with capabilities + status. |
| `workflow_list` | Every workflow with id, name, enabled, version. |
| `workflow_check_name` | Returns `{available, conflict_id}`. Call before `workflow_create` so the LLM surfaces a friendly error. |
| `workflow_get` | Full workflow definition: triggers, graph, env schema. |
| `workflow_list_files` | Files in the workflow folder (`workflow.yaml`, `nodes/*.md`, `__tests__/`, …). |
| `workflow_read_file` | Content of one file. Replaces native file tool for remote AI. |

### Tier 2 — editing (mostly idempotent, some destructive)

| Op | Destructive | Purpose |
|---|---|---|
| `workflow_create` | no | Scaffold a new workflow from a template (`empty`, `support-triage`, `incident-response`, `daily-digest`). New workflows are **disabled** — admin enables explicitly. |
| `workflow_write_file` | no | Write any file in the workflow folder. |
| `workflow_delete_file` | **yes** | Delete one file. |
| `workflow_delete` | **yes** | Delete the whole workflow folder + unregister scheduled triggers. |
| `workflow_add_node` | no | Add a node via declarative patch — validates type + schema. |
| `workflow_update_node` | no | Merge-patch one node's fields (`prompt`, `config`, `on_failure`, …). |
| `workflow_delete_node` | **yes** | Remove a node and every edge that references it. |
| `workflow_connect` | no | Add an edge. Pass `case` label for `classify` / `branch` / `switch` sources. |
| `workflow_disconnect` | no | Remove an edge. |
| `workflow_move_node` | no | Update canvas position (`x, y` pixels). No effect on execution. |
| `workflow_set_triggers` | no | Replace the trigger list. |
| `workflow_toggle` | no | Enable / disable. Disabled workflows skip cron/channel/webhook but still respond to `workflow_run_now`. |
| `workflow_publish` | no | Promote `workflow.draft.yaml` → `workflow.yaml` and re-register the workflow with the router. **Required after every edit**. The connector description tells the LLM to ALWAYS ask the user before publishing edits. |
| `workflow_discard_draft` | no | Throw away the draft and revert to the published version. |
| `workflow_has_draft` | no | `{has_draft: bool}`. |

### Tier 3 — running + tests + diagnostics

| Op | Destructive | Purpose |
|---|---|---|
| `workflow_validate` | no | Parse + validate: cycle detect, schema check, guard dry-run. Errors decorated with `did_you_mean` / hint pointers (lowercase key, misspelt match field, picker scalar vs object shape, …). |
| `workflow_template_test` | no | Render a Go template against a synthetic context. On missing-key errors, lists available keys + did-you-mean. Sample events: `slack.message`, `slack.block_action`, `slack.view_submission`, `cron`. |
| `workflow_picker_resolve` | no | Resolve a picker source (e.g. `slack.channels`) to `[{id, name}]`. Use when populating Match filter picker fields so AI passes valid IDs instead of guessing. |
| `workflow_describe` | no | Human-readable summary: triggers, graph shape, dependencies (channels/connectors/providers), dangling-edge + template-reference warnings. **Call before editing** to orient the LLM. |
| `workflow_simulate` | no | Dry-run with a synthetic event. No state persisted, no external calls. Returns per-node outputs + `path_taken` + `final_result`. |
| `workflow_test` | no | Run `__tests__/` fixtures (optional name-prefix filter). Returns `[{case, pass, error, diff}]` + coverage summary. Requires runner-enabled module. |
| `workflow_test_coverage` | no | Which nodes were hit and which are untested across all fixtures. |
| `workflow_record_test` | no | Generate a `__tests__/` fixture by capturing a real run's event + per-node outputs. |
| `workflow_capture_fixture` | no | Snapshot one node's output as a unit-test fixture in `__tests__/nodes/`. |
| `workflow_run_now` | no | Enqueue a manual run (bypasses `Enabled` check). Returns `{run_id}`. |
| `workflow_get_runs` | no | Recent run IDs + status + `started_at` (default limit 20). |
| `workflow_get_run` | no | Full run state — node outputs, events, `path_taken`, status, cost. |
| `workflow_get_run_events` | no | Raw `events.jsonl` stream for a run — every `node_started` / `node_completed` / `node_failed` / `edge_traversed` entry. |
| `workflow_watch` | no | Bounded read over recent runs. `wait_seconds > 0` subscribes to the live event stream and returns when `expect` / `stop_on_first` is met (server caps at 30s). Multi-dim filter — `workflow_id`, `trigger_id`, `node_id`, `status`, `since`. |
| `workflow_get_run_log` | no | One-shot summary of a run — status, error, completed/failed/skipped nodes, per-node duration, total duration. Pass `diagnose=true` to classify the error (`template_missing_key`, `channel_action_missing`, `connector_op_missing`, `secret_leak`, `branch_no_edge`, `agent_session_invalid`, `provider_skill_missing`) and surface available keys + suggested fix. |
| `workflow_copy_run_to_editor` | no | UI parity with the **Copy to editor** button — loads run state, saves the current published workflow as draft, writes `runs/<run_id>/mocks.json` so the Execute step can prefill from real data. |
| `workflow_replay_run` | no | Re-enqueue a run with the same trigger event as a past run. |
| `workflow_list_test_cases` | no | `__tests__/*.json` fixtures with name + assertion count + last result. |
| `workflow_save_test_case` | no | Create or update one `__tests__/<name>.json` fixture (slug-safe names only). |
| `workflow_delete_test_case` | **yes** | Delete one fixture. |
| `workflow_request_review` | no | Notify admin that the workflow is ready for approval. Workflow stays disabled until admin enables it. Returns `{url}`. |

## Authoring flow from MCP

Recommended sequence when an LLM creates a workflow from scratch:

```
workflow_workspace
  → workflow_node_types
  → workflow_check_name        ← surface conflicts up front
  → workflow_create
  → workflow_add_node × N
  → workflow_connect × N
  → workflow_validate          ← static + schema checks
  → workflow_simulate          ← dry-run with synthetic event
  → workflow_test              ← if __tests__/ fixtures exist
  → workflow_request_review    ← admin enables manually
```

After every edit (`workflow_add_node`, `workflow_connect`, `workflow_write_file workflow.yaml`, …) the change lands in `workflow.draft.yaml`. Call `workflow_publish` to promote — and always ask the user first.

## See also

- [Workflows](/workflow/) — the user-facing feature this connector drives.
- [Connector Module](/guide/connector-module) — the contract this connector reuses.
- [Wick Manager](./wickmanager) — sibling connector for inspecting jobs / tools / lifecycle.
