package channels

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
)

// recordingChannel captures every agent event fanned out to it, so a test can
// assert which events reached channels and which were suppressed.
type recordingChannel struct {
	got []event.AgentEvent
}

func (c *recordingChannel) Name() string                { return "rec" }
func (c *recordingChannel) Start(context.Context) error { return nil }
func (c *recordingChannel) Stop()                       {}
func (c *recordingChannel) IsConfigured() bool          { return true }
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

// A turn that ends giving the channels nothing (empty or [silent]) gets the
// fallback line first, then its terminal event; a turn with text gets none.
func TestQuietTurnFallbackNotice(t *testing.T) {
	fallback := func(string, time.Time, bool) (string, bool) { return "Team results (no summary from the agent)", true }
	for _, tc := range []struct {
		name   string
		deltas []string
		notice bool
	}{
		{"empty", nil, true},
		{"silent short", []string{"[silent]"}, true},
		{"silent long", []string{"[silent] nothing to add\nreally"}, true},
		{"answer", []string{"Here is the summary of all three"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, rec := setup()
			reg.WithTurnFallback(fallback)
			for _, d := range tc.deltas {
				reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: d})
			}
			reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
			var notices int
			for i, e := range rec.got {
				if e.Type == event.Notice {
					notices++
					if i+1 >= len(rec.got) || rec.got[i+1].Type != event.Done {
						t.Fatalf("notice must come right before Done, got %+v", rec.got)
					}
				}
			}
			if (notices == 1) != tc.notice || notices > 1 {
				t.Fatalf("notices = %d, want notice=%v (%+v)", notices, tc.notice, rec.got)
			}
			if tc.notice && len(rec.texts()) != 0 {
				t.Fatalf("a quiet turn's own text must stay off the channel, got %v", rec.texts())
			}
		})
	}
}

// No fallback for the session = today's behaviour: nothing reaches the channel.
func TestQuietTurnNoFallback(t *testing.T) {
	reg, rec := setup()
	reg.WithTurnFallback(func(string, time.Time, bool) (string, bool) { return "", false })
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: "[silent]"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
	if len(rec.got) != 0 {
		t.Fatalf("silent turn without fallback must not reach channel, got %+v", rec.got)
	}
}

// The hook hears every turn end, with the turn's start and whether it was
// quiet; an empty turn's other held events (a compaction) still go out
// with the fallback line.
func TestQuietTurnKeepsHeldEventsAndReportsStart(t *testing.T) {
	reg, rec := setup()
	type call struct {
		start time.Time
		quiet bool
	}
	var calls []call
	reg.WithTurnFallback(func(_ string, start time.Time, quiet bool) (string, bool) {
		calls = append(calls, call{start, quiet})
		return "results", true
	})
	before := time.Now()
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Warning, ErrorMsg: "w"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
	if len(rec.got) != 3 || rec.got[0].Type != event.Warning || rec.got[1].Type != event.Notice || rec.got[2].Type != event.Done {
		t.Fatalf("want warning, notice, done; got %+v", rec.got)
	}
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: "a real answer here"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
	if len(calls) != 2 || !calls[0].quiet || calls[1].quiet || calls[0].start.Before(before) || !calls[1].start.After(calls[0].start) {
		t.Fatalf("hook calls = %+v", calls)
	}
	for _, e := range rec.got[3:] {
		if e.Type == event.Notice {
			t.Fatal("a turn with text gets no notice")
		}
	}
}
