---
outline: deep
---

# Workflows

A **workflow** is a multi-step automation stored on disk as a YAML graph of typed nodes (`classify`, `agent`, `connector`, `http`, `shell`, `branch`, `parallel`, `dataset_*`, …) and one or more triggers (cron, channel, webhook, manual, schedule_at). One inbound message — Slack mention, cron tick, webhook — kicks off a deterministic, replayable run that wick traces node-by-node.

Workflows reuse the same wick infrastructure you already configured for agents: provider pool, channel adapters, connectors, datasets. The workflow engine is the wiring that lets an LLM (or you, in the canvas editor) compose those primitives into something more structured than a free-form chat.

::: tip Where it sits
A workflow is **not** an agent — it's the layer above. An [`agent`](./nodes/agent) node inside a workflow spawns an agent turn the same way a channel message would. The difference: a workflow controls *when* that turn fires, *what context* it receives, and *what happens to its output*.
:::

## In this section

| | |
|---|---|
| [Nodes](./nodes) | Catalog of every node type with input schema, output fields, and a one-file-per-type reference. |
| [Triggers](./triggers) | Cron / channel / webhook / manual / `schedule_at` / error — when a workflow run starts. |
| [Canvas editor](./canvas) | Visual editor at `/tools/agents/workflows/<id>` — Drawflow, inspector, run timeline, Publish. |
| [MCP authoring](./mcp) | How an LLM scaffolds and edits workflows through `workflow_*` ops. |
| [Run state](./state) | On-disk layout, retention, replay. |

## When to reach for a workflow

| You want | Use |
|---|---|
| One-shot chat in Slack / Telegram / web | [Agents](/guide/agents) — direct channel → pool → reply |
| LLM that calls your APIs over MCP | [Connectors](/guide/connector-module) |
| Cron that runs a script | [Background Job](/guide/job-module) |
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
      output_cases: [bug_report, how_to, refund]
      input: '{{index .Event.Payload "text"}}'

    bug_report:
      type: agent
      prompt_file: nodes/bug.md

    how_to:
      type: connector
      module: docs-search
      op: search
      args:
        q: '{{index .Event.Payload "text"}}'

  edges:
    - {from: classify_intent, to: bug_report, case: bug_report}
    - {from: classify_intent, to: how_to,     case: how_to}
    - {from: classify_intent, to: refund,     case: refund}
```

Stored at `<BaseDir>/workflows/<id>/workflow.yaml`. The folder also holds per-run state under `runs/<run-id>/` — see [Run state](./state).

## Gate integration

Workflow `shell` and `agent` nodes participate in the [Command Gate](/guide/command-gate) policy:

- **PermissionMode** — same modes as channel-driven agent turns (`on` / `bypass`).
- **AskUserMode** — workflow agent nodes that call the `ask_user` MCP tool route the question through the same dispatcher channels do; the policy short-circuits cleanly when ask_user is disabled.

The gate is **per-provider**, not per-workflow — turning it on for a `claude/...` instance gates every place that instance is used, whether the trigger is a Slack thread or a cron-fired workflow.

## See also

- [Agents](/guide/agents) — the layer below; workflows orchestrate agent turns rather than replacing them.
- [Channels](/guide/agents/channels) — inbound transports + outbound actions a workflow node can use.
- [Connector Module](/guide/connector-module) — the same connector rows a `connector` node calls.
- [MCP for LLMs](/guide/mcp) — the transport every `workflow_*` op rides.
- [Built-in Workflow connector](/connectors/workflow) — full `workflow_*` op catalog for LLMs.
