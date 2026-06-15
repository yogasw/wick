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
| `prompt` | string | ✅ | Inline prompt rendered as a Go template with `.Event`, `.Node`, `.Trigger` context. |
| `provider` | string | | Provider name (`claude`, `codex`, `gemini`, …). Optional — falls back to workflow default. |
| `skills` | array | | Skill names to expose to this turn. Per-provider — see [Providers](/guide/agents/providers). |
| `tools` | array | | Tool names to allowlist. Empty = provider default. |
| `max_turns` | int | | Cap on agentic turns passed as `--max-turns` to the provider CLI. `0` (default) = unlimited (provider default). |
| `thinking` | `on` \| `off` | | Extended thinking toggle. `on` (default) = enabled; `off` = disabled (sets `MAX_THINKING_TOKENS=0`). Claude only — ignored by Gemini and Codex. |
| `max_thinking_tokens` | int | | Visible only when `thinking: on`. Token budget for extended thinking. `0` (default) = unlimited (env not set, provider default applies). Set to `≥ 1024` to impose a hard cap. Claude only. |
| `session` | string | | `new` = fresh session per run, empty = inherit the run's session. The session directory is created automatically on first use — a `session_init` upstream node is not required. |

## Output

Whatever the agent emits. The executor merges `text_delta` chunks into a single `result` string and exposes typed events (`tool_use`, `tool_result`, `thinking`) on the run timeline for replay.

## Extended thinking

The `thinking` dropdown and `max_thinking_tokens` field control Claude's extended thinking on a per-node basis. The regular agent chat flow is unchanged — this config applies only inside a workflow agent node.

| `thinking` | `max_thinking_tokens` | Effect |
|---|---|---|
| `off` | (ignored) | Extended thinking disabled. `MAX_THINKING_TOKENS=0` is passed to the spawner. Lower latency; useful for fast routing steps. |
| `on` (default) | `0` or empty | Unlimited thinking. The env var is left unset; the provider uses its own default budget. |
| `on` | `≥ 1024` | Capped budget. `MAX_THINKING_TOKENS=<n>` is passed, hard-limiting the number of thinking tokens Claude may use in that turn. |

> **Important:** `max_thinking_tokens: 0` means *unlimited* (env left unset), which is distinct from `thinking: off` (env set to `0` = disabled). The minimum meaningful cap is `1024`; values between `1` and `1023` are forwarded as-is but may be rejected by the provider.
>
> Gemini and Codex ignore both fields — setting them has no effect on those providers.

The values are persisted to session meta before each pool send (mirroring `max_turns`), so a reused session always reflects the current node config rather than a prior run's settings.

## Example

```json
{
  "id": "bug_report",
  "type": "agent",
  "prompt": "You are a support engineer.\n\nNew bug from {{.Event.User.Name}}:\n> {{.Event.Payload.text}}\n\nUse the `gh` CLI to file the issue. Reply with the issue URL.",
  "provider": "claude",
  "skills": ["shell", "git"],
  "max_turns": 3,
  "thinking": "on",
  "max_thinking_tokens": 8000
}
```

## Pool integration

When the resolved provider can route via the pool, the executor:

1. Subscribes to the session's event stream **before** dispatching the pool send (no leading event lost).
2. Enqueues the turn through the agent pool — FIFO queue, slot allocation, session reuse, sidebar visibility.
3. Streams events back into the workflow run's `events.jsonl`.

For non-pool providers (codex / gemini), the executor falls back to the direct `provider.AgentCall` path.

## Pair with

- [`session_init`](./session_init) — inject a first-turn context (project / chat / user / link) before the agent runs.
- [`classify`](./classify) — cheap routing in front of an expensive agent call.
- [`channel`](./channel) — post the agent's reply back to the inbound channel.
