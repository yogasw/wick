package event

// Parser turns one CLI stdout line into an AgentEvent. Implementations
// are stateful when the CLI's stream-json grammar requires it (e.g.
// Claude emits content_block_start then a sequence of content_block_delta
// for the same block — the parser tracks "what kind of block am I in"
// across calls).
//
// Parse returns:
//   - (AgentEvent{Type: Unknown}, nil)  → line is parseable but uninteresting
//   - (event, nil)                      → caller forwards the event
//   - (_, err)                          → line is malformed; caller logs and skips
//
// A blank line is always (Unknown, nil) — never an error — so naive
// scanners can hand every line to Parse without filtering.
type Parser interface {
	Parse(line string) (AgentEvent, error)
}
