---
outline: deep
---

# Workflows

A **workflow** is a multi-step automation stored on disk as a YAML graph of typed nodes (`classify`, `agent`, `connector`, `http`, `shell`, `branch`, `parallel`, `dataset_*`, …) and one or more triggers (cron, channel, webhook, manual, schedule_at). One channel message — Slack mention, cron tick, webhook — kicks off a deterministic, replayable run that wick traces node-by-node.

Workflows reuse the same wick infrastructure you already configured for agents: provider pool, channel adapters, connectors, datasets. The workflow engine is the wiring that lets an LLM (or you, in the canvas editor) compose those primitives into something more structured than a free-form chat.

::: tip Where it sits
A workflow is **not** an agent — it's the layer above. An `agent` node inside a workflow spawns an agent turn the same way a channel message would. The difference: a workflow controls *when* that turn fires, *what context* it receives, and *what happens to its output*.
:::

## When to reach for a workflow

| You want | Use |
|---|---|
| One-shot chat in Slack / Telegram / web | [Agents](./channels) — direct channel → pool → reply |
| LLM that calls your APIs over MCP | [Connectors](../connector-module) |
| Cron that runs a script | [Background Job](../job-module) |
| **Same trigger fires a multi-step pipeline** — classify the inbound message, branch on intent, fetch context from N APIs, hand a focused prompt to the agent, post the structured reply | **Workflow** |
| Replayable runs you can edit visually | **Workflow** |

## Anatomy

```yaml
id: support-triage
version: 1
name: Support Triage
enabled: true

triggers:
  - type: channel
    source: slack
    match:
      event: app_mention

graph:
  entry: classify_intent

  nodes:
    classify_intent:
      type: classify
      categories: [bug_report, how_to, refund]

    bug_report:
      type: agent
      preset: support-engineer
      message: |
        New bug from {{.Event.User.Name}}:
        {{.Event.Text}}

    how_to:
      type: connector
      connector_id: <docs-search-row-id>
      op: search
      input:
        q: "{{.Event.Text}}"

  edges:
    - {from: classify_intent, to: bug_report, case: bug_report}
    - {from: classify_intent, to: how_to,     case: how_to}
    - {from: classify_intent, to: refund,     case: refund}
```

Stored at `<BaseDir>/workflows/<id>/workflow.yaml`. The folder also holds per-run state under `runs/<run-id>/` (events.jsonl, node outputs, prefill snapshot) — see the [state layout below](#state-layout).

### Node types

| Type | What it does |
|---|---|
| `classify` | Ask an LLM to pick one of `categories` for the incoming text. Verdict drives outgoing edges by `case:`. |
| `agent` | Spawn an agent turn through the existing pool, with a templated prompt + optional preset/workspace. Reuses subprocesses via `--resume`. |
| `connector` | Call one operation on an existing connector row. Same code path as MCP `wick_execute`. |
| `channel` | Send a message (or run a channel action — Slack `send_message`, `open_modal`, `add_reaction`, …) without going through an agent. |
| `http` | Outbound HTTP call (templated URL / headers / body, retry policy, `parse_response: raw\|json\|bytes`). |
| `shell` | Run a local command. Uses the same gate policy as agents when enabled. |
| `db_query` | Run SQL against a configured database connector. |
| `transform` | Pure data shaping — pluck fields, build a new object, no I/O. |
| `go_script` | Inline Go snippet evaluated at run time. Escape hatch for tiny adapters. |
| `branch`, `switch` | Conditional edges by expression / verdict. |
| `parallel`, `merge` | Fan out N nodes, wait for all to finish, merge their outputs. |
| `dataset_get / query / insert / upsert / delete / count / exists` | Read / write an in-process dataset (a typed key/value or rowset bound to the workflow). |
| `session_init` | First-turn context injection for agent nodes — mirrors the channel session-context pattern. |
| `end` | Explicit terminator. Implicit when a node has no outgoing edges. |

Schema for every node type is self-documenting via MCP `workflow_node_detail` — see [MCP surface](#mcp-surface).

### Triggers

| Trigger | When it fires |
|---|---|
| `cron` | On a cron schedule, exactly like a [job](../job-module) but driving a workflow run. |
| `channel` | An inbound message from Slack / Telegram / Web matches `source` + `match.event`. The full event payload (user, text, thread_ts, …) lands in `.Event.Payload`. |
| `webhook` | Public HTTP endpoint at `/webhook/<workflow-id>`. Body becomes `.Event.Payload`. |
| `manual` | Triggered from the canvas editor's Run button or the workflow detail page. |
| `schedule_at` | One-shot timer — a previous node emits a "fire at T+N" intent, the scheduler queues a fresh run at that time. |
| `error` | Other workflow failed — receives the failed run's metadata so you can route alerts. |

One workflow can carry multiple triggers — the queue policy (per-workflow concurrency cap, drop / queue / parallel) decides what happens when two land at once.

## Canvas editor

The canvas at `/tools/agents/workflows/<id>` is a Drawflow-based visual editor:

- Drag node types from the palette, double-click to edit.
- Connections are typed — branch / classify / switch nodes get one outgoing port per `case:`.
- Inspector panel reflects the per-field schema reflected from the executor's `Describe()` method (same source the MCP catalog reads), so every field has a label, widget, and help text without doc duplication.
- **Run timeline** at the bottom — pick a run, replay it step by step, see input / output per node, error trace per failure.

A "Publish" button validates the canvas state, runs the same `workflow_diagnose` checks the MCP surface uses, and only writes `workflow.yaml` if every check passes. Failed checks land inline next to the offending node.

::: info Two ways to edit the same file
The canvas + MCP `workflow_*` ops mutate the same `workflow.yaml`. An LLM can scaffold the graph over MCP, you tighten it in the canvas, and version control sees a single file. There is no separate "AI mode" / "canvas mode" — both write through the same engine validator.
:::

## MCP surface

Every workflow primitive is reachable from an LLM via the wick MCP server:

| Op | Purpose |
|---|---|
| `workflow_list` | Enumerate every workflow (id, name, enabled, last run status). |
| `workflow_describe` | Full node + edge + trigger snapshot for one workflow, with grouped dependencies (channels, connectors, datasets, agents, http hosts). |
| `workflow_node_types` | Catalog of all node types with JSON-schema-ish field specs reflected from each executor's `Describe()`. The LLM picks a type without guessing. |
| `workflow_node_detail` | Per-type detail — schema, output fields, examples, common pitfalls, pair-with hints. |
| `workflow_diagnose` | Run static checks against a workflow (broken edges, missing trigger schema, undefined template references, unreachable nodes). Same check Publish uses. |
| `workflow_watch` | Stream live run events (node started / finished / failed) to the caller. Useful for "what happened on the last cron tick?" without grepping logs. |
| `workflow_scaffold` | Create a new workflow from a high-level intent. The LLM produces a YAML draft, the op writes the folder + opens the canvas. |
| `workflow_connect`, `workflow_patch`, `workflow_delete` | Mutate canvas state — add/move/remove nodes and edges, patch node config, all with the same validator the UI uses. |

The catalog is **self-documenting**: every executor declares its schema next to its `Execute` method (the [`workflow-node-module` skill](https://github.com/yogasw/wick/tree/master/.claude/skills/workflow-node-module) enforces this), so adding a new node type makes it appear in `workflow_node_types` and the canvas palette automatically — no separate registration.

## Channel actions inside a workflow

Channels expose more than inbound events. A `channel` node can call any registered channel action:

| Slack action | What |
|---|---|
| `send_message` | Post a message in a channel / DM / thread, with optional Block Kit blocks. |
| `add_reaction` | Add an emoji reaction to a message. |
| `open_dm` | Open (or reuse) a DM channel with a user, returning the channel ID. |
| `open_modal`, `push_modal`, `update_modal` | Open / push / update a Slack modal. |
| `send_ephemeral` | Post a message visible only to one user. |
| `publish_home` | Update a user's App Home tab. |
| `respond_url`, `update_message` | Use a `response_url` from an interaction payload; edit an existing message. |

Inbound events are first-class too — each event type (`event_message`, `event_app_mention`, `event_command`, `event_block_action`, `event_view_submission`, `event_shortcut`, …) becomes a typed trigger match so the workflow only fires on the shape it expects.

See [Channels ▶ Slack](./channels#slack) for transport details; the workflow surface is just the structured invocation of those same primitives.

## Run state

Every run lives at `<BaseDir>/workflows/<id>/runs/<run-id>/`:

```
runs/<run-id>/
├── meta.json          ← trigger payload, start/end, status
├── events.jsonl       ← one line per node start/finish/error
├── nodes/<node-id>.json  ← output of each node (used for prefill on replay)
└── prefill.json       ← snapshot of the inbound trigger payload
```

The canvas's run timeline reads this directly — no separate trace store. Retention is per-workflow (default: keep last 50 runs).

Replay: open the run in the timeline, click **Rerun** — it navigates to the manual runner pre-filled with the original payload. There is no one-click replay-and-execute; you review before re-running (same pattern as the [connector test panel](../connector-module#test-panel-postman-style)).

## Doctor + Watch

`workflow_doctor` runs `workflow_diagnose` across every workflow and reports the union — broken refs, missing triggers, unreachable nodes — in one report. Use it as a pre-deploy health check; the same op powers the **Doctor** button on the workflows index page.

`workflow_watch` is the streaming side: subscribe over MCP (or the canvas's run timeline) and receive node-level events as the run progresses. The transport is the same SSE channel the agent UI uses for its event stream.

## Gate integration

Workflow `shell` and `agent` nodes participate in the [Command Gate](../command-gate) policy:

- **PermissionMode** — same modes as channel-driven agent turns (`allow_all`, `prompt`, `bypass`).
- **AskUserMode** — workflow agent nodes that call the `ask_user` MCP tool route the question through the same dispatcher channels do; the policy short-circuits cleanly when ask_user is disabled.

The gate is **per-provider**, not per-workflow — turning it on for a `claude/...` instance gates every place that instance is used, whether the trigger is a Slack thread or a cron-fired workflow.

## See also

- [Agents](../agents) — the layer below; workflows orchestrate agent turns rather than replacing them.
- [Channels](./channels) — inbound transports + outbound actions a workflow node can use.
- [Connector Module](../connector-module) — the same connector rows a `connector` node calls.
- [MCP for LLMs](../mcp) — the transport every `workflow_*` op rides.
- [Command Gate](../command-gate) — gating shell + ask_user inside a workflow run.
