package pool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

// A sub-agent's result is delivered with NO agent name (nothing populates
// ParentAgent), which used to key on sessionID+"" — matching no live agent, so
// it never reached the leader's process and spawned a nameless second track
// instead. It must land on the leader's real agent, exactly as a person typing
// in the composer would.
func TestSendResolvesAnEmptyAgentNameToTheSessionsAgent(t *testing.T) {
	sp := &scriptedSpawner{Lines: okLines()}
	p, layout := newPool(t, 2, sp)

	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID: "S1", Origin: session.OriginUI,
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, "S1", "main", "codex/codex"); err != nil {
		t.Fatal(err)
	}
	if err := session.SetModelID(layout, "S1", "main", "gpt-5.5"); err != nil {
		t.Fatal(err)
	}

	// Delivered the way DeliverToSession does it: empty agent name.
	if err := p.Send(context.Background(), "S1", "", "subagent", "user", "result"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.Active() == 0 }, 2*time.Second)

	sess, err := session.Load(layout, "S1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Agents) != 1 {
		names := make([]string, 0, len(sess.Agents))
		for _, a := range sess.Agents {
			names = append(names, a.Name)
		}
		t.Fatalf("agents = %v, want only [main] — an empty name must not create a second entry", names)
	}
	// And it kept running on what the session was set to.
	if got := sess.Agents[0].Provider; got != "codex/codex" {
		t.Fatalf("provider = %q, want codex/codex", got)
	}
	if got := sess.Agents[0].ModelID; got != "gpt-5.5" {
		t.Fatalf("model = %q, want gpt-5.5 — the session's own pick", got)
	}
}

// Meta.ActiveAgent wins over the first entry, matching switchProvider and
// session.ActiveRunTarget so every door agrees on who "the agent" is.
func TestSendPrefersTheActiveAgent(t *testing.T) {
	sp := &scriptedSpawner{Lines: okLines()}
	p, layout := newPool(t, 2, sp)

	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID: "S1", Origin: session.OriginUI,
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, "S1", "first", "codex/codex"); err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, "S1", "second", "codex/codex"); err != nil {
		t.Fatal(err)
	}
	if err := session.SetActiveAgent(layout, "S1", "second"); err != nil {
		t.Fatal(err)
	}

	if got := p.resolveAgentName("S1", ""); got != "second" {
		t.Fatalf("resolveAgentName = %q, want second", got)
	}
	if got := p.resolveAgentName("S1", "explicit"); got != "explicit" {
		t.Fatalf("an explicit name must pass through, got %q", got)
	}
}

// The reported problem, at the point where it actually goes wrong: delivering a
// sub-agent's result to main passes an EMPTY agent name, and providerForSession
// keys on that name to decide which provider the respawn uses. With "" it
// matched no entry and returned no provider, so the spawn fell through to a
// per-type default instead of the one the user picked in the composer.
func TestProviderForSessionResolvesAnEmptyAgentName(t *testing.T) {
	sp := &scriptedSpawner{Lines: okLines()}
	p, layout := newPool(t, 2, sp)

	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID: "LEADER", Origin: session.OriginUI,
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, "LEADER", "main", "codex/codex"); err != nil {
		t.Fatal(err)
	}

	// Before the fix this returned ("", "") — "provider unknown".
	name := p.resolveAgentName("LEADER", "")
	pType, pName := p.providerForSession("LEADER", name)
	if pType != "codex" || pName != "codex" {
		t.Fatalf("provider = %q/%q, want codex/codex — main's own provider", pType, pName)
	}

	// And the raw empty name still resolves to nothing, which is why the
	// resolution above has to happen before any provider lookup.
	if t0, n0 := p.providerForSession("LEADER", ""); t0 != "" || n0 != "" {
		t.Fatalf("an unresolved empty name should match no entry, got %q/%q", t0, n0)
	}
}

// Replays the user's real on-disk session shape: meta.json with NO
// active_agent, and agents.json whose second entry has an empty name (the
// artifact of the old profile.Provider naming bug).
func TestResolvesAgainstTheRealOnDiskShape(t *testing.T) {
	sp := &scriptedSpawner{Lines: okLines()}
	p, layout := newPool(t, 2, sp)

	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID: "REAL", Origin: session.OriginUI,
	}); err != nil {
		t.Fatal(err)
	}

	// Hand-write agents.json exactly as it exists in ~/.wick-lab.
	agents := []session.AgentEntry{
		{Name: "main", Provider: "codex/codex", Status: "idle", ModelID: "gpt-5.5"},
		{Name: "", Provider: "claude/claude_default", Status: "idle", ModelID: "opus"},
	}
	b, _ := json.MarshalIndent(agents, "", "  ")
	if err := os.WriteFile(filepath.Join(layout.SessionDir("REAL"), "agents.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	// meta.json has no active_agent, so resolution falls to the first entry.
	name := p.resolveAgentName("REAL", "")
	if name != "main" {
		t.Fatalf("resolveAgentName = %q, want main", name)
	}
	pType, pName := p.providerForSession("REAL", name)
	if pType != "codex" || pName != "codex" {
		t.Fatalf("provider = %q/%q, want codex/codex", pType, pName)
	}
	// Crucially NOT claude/claude_default from the blank-named row.
	if pName == "claude_default" {
		t.Fatal("resolved to the blank-named entry's provider")
	}
}
