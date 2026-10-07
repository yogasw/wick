---
name: wick-workflows
description: Use when the user asks to build, edit, debug, or run a wick workflow — a multi-step automation stored as a graph of typed nodes with triggers (cron, channel, webhook, manual). Covers when a workflow is the right tool versus an agent or a job, the node catalog, trigger types, the render context, and how to author one over MCP.
---

# Using wick workflows

A **workflow** is a multi-step automation stored as a JSON graph of typed nodes plus one or more triggers. One inbound event — a Slack mention, a cron tick, a webhook — starts a deterministic, replayable run that wick traces node by node.

A workflow is **not** an agent; it is the layer above. An `agent` node inside a workflow spawns an agent turn the same way a channel message would. What the workflow adds is control over *when* that turn fires, *what context* it gets, and *what happens to its output*.

## When a workflow is the right answer

| The user wants | Use |
|---|---|
| One-shot chat in Slack / Telegram / web | An agent — channel → pool → reply |
| An LLM that calls APIs | Connectors over MCP |
| A cron that runs a script | A background job |
| **One trigger firing a multi-step pipeline** — classify the message, branch on intent, fetch from several APIs, hand a focused prompt to an agent, post a structured reply | **A workflow** |
| Runs you can replay and edit visually | **A workflow** |

If the task is a single prompt and a single reply, a workflow is overhead. Say so rather than building one.

## Anatomy

```json
{
  "id": "support-triage",
  "version": 1,
  "name": "Support Triage",
  "enabled": true,
  "triggers": [
    {
      "id": "trigger-slack-message",
      "type": "channel",
      "channel": "slack",
      "event": "app_mention",
      "entry_node": "classify_intent"
    }
  ],
  "graph": {
    "entry": "classify_intent",
    "nodes": [
      { "id": "classify_intent", "type": "classify",
        "output_cases": ["bug_report", "how_to", "refund"],
        "input": "{{index .Event.Payload \"text\"}}" },
      { "id": "bug_report", "type": "agent",
        "prompt": "Triage this bug report: {{.Node.classify_intent.input}}" }
    ]
  }
}
```

Node types include `classify`, `agent`, `connector`, `http`, `shell`, `branch`, `parallel`, the `datatable_*` family, and `sticky_note` — a canvas-only annotation (fields `content`, `color`, `width`, `height`, `texts`) that never executes and takes no edges. Do not guess a node's schema — fetch it (see *Authoring over MCP* below).

## Triggers

| Trigger | Fires when |
|---|---|
| `cron` | A 6-field cron expression matches. Disabled workflows skip ticks but still run manually. |
| `channel` | An inbound message matches `channel` + `event` + `match`. |
| `webhook` | A request hits `/webhook/<workflow-id>`. |
| `manual` | Someone presses Run in the canvas or on the detail page. |
| `schedule_at` | A one-shot timer queued by an earlier node. |
| `error` | Another workflow failed — receives the failed run's metadata, so you can route alerts. |

One workflow can carry several triggers. The queue policy — per-workflow concurrency cap, drop / queue / parallel — decides what happens when two land at once.

For `channel` triggers, `match` is **event-shape specific**. Slack events (`message`, `thread_started`, `app_mention`, `command`, `block_action`, `view_submission`, `shortcut`, …) each have their own filter schema. Discover the exact shape via the `workflow_integration` MCP op, which returns the per-channel event catalog with `match_schema` and `payload_schema`. Note `thread_started` fires only for a top-level post that starts a thread, never for replies.

## Render context

Every trigger contributes to `.Event` — payload, source identity, timestamp, user. Earlier node results are available under `.Node.<id>`. Templates use Go template syntax:

```
{{index .Event.Payload "text"}}
{{.Node.classify_intent.input}}
```

## Authoring over MCP

Workflows are scaffolded and edited through `workflow_*` operations. The discipline is the same as connectors: **fetch the schema, do not guess it.** The node catalog, per-node input schema, and output fields are all introspectable — a node's `Descriptor()` is the source of truth, so what MCP returns is always current.

Editing a live workflow creates a new version rather than mutating the running one. Publish makes a version active.

### Rules for every edit (required)

These keep a workflow readable by the next person — human or AI — who opens it. A workflow that breaks them is not finished.

1. **Every node and every trigger gets a `description`.** Markdown: one line on what it is for, then a `Why:` line on why it exists. Pass it on the node (`add_node` / `update_node`) and on each trigger (`set_triggers`). `workflow_validate` warns once per id that lacks one. When you change what a node does, update its `Why:` line in the same edit.

   ```markdown
   Post the summary back to the thread it came from.

   Why: the reporter needs the answer in the same thread, not in a DM.
   ```

   Write it for the person reading the canvas, not for the engine: plain language about what happens in the real world. No op names, field names, template syntax or ids — those are already visible on the card. Write it in the language the workspace's users read.

   | Bad | Good |
   |---|---|
   | `ticket_search by permalink C030…/p<ts>, limit 1` | Look up this inquiry's ticket on the board. |
   | `jq capture notion url → page_id` | Take the Notion ticket link from the bot's message in the thread. If there is none, stop here. |

2. **Every `go_script` / code body opens with a header comment** — `Purpose` / `Why` / `Input` / `Output` — and each helper function gets a one-line comment.

   ```go
   // Purpose: pull the ticket number out of the message text.
   // Why: the next node needs a ticket id, not raw text.
   // Input: .Node.trigger.payload.text
   // Output: {"ticket_id": "T-123"} or {"ticket_id": ""}
   ```

3. **Group each path with a `sticky_note` node.** A node of `type: sticky_note` with `content` (markdown title of the path + a short summary), `color` (yellow/green/blue/purple/red/gray) and a `width`/`height` big enough to cover the block. It renders behind the block's nodes, never executes and takes no edges. The note is a board: `content` is its title (top left). To pin annotations on it, set `texts`: `[{id, content, x, y, width, color, size}]` — each is a small sticky card; `x`/`y` (top-left of the card) and `width` are relative 0..1 to the note, so cards follow it on resize; `color` uses the same presets (default yellow), `size` is the font size `sm`/`md`/`lg` (default md). Its `content` or any `texts[].content` counts as its description. It is edited, moved and deleted like any other node.

4. **Tidy the canvas — on a grid a human can follow.** Start with `workflow_auto_layout`: it gives each trigger its own lane, puts every child under the output port its edge leaves from, and spaces cards by their real width (it leaves sticky notes where they are, so re-wrap them afterwards). Adjust by hand only where it falls short. When placing by hand, these are the rules a reviewer will hold you to:

   - **Grid, not eyeballing.** A node card renders 220 wide and ~140 tall (an `end` node ~80, a trigger ~115). Use a row step of **180** and a column step of **300–360**. Every node in a row shares the exact same `y`; every node in a column shares the exact same `x`. A step of 260+ reads as gaps; 120 overlaps.
   - **Wide cards take more room.** A card with 3–6 output ports (cases, plus `error` when `on_failure: fallback`) or 3–6 incoming sources widens to 90 per port: 4 ports → 360, 6 → 540. Measure the gap from its right edge, not from `x + 220`, or the next card lands on top of it. Several sources also add a strip under the header naming each one, so leave a little more row step under a merge.
   - **A child sits under the port it comes from.** A case node paints its outputs left → right in declared order, `default` last and `error` rightmost. Put each case's next node under that port, in the same order, so the edges drop straight down instead of crossing. The fallback node of `on_failure: fallback` goes under the `error` port, one row down.
   - **Longest path straight down, short branches to the right.** The path with the most steps stays in one column. A branch steps one column right, on the row *below* its parent, so the edge forms an `L`. Edges leave a card at the bottom and enter at the top, so a child placed on the *same* row as its parent (left or right) draws a loop — never do that.
   - **One `end` per path tip.** Do not route several paths into one shared `end`: that draws long edges across the canvas and the validator warns about parallel incoming edges. Put a small `end` directly under each final step.
   - **A trigger lives inside its path's sticky note.** The note's top sits ~80 above the trigger, so the title gets its own band and the trigger is the first card under it; the first node follows one row step later. All path notes share the same top `y`, and so do all triggers. A trigger left outside its note reads as "not part of this path".
   - **Sticky notes do not touch.** Wrap the block with ~30–40 padding, leave **≥60** between neighbouring notes, and put a note's `texts` cards in an empty column (widen the note to make one). A card over a node, or one note running into the next, reads as overlap.

5. **Edit in one batch — `workflow_apply`.** Every single-step op (`workflow_add_node`, `workflow_update_node`, `workflow_connect`, `workflow_move_nodes`, …) loads the draft, saves it and returns the WHOLE workflow — ~12k tokens for a 30-node graph — and they must run one after another, because concurrent single-step edits overwrite each other's draft. Ten small edits cost ten copies of the workflow. `workflow_apply` takes the whole edit as one ordered list, saves it once, and answers with counts and warnings only:

   ```json
   [
     {"op": "add_node", "node": {"id": "end_new", "label": "end_new", "type": "end",
                                 "description": "Done: the session is on the new ticket.\n\nWhy: marks the end of this path."}},
     {"op": "connect", "from": "attach_new_ticket", "to": "end_new"},
     {"op": "disconnect", "from": "run_agent", "to": "end_1"},
     {"op": "update_node", "node_id": "note_ticket", "patch": {"height": 1300}},
     {"op": "move", "moves": [{"node_id": "end_new", "x": 560, "y": 3330},
                              {"node_id": "note_ticket", "x": 520, "y": 2150}]},
     {"op": "set_triggers", "triggers": [ ... ]}
   ]
   ```

   - Steps run in order and later steps see earlier ones, so add a node and connect, patch and place it in the same call.
   - It is all-or-nothing: if one step fails nothing is saved, and the error names the step (`ops[3] connect: …`). Fix that step and resend the batch.
   - The draft is validated once, on the end state, so a node that is only reachable after a later `connect` is fine.
   - Give every `add_node` an explicit `id` so later steps (and later calls) can reference it. An id-less node gets a minted UUID, reported back under `minted` as `{op_index, label, id}`, where `op_index` is the step's position in `ops`.
   - Deleting is not batched: `delete_node` is refused, because deletion is a destructive op an admin can switch off. Use `workflow_delete_node` for it, separately.

   The order of work that keeps an edit cheap:

   1. Read the current state once (`workflow_canvas_view` for positions, `workflow_get` only if you need node bodies).
   2. Plan the final result on paper: every node, edge and description, and a table of every `x`/`y` and every note's size computed from the grid above.
   3. Send it as **one** `workflow_apply`.
   4. `workflow_validate`, then `workflow_publish` (ask the user before publishing).

   Do not move things in pieces "to see how it looks", and do not re-read the workflow between steps to check what you just sent. If the user corrects the layout, recompute the whole layout and send one more batch — not a string of single moves.

## Debugging a run

Runs are traced node by node and stored on disk, so a failed run can be inspected and replayed rather than re-triggered blindly. Read the run state first: which node failed, what its input actually was, and what the previous node emitted. A workflow that fails on the third node usually fails because the first node's output was not the shape the third node expected.

## The canvas

The editor lives at `/tools/agents/workflows/edit/<id>` — canvas, inspector, run timeline, version history with side-by-side compare, and Publish. Point the user there for visual editing; it is usually faster than describing a graph edit in prose.
