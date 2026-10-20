package channels

import (
	"context"
	"strings"
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

// A Team agent woken with its teammates' results that answers with
// [silent] or nothing posts nothing: the hook's "Results came in…" line
// never reaches a channel. An answer goes out as usual.
func TestQuietTurnNeverPostsFallback(t *testing.T) {
	fallback := func(string, time.Time, bool) (string, bool) {
		return "Results came in, but the agent gave no summary:\n• Result 1 — completed: x", true
	}
	for _, tc := range []struct {
		name   string
		deltas []string
		texts  int
	}{
		{"empty", nil, 0},
		{"silent short", []string{"[silent]"}, 0},
		{"silent long", []string{"[silent] notes for client A updated, nothing to send to the thread"}, 0},
		{"answer", []string{"Here is the summary of all three"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg, rec := setup()
			reg.WithTurnFallback(fallback)
			for _, d := range tc.deltas {
				reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: d})
			}
			reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
			for _, e := range rec.got {
				if e.Type == event.Notice || strings.Contains(e.Text, "Results came in") {
					t.Fatalf("the fallback reached the channel: %+v", rec.got)
				}
			}
			if got := len(rec.texts()); (got > 0) != (tc.texts > 0) {
				t.Fatalf("texts = %v, want %d", rec.texts(), tc.texts)
			}
		})
	}
}

// Silent without a hook: nothing reaches the channel.
func TestQuietTurnNoFallback(t *testing.T) {
	reg, rec := setup()
	reg.WithTurnFallback(func(string, time.Time, bool) (string, bool) { return "", false })
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: "[silent]"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
	if len(rec.got) != 0 {
		t.Fatalf("silent turn without fallback must not reach channel, got %+v", rec.got)
	}
}

// The hook still hears every turn end, with the turn's start and whether
// it was quiet (the Team wake marks are settled there); an empty turn's
// other held events (a warning) still go out, without any fallback line.
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
	if len(rec.got) != 2 || rec.got[0].Type != event.Warning || rec.got[1].Type != event.Done {
		t.Fatalf("want warning, done; got %+v", rec.got)
	}
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: "a real answer here"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
	if len(calls) != 2 || !calls[0].quiet || calls[1].quiet || calls[0].start.Before(before) || !calls[1].start.After(calls[0].start) {
		t.Fatalf("hook calls = %+v", calls)
	}
	for _, e := range rec.got {
		if e.Type == event.Notice {
			t.Fatal("no turn gets a fallback notice")
		}
	}
}

// TestDispatchNoticeOutsideTurn: a failover notice sent between turns
// reaches the channel at once as its own Notice, and does not decide the
// next turn — a silent reply after it is still dropped.
func TestDispatchNoticeOutsideTurn(t *testing.T) {
	reg, rec := setup()
	reg.DispatchNotice("s1", "⚠️ Back to home claude/a")
	if len(rec.got) != 1 || rec.got[0].Type != event.Notice || rec.got[0].Text != "⚠️ Back to home claude/a" {
		t.Fatalf("notice not forwarded as a Notice: %+v", rec.got)
	}
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.TextDelta, Text: "[silent] nothing new"})
	reg.DispatchAgentEvent("s1", event.AgentEvent{Type: event.Done})
	if len(rec.got) != 1 {
		t.Fatalf("silent reply after the notice must still be dropped, got %+v", rec.got)
	}
	reg.DispatchNotice("s1--sub-x", "ignored")
	reg.DispatchNotice("s1", "")
	if len(rec.got) != 1 {
		t.Fatalf("sub-agent / empty notice must not be sent: %+v", rec.got)
	}
}
