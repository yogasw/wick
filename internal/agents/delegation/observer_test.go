package delegation

import (
	"sync"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

func TestObserverRoutesOnceOnDone(t *testing.T) {
	var got []string
	o := NewTurnObserver(func(sid, _ string, text string) { got = append(got, sid+"|"+text) })

	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.TextDelta, Text: "@worker "})
	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.TextDelta, Text: "check auth"})
	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.Done})
	// The exit path publishes a SECOND synthetic Done for a turn that has
	// already been flushed. Routing there would dispatch every mention in
	// it twice.
	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.Done})

	if len(got) != 1 || got[0] != "s1|@worker check auth" {
		t.Fatalf("got %v, want exactly one assembled turn", got)
	}
}

func TestObserverKeysBySessionAndAgent(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	o := NewTurnObserver(func(sid, agent, text string) {
		mu.Lock()
		defer mu.Unlock()
		got[sid+"/"+agent] = text
	})

	o.OnEvent("s1", "a", event.AgentEvent{Type: event.TextDelta, Text: "alpha"})
	o.OnEvent("s1", "b", event.AgentEvent{Type: event.TextDelta, Text: "beta"})
	o.OnEvent("s1", "a", event.AgentEvent{Type: event.Done})

	if got["s1/a"] != "alpha" {
		t.Fatalf("a = %q, want alpha", got["s1/a"])
	}
	if _, done := got["s1/b"]; done {
		t.Fatal("flushing one agent's turn also flushed another's")
	}

	o.OnEvent("s1", "b", event.AgentEvent{Type: event.Done})
	if got["s1/b"] != "beta" {
		t.Fatalf("b = %q, want beta", got["s1/b"])
	}
}

// A turn that streamed nothing has nothing to scan.
func TestObserverIgnoresEmptyTurns(t *testing.T) {
	calls := 0
	o := NewTurnObserver(func(string, string, string) { calls++ })

	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.Done})
	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.TextDelta, Text: "   \n "})
	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.Done})

	if calls != 0 {
		t.Fatalf("routed %d times, want 0", calls)
	}
}

// Buffers must not survive their turn, or a later one inherits text the
// agent already sent and re-dispatches its mentions.
func TestObserverClearsItsBufferOnFlush(t *testing.T) {
	var got []string
	o := NewTurnObserver(func(_, _, text string) { got = append(got, text) })

	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.TextDelta, Text: "first"})
	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.Done})
	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.TextDelta, Text: "second"})
	o.OnEvent("s1", "claude", event.AgentEvent{Type: event.Done})

	if len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Fatalf("got %v, want [first second]", got)
	}
}
