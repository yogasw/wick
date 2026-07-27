package session

import (
	"path/filepath"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

func TestGoalLatch_OpenCompleteAbandon(t *testing.T) {
	layout := agentconfig.NewLayout(t.TempDir())
	id := "sess1"
	if HasOpenGoal(layout, id) {
		t.Fatal("expected no goal initially")
	}
	if err := OpenGoal(layout, id, "find 3 houses", "start"); err != nil {
		t.Fatal(err)
	}
	if !HasOpenGoal(layout, id) {
		t.Fatal("expected open after OpenGoal")
	}
	g, err := LoadGoal(layout, id)
	if err != nil || g == nil || g.Goal != "find 3 houses" || g.Status != GoalOpen {
		t.Fatalf("load: g=%+v err=%v", g, err)
	}
	// Dir form matches layout path.
	dir := layout.SessionDir(id)
	if !HasOpenGoalDir(dir) {
		t.Fatal("HasOpenGoalDir should see open goal")
	}
	if err := CompleteGoal(layout, id, "found them"); err != nil {
		t.Fatal(err)
	}
	if HasOpenGoal(layout, id) {
		t.Fatal("expected closed after CompleteGoal")
	}
	g, _ = LoadGoal(layout, id)
	if g.Status != GoalDone || g.Note != "found them" {
		t.Fatalf("done state: %+v", g)
	}
	// Re-open then abandon.
	_ = OpenGoal(layout, id, "other", "")
	_ = AbandonGoal(layout, id, "gave up")
	g, _ = LoadGoal(layout, id)
	if g.Status != GoalAbandoned {
		t.Fatalf("abandon state: %+v", g)
	}
	// File lives under session dir.
	if _, err := filepath.Glob(filepath.Join(dir, "goal.json")); err != nil {
		t.Fatal(err)
	}
}
