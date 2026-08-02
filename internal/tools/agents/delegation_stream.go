package agents

import (
	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/agents/event"
)

// DelegationStream adapts the SSE Broadcaster to the delegation
// package's EventStream interface, so a waiting delegation.Run observes
// exactly the same normalized event flow the UI does — one source of
// truth for "what did this agent do", rather than a second tap into the
// provider stream that could drift from it.
type DelegationStream struct {
	Bcast *Broadcaster
}

// NewDelegationStream wraps a live broadcaster.
func NewDelegationStream(b *Broadcaster) *DelegationStream {
	return &DelegationStream{Bcast: b}
}

// SubscribeSession bridges one session's event channel, translating the
// SSE wire shape back into the typed events the turn counter reads.
func (d *DelegationStream) SubscribeSession(sessionID string) (<-chan delegation.StreamEvent, func()) {
	src, unsub := d.Bcast.Subscribe(sessionID)
	out := make(chan delegation.StreamEvent, 64)

	go func() {
		defer close(out)
		for ev := range src {
			se := delegation.StreamEvent{Type: eventTypeFromString(ev.Type), Text: ev.Data}
			select {
			case out <- se:
			default:
				// Never block the broadcaster: a stalled delegation
				// consumer must not wedge the agent reader goroutine that
				// every other subscriber also depends on.
			}
		}
	}()
	return out, unsub
}

// eventTypeFromString reverses event.EventType.String(). Only the types
// the delegation turn counter acts on need to round-trip; everything
// else maps to Unknown and is ignored by the consumer.
func eventTypeFromString(s string) event.EventType {
	switch s {
	case "session_start":
		return event.SessionStart
	case "thinking":
		return event.Thinking
	case "text_delta":
		return event.TextDelta
	case "tool_use":
		return event.ToolUse
	case "tool_result":
		return event.ToolResult
	case "done":
		return event.Done
	case "error":
		return event.Error
	case "warning":
		return event.Warning
	case "trace":
		return event.Trace
	default:
		return event.Unknown
	}
}
