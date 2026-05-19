---
outline: deep
---

# Run state

Every run lives on disk under the workflow's folder. The canvas's run timeline reads this directly — no separate trace store, no DB rows for per-node IO.

## Layout

```
<BaseDir>/workflows/<workflow-id>/
├── workflow.yaml          ← the published graph
├── workflow.draft.yaml    ← unpublished edits (if any)
├── nodes/                 ← prompt files referenced by agent / classify nodes
│   └── *.md
├── __tests__/             ← test fixtures (see MCP authoring)
│   ├── *.json
│   └── nodes/             ← per-node unit fixtures
└── runs/
    └── <run-id>/
        ├── meta.json          ← trigger payload, start/end, status, cost
        ├── events.jsonl       ← one line per node start/finish/error + edge_traversed
        ├── nodes/<node-id>.json   ← output of each node
        ├── prefill.json       ← snapshot of the inbound trigger payload
        └── mocks.json         ← (optional) prefill data for Copy-to-editor
```

`meta.json` is the run summary the timeline reads first. `events.jsonl` is the full event stream — every `node_started`, `node_completed`, `node_failed`, `edge_traversed` entry with timestamps and data payloads. Reach for it (via `workflow_get_run_events`) when `workflow_get_run` doesn't have enough detail — e.g. user gives you a failed run ID and asks why.

Retention is per-workflow (default: keep last 50 runs). Older runs are pruned at boot; the cleanup is best-effort and a manual delete of `runs/<id>/` is always safe.

## Replay

Open a run in the timeline → click **Rerun** → navigates to the manual runner pre-filled with the original payload. There is no one-click replay-and-execute; you review before re-running.

This is the same pattern as the [connector test panel](/guide/connector-module#test-panel-postman-style). Rationale: an LLM-driven workflow might have side-effected on a downstream API; blindly re-running can double-fire. Manual review + Run keeps the human in the loop.

From MCP, `workflow_replay_run` is the equivalent convenience wrapper — loads `RunState.Event` from `run_id` and calls `workflow_run_now`. Returns a new `run_id`.

## Copy to editor

The **Copy to editor** button (timeline + the MCP `workflow_copy_run_to_editor` op) does three things in one call:

1. Loads the run state.
2. Saves the current published workflow as draft (so you can edit without overwriting prod).
3. Writes `runs/<run_id>/mocks.json` so the canvas's Execute step can prefill from real data.

Use this when iterating on a workflow against real-world failure data: load the failed run → edit the graph → run again against the same payload → publish when it's right.

## See also

- [Canvas editor ▶ Run timeline](./canvas#run-timeline) — the UI that reads this layout.
- [MCP authoring ▶ Watching live runs](./mcp#watching-live-runs) — streaming side over MCP.
