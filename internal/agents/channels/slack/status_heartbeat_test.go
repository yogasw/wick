package slack

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

// TestStatusHeartbeatLifecycle verifies the assistant "is thinking…"
// heartbeat starts for a live turn and is torn down on Done — Slack drops
// the status after a 2-minute idle timeout, so a long tool-use turn relies
// on this heartbeat to keep the banner visible.
func TestStatusHeartbeatLifecycle(t *testing.T) {
	s := &Channel{turns: map[string]*turn{}}
	const sid = "slack:__owner__:1700000000.000400"
	s.turns[sid] = &turn{channelID: "C1", threadTS: "1700000000.000400"}

	s.startStatusHeartbeat(sid)

	s.mu.Lock()
	tk := s.turns[sid].statusTicker
	stop := s.turns[sid].statusStop
	s.mu.Unlock()
	if tk == nil || stop == nil {
		t.Fatal("heartbeat did not start (ticker/stop nil)")
	}

	// Starting again must be a no-op (no second ticker leak).
	s.startStatusHeartbeat(sid)
	s.mu.Lock()
	if s.turns[sid].statusTicker != tk {
		t.Error("startStatusHeartbeat started a second ticker for the same turn")
	}
	s.mu.Unlock()

	// Done tears the heartbeat down. api is nil so setAssistantStatus is a
	// safe no-op; we only assert the heartbeat state.
	s.OnAgentEvent(sid, event.AgentEvent{Type: event.Done})

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turns[sid].statusTicker != nil || s.turns[sid].statusStop != nil {
		t.Error("Done did not stop the status heartbeat")
	}
	// The stop channel must be closed so the goroutine exits.
	select {
	case <-stop:
	default:
		t.Error("stop channel not closed after Done")
	}
}

// TestStatusHeartbeatStoppedOnTurnReplace verifies a superseding turn stops
// the prior turn's heartbeat so the goroutine doesn't outlive its turn.
func TestStatusHeartbeatStoppedOnTurnReplace(t *testing.T) {
	s := &Channel{turns: map[string]*turn{}}
	const sid = "slack:__owner__:1700000000.000500"
	old := &turn{channelID: "C1", threadTS: "1700000000.000500"}
	s.turns[sid] = old
	s.startStatusHeartbeat(sid)

	s.mu.Lock()
	stop := old.statusStop
	// Simulate handleMessage replacing the turn.
	s.stopStatusHeartbeat(old)
	s.mu.Unlock()

	if old.statusTicker != nil {
		t.Error("old turn ticker not cleared on replace")
	}
	select {
	case <-stop:
	default:
		t.Error("old turn stop channel not closed on replace")
	}
}
