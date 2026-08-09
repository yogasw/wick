package agents

import (
	"github.com/rs/zerolog/log"

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
	out := make(chan delegation.StreamEvent, delegationStreamBuffer)

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
				//
				// But some events cannot be dropped. Done is how the turn
				// counter learns a turn ended and how a run learns it is
				// over — lose one and the delegation waits forever on an
				// event that will never come again. The sub-agent finishes,
				// writes its answer, goes idle, and the row still reads
				// "running 0/12 turns" with nobody ever woken. Observed in
				// the wild, and indistinguishable from a hung agent.
				//
				// A dropped TextDelta costs a fragment of prose that later
				// events supersede; a dropped Done costs the whole run. So
				// terminal events block, and only they.
				if isTerminalStreamEvent(se.Type) {
					out <- se
					continue
				}
				log.Warn().
					Str("session", sessionID).
					Str("event_type", ev.Type).
					Msg("delegation stream: consumer behind, dropping a non-terminal event")
			}
		}
	}()
	return out, unsub
}

// delegationStreamBuffer sizes the per-delegation event channel.
//
// The consumer (Service.await) does real work between reads — database
// writes, auto-reply, inbox delivery — so events arrive faster than they
// are taken during those windows. The old 64 was small enough that a
// chatty turn could overrun it while await was mid-write.
const delegationStreamBuffer = 512

// isTerminalStreamEvent reports whether losing this event would strand
// the run rather than merely thin its output.
func isTerminalStreamEvent(t event.EventType) bool {
	return t == event.Done || t == event.Error
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
