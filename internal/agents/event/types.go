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
	// failure — those are returned via Parser.Parse error). Fatal:
	// consumers treat it as end-of-turn.
	Error
	// Warning is a NON-fatal error the CLI reported mid-stream (e.g. a
	// malformed skill/agent-role definition it chose to ignore). It is
	// recorded to history like an error but does NOT end the turn — the
	// subprocess keeps running. ErrorMsg carries the detail.
	Warning
	// Compaction fires when the CLI compacted the conversation to free
	// context — automatically at the window limit, or because someone ran
	// /compact. Not an error and not end-of-turn: the session id stays
	// the same and the run continues with a summary standing in for the
	// dropped messages. Carrying it as its own type is what lets the UI
	// mark the boundary instead of silently losing the history.
	Compaction
	// Trace is an event the parser doesn't map to a first-class type but
	// that is worth keeping visible — recorded into the turn's trace
	// (expandable in the UI) rather than the main thread. Raw carries the
	// verbatim line. Non-fatal; never ends the turn. Pure control frames
	// (started/ping/snapshots) stay Unknown and are skipped.
	Trace
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
	case Warning:
		return "warning"
	case Compaction:
		return "compaction"
	case Trace:
		return "trace"
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
	ToolUseID string // ToolUse + ToolResult: correlation ID to pair call with result
	IsError   bool   // ToolResult: true when the tool returned an error
	SessionID string // SessionStart: CLI session ID (or first event for Claude)
	ErrorMsg  string // Error: short reason
	Raw       string // verbatim source line

	// SubAgent names the sub-agent an event was RELAYED from, and is set
	// only on that relay — never by a parser. A leader's own events leave
	// it empty; a child's status event forwarded onto the leader's thread
	// carries the child's label ("researcher") so a channel can say whose
	// work it is showing.
	//
	// Relay is status-only by design: a channel that renders progress needs
	// to know a sub-agent is moving, but the child's REPLY reaches the
	// conversation through the delegation result, so relaying its text too
	// would post the same answer twice.
	SubAgent string

	// Usage is the turn's token accounting. Set on Done, and only when
	// the CLI reported it — nil otherwise, which is not an error (a
	// provider may simply not say).
	Usage *TokenUsage

	// Compaction is set only on Compaction events.
	Compaction *CompactionInfo

	// ContextUsed is how full the window was for the request THIS frame
	// came out of — a mid-turn reading, set on whatever event the frame
	// produced rather than on a type of its own.
	//
	// It exists because everything else here is end-of-turn. Usage lands
	// on Done, so a turn that runs for four minutes leaves every meter
	// frozen on the previous turn's numbers for as long as it is the one
	// thing anybody is watching. Claude reports the level on every
	// `assistant` frame, so the answer is already in hand well before the
	// turn ends; carrying it costs one int.
	//
	// It CANNOT disagree with the final reading: the end-of-turn level is
	// the last frame's level (see ClaudeParser.lastLevel, which overrides
	// what the result frame carries), so the last value seen here is the
	// value Done reports. Providers that report nothing mid-turn (codex —
	// its level comes from a rollout file read once per turn) simply
	// leave this zero, and zero means "this frame said nothing about the
	// window", never "the window is empty".
	ContextUsed int
}

// CompactionInfo is one compaction boundary as the CLI reported it.
// PreTokens is the context size that triggered it, PostTokens what
// survived — the pair is the whole story, and neither means much alone.
type CompactionInfo struct {
	// Trigger is "auto" (window limit) or "manual" (/compact).
	Trigger       string `json:"trigger,omitempty"`
	PreTokens     int    `json:"pre_tokens,omitempty"`
	PostTokens    int    `json:"post_tokens,omitempty"`
	DroppedTokens int    `json:"dropped_tokens,omitempty"`
	DurationMS    int    `json:"duration_ms,omitempty"`
}

// TokenUsage is one turn's token accounting, normalized across CLIs so
// the same arithmetic works whether the turn ran on claude, codex, or
// wick's own engine.
//
// Two different questions live here, and conflating them is the easy
// mistake. The four counters (Input/CacheRead/CacheWrite/Output) are
// FLOW — what this turn spent, which is what a cost report sums over
// time. ContextUsed is a LEVEL — how full the window was when the turn
// ended, which only makes sense as the latest reading and must never be
// added up. Window puts that level on a scale.
//
// Every counter is what the vendor reported, never our own estimate.
// Fields a given CLI does not report stay zero rather than being guessed:
// codex, for one, says nothing about the window size.
type TokenUsage struct {
	// Input is fresh input tokens — the part that missed the cache.
	Input int `json:"input,omitempty"`
	// CacheRead is input served from the prompt cache (cheap).
	CacheRead int `json:"cache_read,omitempty"`
	// CacheWrite is input written INTO the cache this turn.
	CacheWrite int `json:"cache_write,omitempty"`
	// Output is tokens the model generated, reasoning included.
	Output int `json:"output,omitempty"`

	// ContextUsed is everything the model saw as input for the last
	// message of the turn: fresh + both cache halves. A level, not a
	// flow — summing it across turns is meaningless.
	ContextUsed int `json:"context_used,omitempty"`
	// Window is the model's context limit, 0 when unreported.
	Window int `json:"window,omitempty"`
	// Model is the id the vendor billed, for per-model breakdowns.
	Model string `json:"model,omitempty"`
	// CostUSD is the vendor's own figure for the turn, 0 when it gives
	// none. Never computed here — a price table would go stale silently.
	CostUSD float64 `json:"cost_usd,omitempty"`
}
