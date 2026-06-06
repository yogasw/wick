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

## Inspecting a run

The executions view ([canvas ▶ run timeline](./canvas#run-timeline)) opens a detail panel for any run:

- **Events** — the full `events.jsonl` stream (`node_started` / `node_completed` / `node_failed` / `edge_traversed`) with timestamps, so you can see exactly which node fired last and how long each took.
- **Nodes** — per-node status (success / failed / running) and each node's output.
- **JSON preview** — the full run state (`state.json`) for the run.

A run can also be **deleted** from the panel — it removes that run's `runs/<run-id>/` folder. Active runs aren't touched; deletion is for clearing out finished history you no longer need, on top of the automatic 50-run retention above.

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

## Version history

The **History** tab at the bottom of the workflow editor lists saved snapshots from the `workflow_versions` table. Each row shows the version ID, kind (`draft` or `published`), author, timestamp, and optional message.

### What you can do

| Action | How |
|---|---|
| **View** | Click **view** on any row to open a read-only JSON preview of that snapshot. |
| **Restore** | Click **restore** to overwrite the current draft with the snapshot body. |
| **Delete** | Click the trash button on a row to permanently remove that snapshot. |
| **Clear all** | Click **Clear all** to remove every history snapshot for this workflow (requires confirmation). |
| **Compare** | Tick the checkboxes on exactly two rows, then click **Compare** to open a side-by-side colored diff. Unchanged lines can be collapsed with the **Diff only** toggle. |

### Auto-refresh and dedup

The History list refreshes automatically after every autosave. Autosaves are debounced at 2 s. If the workflow body has not changed since the last snapshot, no new draft entry is created (identical-body dedup).

### Retention

Draft snapshots are capped at 50 per workflow; the oldest are pruned automatically. Published snapshots are unbounded.

### REST endpoints

| Method | Path | Effect |
|---|---|---|
| `DELETE` | `/api/workflows/versions/{id}/{versionID}` | Delete a single snapshot |
| `DELETE` | `/api/workflows/versions/{id}` | Delete all snapshots for a workflow |

## See also

- [Canvas editor ▶ Run timeline](./canvas#run-timeline) — the UI that reads this layout.
- [MCP authoring ▶ Watching live runs](./mcp#watching-live-runs) — streaming side over MCP.
