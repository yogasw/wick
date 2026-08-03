package pool

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
)

// okLines is a minimal successful turn: init, one assistant message, result.
func okLines() [][]string {
	return [][]string{{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"hi"}`,
	}}
}

// An agent entry that exists but names NO provider used to fall straight
// through to factory.go's per-type default, landing every such spawn on
// claude no matter what the project asked for — and looking identical to a
// deliberate choice. Delegation creates exactly this shape when a role
// names no provider, which is how sub-agents ended up on the wrong
// instance.
func TestSpawnRepairsBlankProviderFromProjectDefault(t *testing.T) {
	sp := &scriptedSpawner{Lines: okLines()}
	p, layout := newPool(t, 2, sp)

	if _, err := project.Create(layout, project.CreateOptions{
		ID:   "proj-1",
		Name: "Proj",
		Defaults: project.Defaults{
			Provider: "wick/x",
			Model:    "set1@leaf-model",
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID:        "S1",
		Origin:    session.OriginUI,
		ProjectID: "proj-1",
	}); err != nil {
		t.Fatal(err)
	}
	// The delegation-created shape: entry present, provider blank.
	if err := session.AddAgent(layout, "S1", "default", ""); err != nil {
		t.Fatal(err)
	}

	if err := p.Send(context.Background(), "S1", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.Active() == 0 }, 2*time.Second)

	sess, err := session.Load(layout, "S1")
	if err != nil {
		t.Fatal(err)
	}
	if got := sess.Agents[0].Provider; got != "wick/x" {
		t.Fatalf("provider = %q, want wick/x — a blank provider must be repaired, not silently defaulted", got)
	}
	// The model travels WITH the provider: a pin belongs to the instance it
	// was chosen on, so inheriting one without the other is meaningless.
	if got := sess.Agents[0].ModelID; got != "set1@leaf-model" {
		t.Fatalf("model = %q, want set1@leaf-model", got)
	}
}

// A pin the user already chose must survive: the project default fills a
// gap, it does not overrule a decision.
func TestSpawnKeepsExistingModelPin(t *testing.T) {
	sp := &scriptedSpawner{Lines: okLines()}
	p, layout := newPool(t, 2, sp)

	if _, err := project.Create(layout, project.CreateOptions{
		ID: "proj-1", Name: "Proj",
		Defaults: project.Defaults{Provider: "wick/x", Model: "project@default"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID: "S1", Origin: session.OriginUI, ProjectID: "proj-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, "S1", "default", ""); err != nil {
		t.Fatal(err)
	}
	if err := session.SetModelID(layout, "S1", "default", "chosen@by-user"); err != nil {
		t.Fatal(err)
	}

	if err := p.Send(context.Background(), "S1", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.Active() == 0 }, 2*time.Second)

	sess, _ := session.Load(layout, "S1")
	if got := sess.Agents[0].ModelID; got != "chosen@by-user" {
		t.Fatalf("model = %q, want the user's own pin kept", got)
	}
}

// An entry that already names a provider is a decision; the project default
// must not touch it, or switching a session's provider would be undone on
// its next spawn.
func TestSpawnLeavesExplicitProviderAlone(t *testing.T) {
	sp := &scriptedSpawner{Lines: okLines()}
	p, layout := newPool(t, 2, sp)

	if _, err := project.Create(layout, project.CreateOptions{
		ID: "proj-1", Name: "Proj",
		Defaults: project.Defaults{Provider: "wick/x", Model: "project@default"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID: "S1", Origin: session.OriginUI, ProjectID: "proj-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, "S1", "default", "claude/work"); err != nil {
		t.Fatal(err)
	}

	if err := p.Send(context.Background(), "S1", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.Active() == 0 }, 2*time.Second)

	sess, _ := session.Load(layout, "S1")
	if got := sess.Agents[0].Provider; got != "claude/work" {
		t.Fatalf("provider = %q, want claude/work left untouched", got)
	}
	// And no model is grafted on from a project pointing at another instance.
	if got := sess.Agents[0].ModelID; got != "" {
		t.Fatalf("model = %q, want empty — wick/x's pin is not claude/work's", got)
	}
}
