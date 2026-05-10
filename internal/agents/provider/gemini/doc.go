// Package gemini will hold the Gemini-CLI specific Spawner implementation.
//
// Phase 6 work — placeholder folder so the agent/claude · agent/codex ·
// agent/gemini sibling layout is established now.
//
// Reference (agents-design.md §4.6):
//
//   - Streaming flag: `--output-format stream-json`
//   - Format: newline-delimited JSON
//   - Resume: `gemini --resume <UUID>` (session ID via env
//     GEMINI_SESSION_ID or ~/.gemini/tmp/<hash>/chats/)
//   - Hook: BeforeTool (gate phase 3 — gemini variant)
//
// When implemented, mirror the agent/claude layout.
package gemini
