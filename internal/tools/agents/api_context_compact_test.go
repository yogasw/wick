package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
)

// TestContextProviderTypePrefersTheLedger: once a turn has finished, the
// provider that answered is the one whose window the panel is showing,
// so its type decides whether Compact is offered.
func TestContextProviderTypePrefersTheLedger(t *testing.T) {
	sess := session.Session{
		Meta:   session.Meta{ActiveAgent: "default"},
		Agents: []session.AgentEntry{{Name: "default", Provider: "claude/ent"}},
	}
	if got := contextProviderType(sess, "codex/default"); got != "codex" {
		t.Errorf("type = %q, want codex — the ledger's active provider wins", got)
	}
}

// TestContextProviderTypeFallsBackToTheSession: before the first turn
// there is no ledger entry, but /compact typed right now would still
// reach a specific provider — the one the session is pointed at.
func TestContextProviderTypeFallsBackToTheSession(t *testing.T) {
	sess := session.Session{
		Meta:   session.Meta{ActiveAgent: "default"},
		Agents: []session.AgentEntry{{Name: "default", Provider: "codex/default"}},
	}
	if got := contextProviderType(sess, ""); got != "codex" {
		t.Errorf("type = %q, want codex", got)
	}
	empty := session.Session{}
	if got := contextProviderType(empty, ""); got != "" {
		t.Errorf("type = %q, want empty for a session with no agent yet", got)
	}
}

// TestCanCompactByProvider pins the capability itself: unknown stays
// capable, because a missing button is a worse guess than a useless one.
func TestCanCompactByProvider(t *testing.T) {
	if provider.CanCompact(provider.TypeCodex) {
		t.Error("codex exec has no slash commands — /compact must not be offered")
	}
	for _, typ := range []provider.Type{provider.TypeClaude, provider.TypeWick, provider.Type("something-new")} {
		if !provider.CanCompact(typ) {
			t.Errorf("%s: want compactable", typ)
		}
	}
}
