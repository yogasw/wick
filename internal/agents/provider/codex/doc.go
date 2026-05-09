// Package codex will hold the Codex-CLI specific Spawner implementation.
//
// Phase 6 work — placeholder folder so the agent/claude · agent/codex ·
// agent/gemini sibling layout is established now.
//
// Reference (agents-design.md §4.6):
//
//   - Streaming flag: `--json`
//   - Format: JSONL
//   - Resume: `codex resume <UUID>` (session ID lives in
//     ~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl)
//   - Hook: PermissionRequest (gate phase 3 — codex variant)
//
// When implemented, mirror the agent/claude layout: spawn.go for the
// Spawner + process struct, separate test using a fake spawner via
// the agent.Process interface.
package codex
