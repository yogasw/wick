package slack

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
)

// The banner used to stick because nothing revoked a label between events: the
// keep-alive re-asserted the last activity forever. These cover the three ways
// it got stuck.

// 1. A finished turn must not have its keep-alive revived by a later event.
// Before this guard, a relayed sub-agent event after Done restarted the ticker
// with nothing left to stop it — the banner pinged Slack indefinitely.
func TestStatusAnimationNotRevivedAfterTurnEnded(t *testing.T) {
	c := &Channel{turns: map[string]*turn{
		"slack-123": {channelID: "C1", threadTS: "1.1", running: false},
	}}

	c.startStatusAnimation("slack-123")

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.turns["slack-123"].statusTicker != nil {
		t.Fatal("keep-alive must not start for a turn that already ended")
	}
}

// setStatusLabel starts the keep-alive lazily, so it is the other door into the
// same revival. A relayed child event after Done must not reopen it.
func TestSetStatusLabelDoesNotReviveEndedTurn(t *testing.T) {
	c := &Channel{turns: map[string]*turn{
		"slack-123": {channelID: "C1", threadTS: "1.1", running: false},
	}}

	c.setStatusLabel("slack-123", "researcher → Working")

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.turns["slack-123"].statusTicker != nil {
		t.Fatal("ended turn must stay without a keep-alive ticker")
	}
}

// 2. Every set stamps lastActivity — including a repeat of the same label, which
// is still proof of life and must not be treated as silence.
func TestSetStatusLabelStampsActivityEvenWhenUnchanged(t *testing.T) {
	stale := time.Now().Add(-10 * time.Minute)
	c := &Channel{turns: map[string]*turn{
		"slack-123": {
			channelID: "C1", threadTS: "1.1", running: true,
			statusLabel: "researcher → Working", lastActivity: stale, staleShown: true,
		},
	}}

	c.setStatusLabel("slack-123", "researcher → Working") // same label

	c.mu.Lock()
	got := c.turns["slack-123"]
	c.mu.Unlock()
	if !got.lastActivity.After(stale) {
		t.Fatal("repeat of the same label must refresh lastActivity")
	}
	if got.staleShown {
		t.Fatal("fresh activity must clear the stale flag")
	}
	// The ticker is expected to have started here — this turn IS running.
	c.mu.Lock()
	running := got.statusTicker != nil
	c.mu.Unlock()
	if !running {
		t.Fatal("a live turn should have its keep-alive running")
	}
	c.mu.Lock()
	c.stopStatusAnimation(got)
	c.mu.Unlock()
}

// A live turn keeps its stamp fresh as events arrive, so an ordinary gap
// between tool calls never trips the stale downgrade.
func TestActivityStampAdvancesWithEvents(t *testing.T) {
	c := &Channel{turns: map[string]*turn{
		"slack-123": {channelID: "C1", threadTS: "1.1", running: true, lastActivity: time.Now().Add(-time.Minute)},
	}}
	before := c.turns["slack-123"].lastActivity

	c.OnAgentEvent("slack-123", event.AgentEvent{
		Type: event.ToolUse, SubAgent: "researcher", ToolName: "Read",
	})

	c.mu.Lock()
	got := c.turns["slack-123"]
	after := got.lastActivity
	c.stopStatusAnimation(got)
	c.mu.Unlock()
	if !after.After(before) {
		t.Fatal("a relayed event must refresh the activity stamp")
	}
}

// 3. Done clears running, so nothing downstream treats the thread as live.
// This is what stops a relayed child from painting a finished conversation.
func TestDoneClearsRunningAndStopsTicker(t *testing.T) {
	c := &Channel{turns: map[string]*turn{
		"slack-123": {channelID: "C1", threadTS: "1.1", running: true, lastActivity: time.Now()},
	}}
	c.startStatusAnimation("slack-123")
	c.mu.Lock()
	started := c.turns["slack-123"].statusTicker != nil
	c.mu.Unlock()
	if !started {
		t.Fatal("precondition: keep-alive should be running for a live turn")
	}

	c.OnAgentEvent("slack-123", event.AgentEvent{Type: event.Done})

	c.mu.Lock()
	defer c.mu.Unlock()
	got := c.turns["slack-123"]
	if got.running {
		t.Fatal("Done must clear running")
	}
	if got.statusTicker != nil {
		t.Fatal("Done must stop the keep-alive ticker")
	}
}

// staleActivityAfter must sit well above a normal tool call so ordinary gaps
// never downgrade a label that is genuinely current.
func TestStaleThresholdLeavesRoomForNormalToolCalls(t *testing.T) {
	if staleActivityAfter < 30*time.Second {
		t.Fatalf("staleActivityAfter = %v, too aggressive for a slow tool call", staleActivityAfter)
	}
	if staleActivityAfter <= statusAnimInterval {
		t.Fatalf("staleActivityAfter (%v) must exceed one animation tick (%v)",
			staleActivityAfter, statusAnimInterval)
	}
}

// 4. A NEW turn on a thread whose last turn ended gets its banner back. Only an
// inbound Slack message created a fresh (running) entry, so a turn nobody typed
// in the thread — a teammate's result waking the lead, a workflow re-run —
// worked for minutes with no thinking banner at all.
func TestNewTurnWithoutSlackMessageReopensBanner(t *testing.T) {
	for _, ev := range []event.AgentEvent{
		{Type: event.Thinking},
		{Type: event.ToolUse, ToolName: "Read"},
		{Type: event.TextDelta, Text: "x"},
	} {
		c := &Channel{turns: map[string]*turn{
			"slack-123": {channelID: "C1", threadTS: "1.1", running: false, statusLabel: "Reading old.go"},
		}}

		c.OnAgentEvent("slack-123", ev)

		c.mu.Lock()
		got := c.turns["slack-123"]
		running, ticking := got.running, got.statusTicker != nil
		c.stopStatusAnimation(got)
		c.stopEditTicker(got)
		c.mu.Unlock()
		if !running || !ticking {
			t.Fatalf("%s on an ended turn: running=%v ticker=%v, want both", ev.Type, running, ticking)
		}
	}
}

// A sub-agent relayed after Done is NOT a new turn of the lead: it stays out.
func TestRelayedSubAgentDoesNotReopenEndedTurn(t *testing.T) {
	c := &Channel{turns: map[string]*turn{
		"slack-123": {channelID: "C1", threadTS: "1.1", running: false},
	}}

	c.OnAgentEvent("slack-123", event.AgentEvent{Type: event.ToolUse, SubAgent: "researcher", ToolName: "Read"})

	c.mu.Lock()
	defer c.mu.Unlock()
	if got := c.turns["slack-123"]; got.running || got.statusTicker != nil {
		t.Fatal("a relayed sub-agent event must not reopen the lead's ended turn")
	}
}
