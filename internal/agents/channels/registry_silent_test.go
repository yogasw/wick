package channels

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

// recordingChannel captures every agent event fanned out to it, so a test can
// assert which events reached channels and which were suppressed.
type recordingChannel struct {
	got []event.AgentEvent
}

func (c *recordingChannel) Name() string                { return "rec" }
func (c *recordingChannel) Start(context.Context) error { return nil }
func (c *recordingChannel) Stop()                        {}
func (c *recordingChannel) IsConfigured() bool           { return true }
func (c *recordingChannel) OnAgentEvent(_ string, ev event.AgentEvent) {
	c.got = append(c.got, ev)
}

func (c *recordingChannel) texts() []string {
	var out []string
	for _, e := range c.got {
		if e.Type == event.TextDelta {
			out = append(out, e.Text)
		}
	}
	return out
}

func setup() (*Registry, *recordingChannel) {
	rec := &recordingChannel{}
	reg := NewRegistry()
	reg.Add(rec, nil)
	return reg, rec
}

func TestNormalReplyForwardsToChannel(t *testing.T) {
	reg, rec := setup()
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: "hello there"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
	if len(rec.got) != 2 {
		t.Fatalf("normal reply should forward all events, got %d: %+v", len(rec.got), rec.got)
	}
	if reg.WasLastTurnSilent("s1") {
		t.Fatal("normal turn should not be flagged silent")
	}
}

func TestSilentReplySuppressedFromChannel(t *testing.T) {
	reg, rec := setup()
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: "[silent] run 3/5 ok"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
	if len(rec.got) != 0 {
		t.Fatalf("silent reply must not reach channel, got %+v", rec.got)
	}
	if !reg.WasLastTurnSilent("s1") {
		t.Fatal("silent turn should be flagged for push skip")
	}
}

// The marker can arrive split across stream deltas — the registry must buffer
// until it has enough bytes to decide, then still suppress.
func TestSilentMarkerSplitAcrossDeltas(t *testing.T) {
	reg, rec := setup()
	for _, chunk := range []string{"[si", "le", "nt] quiet update"} {
		reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: chunk})
	}
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
	if len(rec.got) != 0 {
		t.Fatalf("split marker must still suppress, got %+v", rec.got)
	}
}

// Status-only events (Thinking/ToolUse) before the text must NOT leak a silent
// turn's activity to the channel.
func TestSilentSuppressesEarlyStatusEvents(t *testing.T) {
	reg, rec := setup()
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Thinking, Text: "..."})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.ToolUse, ToolName: "Bash"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: "[silent] done"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
	if len(rec.got) != 0 {
		t.Fatalf("silent turn must suppress its pre-text status events too, got %+v", rec.got)
	}
}

// A non-silent reply whose text streams in small chunks must forward every
// chunk in order once the decision is made.
func TestNonSilentBufferedThenFlushedInOrder(t *testing.T) {
	reg, rec := setup()
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Thinking, Text: "hmm"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: "Ans"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: "wer here"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
	// Thinking + 2 TextDelta + Done all forwarded, order preserved.
	if len(rec.got) != 4 {
		t.Fatalf("expected 4 forwarded events, got %d: %+v", len(rec.got), rec.got)
	}
	if rec.got[0].Type != event.Thinking {
		t.Fatalf("order not preserved, first = %v", rec.got[0].Type)
	}
	txt := rec.texts()
	if len(txt) != 2 || txt[0] != "Ans" || txt[1] != "wer here" {
		t.Fatalf("text deltas not flushed in order: %+v", txt)
	}
}

func TestSilentIsCaseInsensitiveAndTrimsSpace(t *testing.T) {
	reg, rec := setup()
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: "  [SILENT] hush"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
	if len(rec.got) != 0 {
		t.Fatalf("uppercase/leading-space marker must suppress, got %+v", rec.got)
	}
}

// A silent turn followed by a normal turn: the normal turn forwards and clears
// the silent-last flag.
func TestSilentThenNormalTurn(t *testing.T) {
	reg, rec := setup()
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: "[silent] a"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
	if !reg.WasLastTurnSilent("s1") {
		t.Fatal("first turn should be silent")
	}
	rec.got = nil
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: "real answer"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
	if len(rec.got) != 2 {
		t.Fatalf("second (normal) turn should forward, got %+v", rec.got)
	}
	if reg.WasLastTurnSilent("s1") {
		t.Fatal("silent-last flag should clear after a normal turn")
	}
}

func TestMarkSilentForcesTurnSilent(t *testing.T) {
	reg, rec := setup()
	reg.MarkSilent("s1")
	// Even though the text has no marker, the forced flag suppresses it.
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: "no marker here"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
	if len(rec.got) != 0 {
		t.Fatalf("MarkSilent turn must suppress, got %+v", rec.got)
	}
	if !reg.WasLastTurnSilent("s1") {
		t.Fatal("forced-silent turn should set silent-last")
	}
}

func TestSilentIsPerSession(t *testing.T) {
	reg, rec := setup()
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: "[silent] hidden"})
	reg.DispatchAgentEvent("s2", event.AgentEvent{Type: event.TextDelta, Text: "s2 visible here"})
	reg.DispatchAgentEvent("s2", event.AgentEvent{Type: event.Done})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
	txt := rec.texts()
	if len(txt) != 1 || txt[0] != "s2 visible here" {
		t.Fatalf("only s2 should forward, got %+v", txt)
	}
}
