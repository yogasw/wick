package slack

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

// The footer is the coarse state Slack shows next to the spinner. A relayed
// sub-agent label carries a "<name> → " prefix, so an exact-match switch missed
// it and reported "Working" even while the child was thinking — the phase the
// operator most wants to see is in progress.
func TestFooterStateReadsThroughSubAgentPrefix(t *testing.T) {
	cases := []struct{ label, want string }{
		// Leader's own labels — unchanged behaviour.
		{"", footerIdle},
		{footerThinking, footerThinking},
		{"Reading store.go", footerWorking},

		// Relayed sub-agent labels.
		{"researcher" + subAgentArrow + footerThinking, footerThinking},
		{"researcher" + subAgentArrow + "Reading store.go", footerWorking},
		{"data-validator" + subAgentArrow + statusLabelWorking, footerWorking},
	}
	for _, tc := range cases {
		if got := footerState(tc.label); got != tc.want {
			t.Errorf("footerState(%q) = %q, want %q", tc.label, got, tc.want)
		}
	}
}

// End to end through the relay branch: a child's Thinking event must leave the
// banner in the Thinking state, not Working.
func TestRelayedThinkingPaintsThinkingFooter(t *testing.T) {
	c := &Channel{turns: map[string]*turn{
		"slack-123": {channelID: "C1", threadTS: "1.1", running: true},
	}}

	c.OnAgentEvent("slack-123", event.AgentEvent{
		Type: event.Thinking, SubAgent: "researcher", Text: "weighing options",
	})

	c.mu.Lock()
	label := c.turns["slack-123"].statusLabel
	c.stopStatusAnimation(c.turns["slack-123"])
	c.mu.Unlock()

	if want := "researcher" + subAgentArrow + footerThinking; label != want {
		t.Fatalf("label = %q, want %q", label, want)
	}
	if got := footerState(label); got != footerThinking {
		t.Fatalf("footer = %q, want %q — a thinking child must not read as Working", got, footerThinking)
	}
}

// Ageing out a stale label drops the activity (it may be over) but keeps the
// sub-agent's name: "who is still out there" is still true and is how a
// delegated run is read.
func TestStaleLabelKeepsSubAgentName(t *testing.T) {
	got := staleLabel("researcher" + subAgentArrow + "Reading store.go")
	if want := "researcher" + subAgentArrow + statusLabelWorking; got != want {
		t.Fatalf("staleLabel = %q, want %q", got, want)
	}
}

// A leader's own stale label has no name to keep.
func TestStaleLabelForLeaderIsPlainWorking(t *testing.T) {
	if got := staleLabel("Reading store.go"); got != statusLabelWorking {
		t.Fatalf("staleLabel = %q, want %q", got, statusLabelWorking)
	}
	if got := staleLabel(""); got != statusLabelWorking {
		t.Fatalf("staleLabel(empty) = %q, want %q", got, statusLabelWorking)
	}
}

// The label is written in one place and parsed back in another; a mismatch
// silently degrades every child phase. Guard the round-trip.
func TestSubAgentLabelRoundTripsThroughFooterState(t *testing.T) {
	label := subAgentStatusLabel(event.AgentEvent{
		Type: event.Thinking, SubAgent: "coder",
	})
	if got := footerState(label); got != footerThinking {
		t.Fatalf("round-trip broken: label %q → footer %q, want %q", label, got, footerThinking)
	}
}
