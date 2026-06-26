package slack

import (
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

// TestSessionKeyNamespacing proves two Slack instances with different
// session prefixes (e.g. App Owner vs a per-user bot, possibly in different
// workspaces) derive DISTINCT session keys from the SAME native threadTS.
// This is the core invariant behind multi-bot isolation: the pool and the
// turns map are keyed on the namespaced id, so an equal threadTS produced
// in two workspaces can never collide.
func TestSessionKeyNamespacing(t *testing.T) {
	a := &Channel{sessionPrefix: "slack:__owner__:"}
	b := &Channel{sessionPrefix: "slack:user-b:"}

	const threadTS = "1700000000.000100" // could legitimately repeat across workspaces
	ka := a.sessionKey(threadTS)
	kb := b.sessionKey(threadTS)

	if ka == kb {
		t.Fatalf("session keys collided across instances: both %q", ka)
	}
	if ka != "slack:__owner__:"+threadTS {
		t.Errorf("instance A key = %q, want prefixed", ka)
	}
	if kb != "slack:user-b:"+threadTS {
		t.Errorf("instance B key = %q, want prefixed", kb)
	}
}

// TestOnAgentEventNoCrossInstanceClobber reproduces the original bug:
// DispatchAgentEvent broadcasts every agent event to ALL registered Slack
// instances. Before namespacing, two bots that saw the same threadTS shared
// a turns-map key, so bot B's buffer absorbed bot A's reply (and vice
// versa). With per-instance session keys, an event addressed to instance A's
// namespaced key must land ONLY in instance A — instance B ignores it
// because its turns map has no such key.
func TestOnAgentEventNoCrossInstanceClobber(t *testing.T) {
	a := &Channel{
		sessionPrefix: "slack:__owner__:",
		turns:         map[string]*turn{},
	}
	b := &Channel{
		sessionPrefix: "slack:user-b:",
		turns:         map[string]*turn{},
	}

	const sharedThreadTS = "1700000000.000200"

	// Each instance seeds its own turn for the SAME native threadTS, exactly
	// as handleMessage would: key = prefix+threadTS, native threadTS kept on
	// the turn for replies.
	keyA := a.sessionKey(sharedThreadTS)
	keyB := b.sessionKey(sharedThreadTS)
	a.turns[keyA] = &turn{channelID: "C_WORKSPACE_A", threadTS: sharedThreadTS}
	b.turns[keyB] = &turn{channelID: "C_WORKSPACE_B", threadTS: sharedThreadTS}

	// Simulate the registry broadcasting instance A's agent reply to BOTH
	// instances (DispatchAgentEvent fans out unconditionally).
	ev := event.AgentEvent{Type: event.TextDelta, Text: "reply meant for workspace A"}
	a.OnAgentEvent(keyA, ev)
	b.OnAgentEvent(keyA, ev) // B receives A's key — must be a no-op

	if got := a.turns[keyA].buf.String(); got != "reply meant for workspace A" {
		t.Errorf("instance A buffer = %q, want the reply", got)
	}
	if got := b.turns[keyB].buf.String(); got != "" {
		t.Fatalf("instance B buffer = %q, want empty — A's reply leaked into B (the original bug)", got)
	}
}

// TestOwnsRequestRoutesToOwningInstance verifies the fan-in dispatcher's
// routing predicate: a /integrations/slack/send call tagged with a session
// id is claimed only by the instance whose prefix matches.
func TestOwnsRequestRoutesToOwningInstance(t *testing.T) {
	a := &Channel{sessionPrefix: "slack:__owner__:"}
	b := &Channel{sessionPrefix: "slack:user-b:"}

	req := httptest.NewRequest("POST", "/integrations/slack/send", nil)
	req.Header.Set("X-Wick-Session-Id", "slack:user-b:1700000000.000300")

	if a.OwnsRequest(req) {
		t.Error("App Owner instance wrongly claimed a user-b session request")
	}
	if !b.OwnsRequest(req) {
		t.Error("user-b instance failed to claim its own session request")
	}

	// No header → unclaimed by everyone (registry falls back to first).
	bare := httptest.NewRequest("POST", "/integrations/slack/send", nil)
	if a.OwnsRequest(bare) || b.OwnsRequest(bare) {
		t.Error("a session-less request should be claimed by no instance")
	}
}
