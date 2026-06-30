package slack

import (
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// TestOwnsSession_AppOwnerDoesNotClaimPerUser guards the prefix-ambiguity bug:
// the App Owner instance's prefix "slack-" is also a prefix of every per-user
// session id "slack-<uuid>-<ts>". A naive HasPrefix made the App Owner claim
// per-user sessions and stamp the wrong bot in the footer.
func TestOwnsSession_AppOwnerDoesNotClaimPerUser(t *testing.T) {
	owner := New(agentconfig.SlackChannelConfig{})
	owner.SetSessionPrefix("slack-") // App Owner

	user := NewWithOwner(agentconfig.SlackChannelConfig{}, "ec0c0b8b-uuid")
	user.SetSessionPrefix("slack-ec0c0b8b-uuid-") // per-user

	appSession := "slack-1700000000.000100"
	userSession := "slack-ec0c0b8b-uuid-1700000000.000100"

	if !owner.OwnsSession(appSession) {
		t.Errorf("App Owner must own its own session %q", appSession)
	}
	if owner.OwnsSession(userSession) {
		t.Errorf("App Owner must NOT claim per-user session %q", userSession)
	}
	if !user.OwnsSession(userSession) {
		t.Errorf("per-user instance must own its session %q", userSession)
	}
	if user.OwnsSession(appSession) {
		t.Errorf("per-user instance must NOT claim App Owner session %q", appSession)
	}
}
