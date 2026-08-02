package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

// The delegation turn counter keys on Done, so if EventType.String()
// and eventTypeFromString ever drift, a sub-agent's turns stop being
// counted and both the turn cap and the budget silently stop working.
// This pins the round trip for every type that has a name.
func TestEventTypeRoundTrip(t *testing.T) {
	types := []event.EventType{
		event.SessionStart,
		event.Thinking,
		event.TextDelta,
		event.ToolUse,
		event.ToolResult,
		event.Done,
		event.Error,
		event.Warning,
		event.Trace,
	}
	for _, want := range types {
		name := want.String()
		if got := eventTypeFromString(name); got != want {
			t.Fatalf("%q: round trip gave %v, want %v", name, got, want)
		}
	}
}

func TestEventTypeUnknownStringMapsToUnknown(t *testing.T) {
	if got := eventTypeFromString("no_such_event"); got != event.Unknown {
		t.Fatalf("got %v, want Unknown", got)
	}
}
