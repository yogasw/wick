package session

import (
	"context"
	"errors"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
)

func mkSession(t *testing.T, layout config.Layout, id string) {
	t.Helper()
	if _, err := Create(context.Background(), layout, CreateOptions{ID: id, Origin: OriginUI}); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
}

// The pair must come off ONE entry — the active one — because a model id
// only resolves against the instance it was chosen on.
func TestActiveRunTargetReadsActiveAgentEntry(t *testing.T) {
	layout := newLayout(t)
	mkSession(t, layout, "S1")
	if err := AddAgent(layout, "S1", "main", "claude/work"); err != nil {
		t.Fatal(err)
	}
	if err := AddAgent(layout, "S1", "second", "wick/x"); err != nil {
		t.Fatal(err)
	}
	if err := SetModelID(layout, "S1", "second", "set1@gpt-5"); err != nil {
		t.Fatal(err)
	}
	if err := SetActiveAgent(layout, "S1", "second"); err != nil {
		t.Fatal(err)
	}

	prov, model, ok := ActiveRunTarget(layout, "S1")
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if prov != "wick/x" || model != "set1@gpt-5" {
		t.Fatalf("got (%q,%q), want (wick/x, set1@gpt-5)", prov, model)
	}
}

// A session created outside the UI flow may never have had an active agent
// set. Treating that as "runs on nothing" would silently drop the
// inheritance a caller asked for, so the first entry answers instead.
func TestActiveRunTargetFallsBackToFirstEntry(t *testing.T) {
	layout := newLayout(t)
	mkSession(t, layout, "S1")
	if err := AddAgent(layout, "S1", "main", "wick/x"); err != nil {
		t.Fatal(err)
	}
	prov, _, ok := ActiveRunTarget(layout, "S1")
	if !ok || prov != "wick/x" {
		t.Fatalf("got (%q, ok=%v), want wick/x", prov, ok)
	}
}

// "No entries" and "an entry naming no provider" are different answers:
// the first is a failed lookup, the second is a real "no opinion".
func TestActiveRunTargetDistinguishesUnknownFromNoOpinion(t *testing.T) {
	layout := newLayout(t)
	mkSession(t, layout, "S1")

	if _, _, ok := ActiveRunTarget(layout, "S1"); ok {
		t.Error("a session with no agent entries should report ok=false")
	}
	if _, _, ok := ActiveRunTarget(layout, "missing"); ok {
		t.Error("an unreadable session should report ok=false")
	}

	if err := AddAgent(layout, "S1", "main", ""); err != nil {
		t.Fatal(err)
	}
	prov, _, ok := ActiveRunTarget(layout, "S1")
	if !ok {
		t.Fatal("an entry with a blank provider is a real answer; want ok=true")
	}
	if prov != "" {
		t.Fatalf("provider = %q, want empty", prov)
	}
}

// Repointing must preserve the rest of the entry: the resume id is what
// makes --resume work, and re-adding the agent would drop it.
func TestSetAgentProviderKeepsResumeID(t *testing.T) {
	layout := newLayout(t)
	mkSession(t, layout, "S1")
	if err := AddAgent(layout, "S1", "main", ""); err != nil {
		t.Fatal(err)
	}
	if err := SetCLISessionID(layout, "S1", "main", "cli-abc"); err != nil {
		t.Fatal(err)
	}
	if err := SetAgentProvider(layout, "S1", "main", "wick/x"); err != nil {
		t.Fatal(err)
	}

	sess, err := Load(layout, "S1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Agents[0].Provider != "wick/x" {
		t.Fatalf("provider = %q, want wick/x", sess.Agents[0].Provider)
	}
	if sess.Agents[0].CLISessionID != "cli-abc" {
		t.Fatalf("resume id = %q, want it preserved", sess.Agents[0].CLISessionID)
	}
}

// An inherited default must never move a pin the user chose deliberately.
func TestSetModelIDIfEmptyDoesNotOverwriteExistingPin(t *testing.T) {
	layout := newLayout(t)
	mkSession(t, layout, "S1")
	if err := AddAgent(layout, "S1", "main", "wick/x"); err != nil {
		t.Fatal(err)
	}
	if err := SetModelID(layout, "S1", "main", "chosen@by-user"); err != nil {
		t.Fatal(err)
	}
	if err := SetModelIDIfEmpty(layout, "S1", "main", "project@default"); err != nil {
		t.Fatal(err)
	}
	sess, _ := Load(layout, "S1")
	if sess.Agents[0].ModelID != "chosen@by-user" {
		t.Fatalf("model = %q, want the user's own pin kept", sess.Agents[0].ModelID)
	}

	// Empty pin: the inherited default does apply.
	if err := SetModelID(layout, "S1", "main", ""); err != nil {
		t.Fatal(err)
	}
	if err := SetModelIDIfEmpty(layout, "S1", "main", "project@default"); err != nil {
		t.Fatal(err)
	}
	sess, _ = Load(layout, "S1")
	if sess.Agents[0].ModelID != "project@default" {
		t.Fatalf("model = %q, want the inherited default", sess.Agents[0].ModelID)
	}
}

// A blank agent name is always a caller bug. The setters create an entry when
// the name does not match, so writing one produced a row nothing could ever
// address again — and every later call appended another, making the session
// look like it held agents nobody created.
func TestSettersRejectABlankAgentName(t *testing.T) {
	layout := newLayout(t)
	mkSession(t, layout, "S1")
	if err := AddAgent(layout, "S1", "main", "codex/codex"); err != nil {
		t.Fatal(err)
	}

	if err := SetModelID(layout, "S1", "", "opus"); !errors.Is(err, ErrNoAgentName) {
		t.Errorf("SetModelID: err = %v, want ErrNoAgentName", err)
	}
	if err := SetMaxTurns(layout, "S1", "", 3); !errors.Is(err, ErrNoAgentName) {
		t.Errorf("SetMaxTurns: err = %v, want ErrNoAgentName", err)
	}
	if err := SetThinkingTokens(layout, "S1", "", "0"); !errors.Is(err, ErrNoAgentName) {
		t.Errorf("SetThinkingTokens: err = %v, want ErrNoAgentName", err)
	}

	// Nothing was appended: the real agent is still the only one.
	sess, err := Load(layout, "S1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Agents) != 1 {
		t.Fatalf("agents = %d, want 1 — a blank name must not create a row", len(sess.Agents))
	}
}
