---
outline: deep
---

# `session_init`

First-turn context injection for [`agent`](./agent) nodes. Mirrors the channel session-context pattern — Slack/Telegram channels inject a one-time system turn (project, chat, user, link) before the first user message; `session_init` does the same for workflow-spawned sessions.

| | |
|---|---|
| **Source** | [`internal/agents/workflow/nodes/session_init.go`](https://github.com/yogasw/wick/blob/master/internal/agents/workflow/nodes/session_init.go) |
| **When to use** | A workflow `agent` node should "feel" like it was invoked from a channel — same project metadata header, same user/link context. |
| **Route** | Through the agent pool, same as [`agent`](./agent). |

## Schema

The node carries the same context fields the channel session bootstrap uses (project name, chat/conversation id, user metadata, dashboard link). The canvas inspector reflects these from the executor's `Descriptor()`; for the exact field set call `workflow_node_detail` over MCP.

## Workspace override

The inspector exposes a **Workspace override (optional)** field. When set, the agent session is bound to that project instead of the workspace the workflow run itself belongs to.

In the canvas editor this field is a dropdown populated from existing projects (`GET /tools/agents/projects/options`). Leaving it at the default **"(use run workspace)"** (empty value) binds the session to the run's own workspace — which is the correct choice for most workflows.

Typing a project ID that does not exist previously caused a runtime error (`ensure session: project "…" not found`). The dropdown eliminates that class of mistake by constraining the selection to projects that actually exist.

## When you don't need it

If your `agent` node already gets enough context from `.Event.Payload` (e.g. a Slack `app_mention` workflow), you don't need `session_init`. The pattern is for workflows triggered by cron / webhook / manual where there's no inbound channel context to inherit.

## Pair with

- [`agent`](./agent) — `session_init` always precedes one.
- [Channels](/guide/agents/channels) — same context-injection pattern, the channel side.
