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
5. **Publish** validates the canvas state, runs the same `workflow_diagnose` checks the MCP surface uses, and only writes `workflow.yaml` if every check passes. Failed checks land inline next to the offending node. One class of check is a **publish-blocker**: any field whose `arg_modes` entry is `fixed` but whose value contains a Go template (`{{...}}`) is reported as an Error — the template would never evaluate at runtime. Draft Save is not affected; only the Publish step is gated.

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

**Fixed-mode template guard** — if a field's arg mode is `fixed` (literal string) and its value contains `{{...}}`, validation raises an Error that blocks publish. Go templates never render in fixed mode; the string would reach the connector/HTTP call verbatim. To fix: either switch the field's toggle to Expression, or remove the template syntax. The editor auto-switches a field to Expression when you type `{{` into it.

Connector and channel op authors can lock the toggle for a field in code via the `wick:"mode=fixed"` / `wick:"mode=expression"` [config tag](../reference/config-tags#modifiers-any-widget), preventing users from accidentally setting the wrong mode on fields that must always be literal or always be templated.

## Workflow settings (env & secrets)

Open the **Settings** modal from the **⋮ more** menu in the toolbar. It contains a key-value table where you can define environment variables that are available to every node in the workflow at run time via `{{.Env.KEY}}`.

### Adding variables

1. Click **+ Add row** — a new row appears with empty key and value fields.
2. Type the key (e.g. `API_TOKEN`, `BASE_URL`).
3. Type the value.
4. Toggle **Secret** if the value is sensitive (API keys, passwords, tokens).

### Secrets

When **Secret** is on for a row:

- The value is encrypted with the master key (`WICK_ENC_KEY`) before being stored in the `workflows.env_values` DB column. The stored form is a `wick_cenc_` token.
- In template preview (`workflow_template_test`) and in execute-step output, secret values are masked as `••••••••`. The same masking applies to SSE events and the stored run state — plaintext only lives in engine memory during an active run.
- All secret and non-secret vars share the same `{{.Env.KEY}}` namespace. There is no `.Secret.KEY` — the engine handles decryption transparently.

::: warning Masking is best-effort
Masking catches values that appear verbatim in output strings. If a node transforms or fragments the secret value the masked form may not cover every occurrence. Avoid logging or returning raw secret values from nodes.
:::

### Using env vars in templates

```
{{.Env.API_TOKEN}}
{{.Env.BASE_URL}}/path
```

The render context injects all env vars (decrypted) before template evaluation. Missing keys produce a template error surfaced in the Validation panel and `workflow_template_test` output.

### Saving

Settings are saved when you click **Save** in the modal. Changes take effect on the next workflow run — no publish required for env/secret edits, since they are stored independently of the workflow graph.

## See also

- [Nodes](./nodes) — the node catalog the palette draws from.
- [MCP authoring](./mcp) — same edits over MCP for LLM workflows.
- [Run state](./state) — what the timeline reads.
