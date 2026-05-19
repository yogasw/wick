---
outline: deep
---

# Canvas editor

The canvas at `/tools/agents/workflows/<id>` is the visual editor. Built on Drawflow with a typed wick inspector layered on top.

## Layout

| Panel | Role |
|---|---|
| **Palette** (left) | Every registered node type. Drag onto the canvas to add. |
| **Canvas** (center) | The graph. Pan + zoom; marquee select; fit-to-view; auto top-down layout. Connections are typed — `branch` / `classify` / `switch` nodes get one outgoing port per `case:`. |
| **Inspector** (right) | Per-node form reflected from the executor's `Descriptor().Schema` — same source the MCP catalog reads. Every field has a label, widget, and help text without doc duplication. |
| **Run timeline** (bottom) | Pick a run, replay it step by step, see input / output per node, error trace per failure. |
| **Toolbar** (top) | Save (writes to `workflow.draft.yaml`), Publish, Test, Doctor, Run. |

## Editing flow

1. Drag a node from the palette → it lands with default field values.
2. Double-click to focus the inspector. Fill required fields (red asterisk).
3. Drag from a node's output port to another node's input port to add an edge. `case:` labels appear on outgoing ports of branching nodes.
4. **Save** writes the current state to `workflow.draft.yaml`.
5. **Publish** validates the canvas state, runs the same `workflow_diagnose` checks the MCP surface uses, and only writes `workflow.yaml` if every check passes. Failed checks land inline next to the offending node.

::: info Two ways to edit the same file
The canvas + MCP `workflow_*` ops mutate the same `workflow.yaml`. An LLM can scaffold the graph over MCP, you tighten it in the canvas, and version control sees a single file. There is no separate "AI mode" / "canvas mode" — both write through the same engine validator.
:::

## Keyboard shortcuts

| Shortcut | Action |
|---|---|
| `Ctrl/Cmd + K` | Node search modal — type to find a node type, Enter to drop at cursor. |
| `Ctrl/Cmd + S` | Save draft. |
| `Ctrl/Cmd + Enter` | Publish (after Save passes). |
| `Delete` / `Backspace` | Delete selected node(s) and any edges they touch. **Cannot delete the entry node** — the canvas guards this. |
| `F` | Fit to view. |
| `L` | Lock / unlock pan. |

## Run timeline

Pick a past run → the canvas highlights the `path_taken` and the timeline shows per-node input / output / duration / error. Click **Rerun** to navigate to the manual runner pre-filled with the original payload — see [Run state](./state#replay).

The **Copy to editor** button on a run loads the run state, saves the current published workflow as draft, and writes `runs/<run_id>/mocks.json` so the next Execute step can prefill from real data. Same op surfaces from MCP as `workflow_copy_run_to_editor`.

## Doctor + Validate

- **Doctor** button → runs `workflow_diagnose` across every workflow and reports the union (broken refs, missing triggers, unreachable nodes) in one report.
- **Validate** button (per workflow) → runs `workflow_validate` on the current draft and shows errors inline. Errors are decorated with did-you-mean hints (`templateMissingKey`, `channel_action_missing`, `picker scalar vs object`, …) when wick recognises the failure.

## See also

- [Nodes](./nodes) — the node catalog the palette draws from.
- [MCP authoring](./mcp) — same edits over MCP for LLM workflows.
- [Run state](./state) — what the timeline reads.
