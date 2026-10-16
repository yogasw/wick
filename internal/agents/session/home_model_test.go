package session

import (
	"context"
	"testing"
)

// A provider set before its model (a workflow node, a spawn repairing a
// blank entry) records the home with the model it ends up on, not the
// pin it had before; a pin set on a stand-in leaves the home alone.
func TestModelPinAfterProviderFollowsHome(t *testing.T) {
	layout := newLayout(t)
	if _, err := Create(context.Background(), layout, CreateOptions{ID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if err := AddAgent(layout, "s1", "main", "claude/a"); err != nil {
		t.Fatal(err)
	}
	if err := SetModelID(layout, "s1", "main", "old"); err != nil {
		t.Fatal(err)
	}
	home := func() FailoverState {
		t.Helper()
		sess, err := Load(layout, "s1")
		if err != nil || sess.Agents[0].Failover == nil {
			t.Fatalf("load: %v", err)
		}
		return *sess.Agents[0].Failover
	}

	if err := SetAgentProvider(layout, "s1", "main", "codex/b"); err != nil {
		t.Fatal(err)
	}
	if err := SetModelID(layout, "s1", "main", "new"); err != nil {
		t.Fatal(err)
	}
	if h := home(); h.HomeProvider != "codex/b" || h.HomeModelID != "new" {
		t.Fatalf("home = %+v, want codex/b with the model set after it", h)
	}

	// SetModelIDIfEmpty, the spawn's repair path, does the same.
	if err := SetModelID(layout, "s1", "main", ""); err != nil {
		t.Fatal(err)
	}
	if err := SetAgentProvider(layout, "s1", "main", "claude/a"); err != nil {
		t.Fatal(err)
	}
	if err := SetModelIDIfEmpty(layout, "s1", "main", "proj"); err != nil {
		t.Fatal(err)
	}
	if h := home(); h.HomeProvider != "claude/a" || h.HomeModelID != "proj" {
		t.Fatalf("home = %+v, want claude/a with the project's model", h)
	}

	// On a stand-in the pin is the stand-in's.
	if err := UpdateAgents(layout, "s1", func(agents []AgentEntry) ([]AgentEntry, bool, error) {
		agents[0].Provider = "claude/c"
		return agents, true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := SetModelID(layout, "s1", "main", "standin"); err != nil {
		t.Fatal(err)
	}
	if h := home(); h.HomeModelID != "proj" {
		t.Fatalf("home model = %q, a stand-in pin moved it", h.HomeModelID)
	}
}
