package delegation

import (
	"context"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

func layoutForTest(t *testing.T) agentconfig.Layout {
	t.Helper()
	l := agentconfig.NewLayout(t.TempDir())
	if err := l.EnsureLayout(); err != nil {
		t.Fatalf("ensure layout: %v", err)
	}
	return l
}

// A profile's SystemPrompt IS the role. Stored but never applied, a
// profile degrades to "a provider and a turn budget" while still being
// labelled with the role name — the sub-agent answers as a generic
// assistant and the output looks plausible.
//
// This asserts the prompt reaches the child session's meta, which is
// what the spawn factory appends after the preset.
func TestRoleSystemPromptReachesChildSession(t *testing.T) {
	layout := layoutForTest(t)
	const childID = "sub-test-1"

	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: childID}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	profile := &entity.AgentProfile{
		Key: "reviewer", Provider: "claude",
		SystemPrompt: "You review code and return findings ranked by severity.",
	}
	if err := applyRolePrompt(layout, childID, profile); err != nil {
		t.Fatalf("apply: %v", err)
	}

	sess, err := session.Load(layout, childID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if sess.Meta.SystemAddon != profile.SystemPrompt {
		t.Fatalf("system addon = %q, want the role's prompt", sess.Meta.SystemAddon)
	}
}

// A role with no prompt must leave the addon alone rather than blanking
// it — the session may carry an addon from elsewhere.
func TestEmptyRolePromptLeavesAddonUntouched(t *testing.T) {
	layout := layoutForTest(t)
	const childID = "sub-test-2"

	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: childID}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := session.SetSystemAddon(layout, childID, "pre-existing"); err != nil {
		t.Fatalf("seed addon: %v", err)
	}

	if err := applyRolePrompt(layout, childID, &entity.AgentProfile{Key: "bare", Provider: "claude"}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	sess, _ := session.Load(layout, childID)
	if sess.Meta.SystemAddon != "pre-existing" {
		t.Fatalf("addon = %q, want it left alone", sess.Meta.SystemAddon)
	}
}
