// Package wick implements the built-in in-process provider — wick's
// own agent runtime, no external CLI required.
//
// Unlike claude/codex/gemini (subprocess spawners), wick runs the
// agent loop inside the wick process and bridges it onto the existing
// machinery by implementing provider.Process over an io.Pipe that
// emits claude-shaped stream-json lines. The ClaudeParser, store,
// state machine, SSE streaming, and spawn-log therefore work
// unchanged; the only bespoke code here is the engine loop, the
// event → stream-json translation, and the per-vendor model adapters.
//
// Key properties (see internal/planning/in-progress/wick-provider/plan.md):
//
//   - Single instance: exactly one "wick/wick" — model multiplicity
//     lives in Instance.WickModels (enforced by provider.Save/Rename).
//   - Custom models: google | openai | anthropic | openrouter | other,
//     each with its own encrypted API key and optional overrides.
//   - Tools: shell (gate-checked), wick connectors (direct service
//     call, no MCP protocol), wick session tools (reused MCP handler
//     implementations), todo, skill.
//   - History: rebuilt from the session's conversation.jsonl every
//     turn — cross-provider continuity by construction.
//   - No subprocess: Process.Pid() reports 0, Binary() a synthetic
//     label; the command gate's PreToolUse hook does not apply (the
//     gate is enforced in-process via a before-tool callback instead).
package wick
