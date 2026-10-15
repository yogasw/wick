package pool

import (
	"testing"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

// A re-bind without a seen ts (every Slack message re-binds) must keep the
// stored one; a different thread starts unseen.
func TestSetThreadBindingKeepsLastSeen(t *testing.T) {
	p, layout := newPool(t, 1, &scriptedSpawner{})
	setupSession(t, layout, "S")

	b := agentchannels.ThreadBinding{Channel: "slack", ChatID: "C1", ThreadID: "100.000001", Instance: "slack-"}
	seen := b
	seen.LastSeenTS = "100.000500"
	p.SetThreadBinding("S", seen)
	p.SetThreadBinding("S", b)
	if got, _ := p.ThreadBinding("S"); got.LastSeenTS != "100.000500" {
		t.Fatalf("plain re-bind reset last seen: %q", got.LastSeenTS)
	}

	older := b
	older.LastSeenTS = "100.000400"
	p.SetThreadBinding("S", older)
	if got, _ := p.ThreadBinding("S"); got.LastSeenTS != "100.000500" {
		t.Fatalf("last seen moved back: %q", got.LastSeenTS)
	}
	newer := b
	newer.LastSeenTS = "100.000600"
	p.SetThreadBinding("S", newer)
	if got, _ := p.ThreadBinding("S"); got.LastSeenTS != "100.000600" {
		t.Fatalf("last seen did not move forward: %q", got.LastSeenTS)
	}

	other := b
	other.ThreadID = "200.000001"
	p.SetThreadBinding("S", other)
	if got, _ := p.ThreadBinding("S"); got.LastSeenTS != "" || got.ThreadID != "200.000001" {
		t.Fatalf("new thread carried the old last seen: %+v", got)
	}
}

// A binding held for a session that does not exist yet keeps its seen ts
// across a later plain re-bind of the same thread.
func TestPendingBindingKeepsLastSeen(t *testing.T) {
	p, _ := newPool(t, 1, &scriptedSpawner{})
	b := agentchannels.ThreadBinding{Channel: "slack", ChatID: "C1", ThreadID: "100.000001"}
	seen := b
	seen.LastSeenTS = "100.000001"
	p.SetThreadBinding("NEW", seen)
	p.SetThreadBinding("NEW", b)
	v, ok := p.pendingBindings.Load("NEW")
	if !ok || v.(agentchannels.ThreadBinding).LastSeenTS != "100.000001" {
		t.Fatalf("pending binding lost last seen: %+v", v)
	}
}
