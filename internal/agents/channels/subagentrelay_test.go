package channels

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

// relayChannel records which session each event was addressed to, which is the
// whole point of the relay — a child's progress must arrive under its LEADER's
// session, not its own. liveTurns lists the sessions it claims a banner for.
type relayChannel struct {
	liveTurns map[string]bool
	sessions  []string
	got       []event.AgentEvent
}

func newRelayChannel(live ...string) *relayChannel {
	m := map[string]bool{}
	for _, s := range live {
		m[s] = true
	}
	return &relayChannel{liveTurns: m}
}

func (c *relayChannel) Name() string                { return "relayrec" }
func (c *relayChannel) Start(context.Context) error { return nil }
func (c *relayChannel) Stop()                       {}
func (c *relayChannel) IsConfigured() bool          { return true }
func (c *relayChannel) HasLiveTurn(sid string) bool { return c.liveTurns[sid] }
func (c *relayChannel) OnAgentEvent(sid string, ev event.AgentEvent) {
	c.sessions = append(c.sessions, sid)
	c.got = append(c.got, ev)
}

func relaySetup(live ...string) (*Registry, *relayChannel) {
	rec := newRelayChannel(live...)
	reg := NewRegistry()
	reg.Add(rec, nil)
	return reg, rec
}

// The bug this fixes: a sub-agent's tool activity reached no channel at all, so
// a leader blocked on it left the banner frozen on "Delegating: …".
func TestSubAgentToolUseRelaysToLeaderSession(t *testing.T) {
	reg, rec := relaySetup("slack-123")
	reg.DispatchAgentEventFrom("slack-123--sub-a1b2", "researcher",
		event.AgentEvent{Type: event.ToolUse, ToolName: "Read", ToolInput: `{"file_path":"store.go"}`})

	if len(rec.got) != 1 {
		t.Fatalf("child progress must reach the channel, got %d events", len(rec.got))
	}
	if rec.sessions[0] != "slack-123" {
		t.Fatalf("relayed to %q, want the leader session %q", rec.sessions[0], "slack-123")
	}
	if rec.got[0].SubAgent != "researcher" {
		t.Fatalf("SubAgent = %q, want the child's agent name", rec.got[0].SubAgent)
	}
	if rec.got[0].ToolName != "Read" {
		t.Fatalf("tool detail lost in relay: %+v", rec.got[0])
	}
}

func TestSubAgentThinkingRelays(t *testing.T) {
	reg, rec := relaySetup("slack-123")
	reg.DispatchAgentEventFrom("slack-123--sub-a1b2", "coder",
		event.AgentEvent{Type: event.Thinking, Text: "weighing options"})
	if len(rec.got) != 1 || rec.got[0].Type != event.Thinking {
		t.Fatalf("child thinking should relay, got %+v", rec.got)
	}
}

// The child's reply is delivered as the delegation result. Relaying TextDelta
// too would post the same answer to the thread twice.
func TestSubAgentTextIsNotRelayed(t *testing.T) {
	reg, rec := relaySetup("slack-123")
	reg.DispatchAgentEventFrom("slack-123--sub-a1b2", "researcher",
		event.AgentEvent{Type: event.TextDelta, Text: "here is my report"})
	if len(rec.got) != 0 {
		t.Fatalf("child text must not reach the channel, got %+v", rec.got)
	}
}

// A child's Done must not end the leader's turn — the leader is still blocked
// waiting for the delegation result.
func TestSubAgentDoneIsNotRelayed(t *testing.T) {
	reg, rec := relaySetup("slack-123")
	reg.DispatchAgentEventFrom("slack-123--sub-a1b2", "researcher", event.AgentEvent{Type: event.Done})
	if len(rec.got) != 0 {
		t.Fatalf("child Done must not end the leader turn, got %+v", rec.got)
	}
}

func TestSubAgentErrorIsNotRelayed(t *testing.T) {
	reg, rec := relaySetup("slack-123")
	reg.DispatchAgentEventFrom("slack-123--sub-a1b2", "researcher",
		event.AgentEvent{Type: event.Error, ErrorMsg: "boom"})
	if len(rec.got) != 0 {
		t.Fatalf("child error is reported via the delegation result, got %+v", rec.got)
	}
}

// Nested delegation: a grandchild's progress belongs on the nearest ancestor
// that actually has a banner, walking up past the middle child.
func TestGrandchildRelaysToNearestChannelAncestor(t *testing.T) {
	reg, rec := relaySetup("slack-123")
	reg.DispatchAgentEventFrom("slack-123--sub-mid--sub-deep", "auditor",
		event.AgentEvent{Type: event.ToolUse, ToolName: "Grep"})
	if len(rec.got) != 1 {
		t.Fatalf("grandchild progress should relay, got %+v", rec.got)
	}
	if rec.sessions[0] != "slack-123" {
		t.Fatalf("relayed to %q, want %q", rec.sessions[0], "slack-123")
	}
}

// When an intermediate ancestor holds the live banner, that one wins over the
// root — it is the thread the operator is watching.
func TestGrandchildPrefersNearestLiveAncestor(t *testing.T) {
	reg, rec := relaySetup("slack-123", "slack-123--sub-mid")
	reg.DispatchAgentEventFrom("slack-123--sub-mid--sub-deep", "auditor",
		event.AgentEvent{Type: event.ToolUse, ToolName: "Grep"})
	if len(rec.got) != 1 || rec.sessions[0] != "slack-123--sub-mid" {
		t.Fatalf("relayed to %v, want the nearest live ancestor", rec.sessions)
	}
}

// A delegation started from the web UI has no channel thread. Nothing to paint,
// and the web UI already streams the child directly.
func TestSubAgentWithNoChannelAncestorIsDropped(t *testing.T) {
	reg, rec := relaySetup() // no live turns anywhere
	reg.DispatchAgentEventFrom("ui-session--sub-a1b2", "researcher",
		event.AgentEvent{Type: event.ToolUse, ToolName: "Read"})
	if len(rec.got) != 0 {
		t.Fatalf("no ancestor banner means nothing to relay, got %+v", rec.got)
	}
}

// Relaying must not leave silent-turn state behind under the child's session:
// a status-only stream has no reply to decide about, so nothing would clear it.
func TestSubAgentRelayLeavesNoTurnState(t *testing.T) {
	reg, _ := relaySetup("slack-123")
	child := "slack-123--sub-a1b2"
	reg.DispatchAgentEventFrom(child, "researcher", event.AgentEvent{Type: event.ToolUse, ToolName: "Read"})
	reg.DispatchAgentEventFrom(child, "researcher", event.AgentEvent{Type: event.TextDelta, Text: "report"})

	reg.mu.Lock()
	_, tracked := reg.turns[child]
	reg.mu.Unlock()
	if tracked {
		t.Fatal("child session must not accumulate silent-turn state")
	}
}

// The leader's own events keep flowing through the normal path untouched — the
// relay must not hijack a non-sub session.
func TestLeaderEventsUnaffectedByRelay(t *testing.T) {
	reg, rec := relaySetup("slack-123")
	reg.DispatchAgentEventFrom("slack-123", "leader",
		event.AgentEvent{Type: event.TextDelta, Text: "leader speaking"})
	reg.DispatchAgentEventFrom("slack-123", "leader", event.AgentEvent{Type: event.Done})
	if len(rec.got) != 2 {
		t.Fatalf("leader turn should forward normally, got %+v", rec.got)
	}
	for _, e := range rec.got {
		if e.SubAgent != "" {
			t.Fatalf("leader event wrongly tagged as sub-agent: %+v", e)
		}
	}
}

// A leader's [silent] reply is still suppressed; the relay bypass applies only
// to a child's status events.
func TestSilentLeaderStillSuppressedWithRelayWired(t *testing.T) {
	reg, rec := relaySetup("slack-123")
	reg.DispatchAgentEventFrom("slack-123", "leader",
		event.AgentEvent{Type: event.TextDelta, Text: "[silent] working quietly"})
	reg.DispatchAgentEventFrom("slack-123", "leader", event.AgentEvent{Type: event.Done})
	if len(rec.got) != 0 {
		t.Fatalf("silent leader reply must stay off-channel, got %+v", rec.got)
	}
}

// Without an agent name the label falls back to the session seed rather than
// showing nothing — attribution matters more than prettiness here.
func TestSubAgentLabelFallsBackToSeed(t *testing.T) {
	if got := subAgentLabel("slack-123--sub-a1b2c3", ""); got != "sub-agent a1b2c3" {
		t.Fatalf("label = %q", got)
	}
	if got := subAgentLabel("slack-123--sub-a1b2c3", "researcher"); got != "researcher" {
		t.Fatalf("named label = %q", got)
	}
}

// A channel that does not implement LiveTurnReporter simply never receives
// relayed progress — it must not be treated as owning every session.
func TestChannelWithoutLiveTurnReporterGetsNoRelay(t *testing.T) {
	rec := &recordingChannel{} // no HasLiveTurn
	reg := NewRegistry()
	reg.Add(rec, nil)
	reg.DispatchAgentEventFrom("slack-123--sub-a1b2", "researcher",
		event.AgentEvent{Type: event.ToolUse, ToolName: "Read"})
	if len(rec.got) != 0 {
		t.Fatalf("non-reporting channel must not receive relay, got %+v", rec.got)
	}
}
