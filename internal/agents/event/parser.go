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

// MultiParser is implemented by parsers whose CLI can pack more than one
// wick event into a single line — opencode reports a tool only once it has
// finished, so one `tool_use` line is both the call and its result.
// Consumers go through ParseLine, which handles both kinds.
type MultiParser interface {
	Parser
	ParseAll(line string) ([]AgentEvent, error)
}

// ParseLine runs p over one line and returns every event it produced, in
// order. A plain Parser always yields exactly one event (possibly Unknown).
func ParseLine(p Parser, line string) ([]AgentEvent, error) {
	if mp, ok := p.(MultiParser); ok {
		return mp.ParseAll(line)
	}
	ev, err := p.Parse(line)
	if err != nil {
		return nil, err
	}
	return []AgentEvent{ev}, nil
}
