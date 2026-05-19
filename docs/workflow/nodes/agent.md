---
outline: deep
---

# `agent`

Spawn an agent turn through the existing pool. Templated prompt, optional skills + tools whitelist. Reuses subprocesses via `--resume` when the session matches an existing one.

| | |
|---|---|
| **Source** | [`internal/agents/workflow/nodes/agent.go`](https://github.com/yogasw/wick/blob/master/internal/agents/workflow/nodes/agent.go) |
| **When to use** | A step needs an actual AI turn — reasoning, tool use, free-form generation. Don't use for routing (use [`classify`](./classify)) or pure shaping (use [`transform`](./transform)). |
| **Gate** | Participates in [Command Gate](/guide/command-gate) policy — `PermissionMode` + `AskUserMode` apply. |

## Schema

| Field | Type | Required | Notes |
|---|---|---|---|
| `prompt_file` | path | ✅ | Path to prompt markdown file relative to workflow root (e.g. `nodes/summarize.md`). Rendered as Go template. |
| `provider` | string | | Provider name (`claude`, `codex`, `gemini`, …). Optional — falls back to workflow default. |
| `skills` | YAML list | | Skill names to expose to this turn. Per-provider — see [Providers](/guide/agents/providers). |
| `tools` | YAML list | | Tool names to allowlist. Empty = provider default. |
| `max_turns` | int | | Cap on agent turns. Default unlimited. |
| `session` | string | | `new` = fresh session per run, empty = inherit the run's session. |

## Output

Whatever the agent emits. The executor merges `text_delta` chunks into a single `result` string and exposes typed events (`tool_use`, `tool_result`, `thinking`) on the run timeline for replay.

## Example

```yaml
- id: bug_report
  type: agent
  prompt_file: nodes/bug.md
  provider: claude
  skills: [shell, git]
  max_turns: 3
```

`nodes/bug.md` is a regular Go-template Markdown file:

```markdown
You are a support engineer.

New bug from {{.Event.User.Name}}:
> {{.Event.Payload.text}}

Use the `gh` CLI to file the issue. Reply with the issue URL.
```

## Pool integration

When the resolved provider can route via the pool, the executor:

1. Subscribes to the session's event stream **before** dispatching the pool send (no leading event lost).
2. Enqueues the turn through the agent pool — FIFO queue, slot allocation, session reuse, sidebar visibility.
3. Streams events back into the workflow run's `events.jsonl`.

For non-pool providers (codex / gemini), the executor falls back to the direct `provider.AgentCall` path.

## Pair with

- [`session_init`](./session_init) — inject a first-turn context (workspace / chat / user / link) before the agent runs.
- [`classify`](./classify) — cheap routing in front of an expensive agent call.
- [`channel`](./channel) — post the agent's reply back to the inbound channel.
