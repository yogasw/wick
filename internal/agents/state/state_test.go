package state

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
)

func TestInitialState(t *testing.T) {
	m := New(nil)
	if m.Current() != Idle {
		t.Fatalf("initial: %v", m.Current())
	}
}

func TestTransitionsHappyPath(t *testing.T) {
	m := New(nil)
	cases := []struct {
		ev   event.EventType
		want State
	}{
		{event.Thinking, Thinking},
		{event.ToolUse, RunningTool},
		{event.TextDelta, Responding},
		{event.Done, Idle},
	}
	for _, tc := range cases {
		got := m.Apply(event.AgentEvent{Type: tc.ev})
		if got != tc.want {
			t.Fatalf("Apply(%v): got %v want %v", tc.ev, got, tc.want)
		}
	}
}

func TestErrorReturnsToIdle(t *testing.T) {
	m := New(nil)
	m.Apply(event.AgentEvent{Type: event.TextDelta})
	m.Apply(event.AgentEvent{Type: event.Error})
	if m.Current() != Idle {
		t.Fatalf("after error: %v", m.Current())
	}
}

func TestUnknownDoesNotChangeStateButBumpsLastActive(t *testing.T) {
	t0 := time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)
	clock := t0
	m := New(func() time.Time { return clock })
	m.Apply(event.AgentEvent{Type: event.TextDelta})
	if m.Current() != Responding {
		t.Fatalf("setup: %v", m.Current())
	}
	clock = t0.Add(time.Second)
	m.Apply(event.AgentEvent{Type: event.Unknown})
	if m.Current() != Responding {
		t.Fatalf("unknown changed state: %v", m.Current())
	}
	if !m.LastActive().Equal(clock) {
		t.Fatalf("lastActive not bumped: %v", m.LastActive())
	}
}

func TestMarkIdle(t *testing.T) {
	m := New(nil)
	m.Apply(event.AgentEvent{Type: event.TextDelta})
	m.MarkIdle()
	if m.Current() != Idle {
		t.Fatalf("after MarkIdle: %v", m.Current())
	}
}
