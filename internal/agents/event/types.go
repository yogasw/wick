// Package event holds the CLI-agnostic event abstraction. Every
// supported CLI (claude, codex, gemini) emits its own stream-json
// flavor; this package normalizes those into a single AgentEvent type
// so the rest of the agents pipeline (state machine, store, pool)
// doesn't have to care which backend produced the line.
//
// Files:
//   - types.go  — AgentEvent + EventType
//   - parser.go — Parser interface
//   - claude.go — ClaudeParser implementation (phase 2 scope)
//
// Codex and Gemini parsers land in phase 6.
package event

// EventType is the normalized event taxonomy used across all CLIs.
//
// `Thinking` is optional — only Claude exposes thinking deltas; other
// parsers may never emit it. Consumers must not rely on Thinking
// arriving before TextDelta.
type EventType int

const (
	// Unknown is the zero value, used for parser output that the
	// caller can safely skip (e.g. control frames, keepalives).
	Unknown EventType = iota
	// SessionStart fires once per spawn, carrying the CLI's session ID
	// so wick can persist it for `--resume`.
	SessionStart
	// Thinking is a chain-of-thought delta (Claude only). UI may show
	// it in the raw view; conversation.jsonl skips it.
	Thinking
	// TextDelta is one chunk of streamed assistant output. Consumers
	// concatenate the .Text fields until Done to get the full reply.
	TextDelta
	// ToolUse fires when the CLI is about to invoke a tool (Bash,
	// edit, ...). ToolName + ToolInput are populated; the wick command
	// gate keys off this in phase 3.
	ToolUse
	// ToolResult fires after a tool finishes. Body is in .Text.
	ToolResult
	// Done marks end-of-turn — subprocess is idle until next input.
	Done
	// Error indicates the CLI emitted an error event (not a parse
	// failure — those are returned via Parser.Parse error).
	Error
)

// String makes log lines readable. Not used for serialization.
func (t EventType) String() string {
	switch t {
	case SessionStart:
		return "session_start"
	case Thinking:
		return "thinking"
	case TextDelta:
		return "text_delta"
	case ToolUse:
		return "tool_use"
	case ToolResult:
		return "tool_result"
	case Done:
		return "done"
	case Error:
		return "error"
	default:
		return "unknown"
	}
}

// AgentEvent is the normalized event passed downstream of every
// parser. Fields are populated based on Type — only Type and Raw are
// always set.
//
// Raw holds the verbatim JSON line so raw.jsonl in the session folder
// can mirror the upstream stream byte-for-byte (debug view).
type AgentEvent struct {
	Type      EventType
	Text      string // TextDelta / Thinking / ToolResult body
	ToolName  string // ToolUse: tool identifier (e.g. "Bash")
	ToolInput string // ToolUse: JSON-encoded arguments before exec
	SessionID string // SessionStart: CLI session ID (or first event for Claude)
	ErrorMsg  string // Error: short reason
	Raw       string // verbatim source line
}
