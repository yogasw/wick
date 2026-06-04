---
outline: deep
---

# Run state

The workflow body and test fixtures live in the database. Per-run artefacts (state snapshots, event logs, env values) stay on disk — they're high-volume append-friendly data the engine writes during execution.

## Where things live

| | Storage | Notes |
|---|---|---|
| Workflow body (published + draft) | DB column `workflows.body_published` / `body_draft` | JSON text |
| Version history | DB table `workflow_versions` | Append-only; powers History tab + compare + restore |
| Test fixtures | DB table `workflow_test_cases` | Name-addressable, no file paths |
| Run state | Disk `runs/<run-id>/state.json` | Engine writes per run |
| Run events | Disk `runs/<run-id>/events.jsonl` | One line per `node_started` / `node_completed` / `node_failed` / `edge_traversed` |
| Env values | Disk `env.json` | Sensitive secrets, OS-perm protected |

Folder layout under `<BaseDir>/workflows/<workflow-id>/`:

```
runs/
└── <run-id>/
    ├── state.json     ← run summary + per-node outputs
    └── events.jsonl   ← full event stream
env.json               ← per-workflow env values (when set)
```

`workflow_get_run` reads `state.json`. `workflow_get_run_events` reads `events.jsonl` — reach for it when `state.json` doesn't carry enough detail (e.g. you have a failed run ID and need to see what fired last).

Retention is per-workflow (default: keep last 50 runs on disk). Older runs are pruned at boot; the cleanup is best-effort and a manual delete of `runs/<id>/` is always safe.

## Replay

Open a run in the timeline → click **Rerun** → navigates to the manual runner pre-filled with the original payload. There is no one-click replay-and-execute; you review before re-running.

This is the same pattern as the [connector test panel](/guide/connector-module#test-panel-postman-style). Rationale: an LLM-driven workflow might have side-effected on a downstream API; blindly re-running can double-fire. Manual review + Run keeps the human in the loop.

From MCP, `workflow_replay_run` is the equivalent convenience wrapper — loads `RunState.Event` from `run_id` and calls `workflow_run_now`. Returns a new `run_id`.

## Copy to editor

The **Copy to editor** button (timeline + the MCP `workflow_copy_run_to_editor` op) does two things in one call:

1. Loads the run state.
2. Saves the current published workflow as draft (so you can edit without overwriting prod).

The response also returns the run's per-node outputs. Pass them as `node_outputs` to `workflow_exec_node` when you want Execute Step to prefill from the original run's data.

Use this when iterating on a workflow against real-world failure data: load the failed run → edit the graph → exec-node with the captured outputs → publish when it's right.

## See also

- [Canvas editor ▶ Run timeline](./canvas#run-timeline) — the UI that reads this layout.
- [MCP authoring ▶ Watching live runs](./mcp#watching-live-runs) — streaming side over MCP.
