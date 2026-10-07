package agents

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/tools/agents/view"
)

// The provider list carries an omp/opencode instance's known live list in
// the grouped shape the drill-in returns, so the picker does not open on a
// flat list and swap it a moment later; a cold instance carries none.
func TestLiveModelChoicesMatchDrillIn(t *testing.T) {
	t.Cleanup(provider.SetModelStateDirForTest(t.TempDir()))
	mk := func(name string) provider.Instance {
		return provider.Instance{Type: provider.TypeOpencode, Name: name, LiveModels: true, ModelSelect: true,
			OpencodeConfig: &provider.OpencodeConfig{DataDir: t.TempDir(), AllowHosted: true}}
	}
	known := mk("oc-known")
	t.Cleanup(provider.SetCLIModelsForTest(known, "opencode/mimo-free", "opencode/nemotron-free", "openrouter/qwen"))

	// The first list never waits on the build (it can run `omp usage`);
	// it starts it, and a later list carries the rows.
	if rows, _, _ := liveModelChoices(context.Background(), known); rows != nil {
		t.Fatalf("first list waited on the build: %+v", rows)
	}
	var rows []view.ModelChoiceVM
	var at time.Time
	deadline := time.Now().Add(5 * time.Second)
	for rows == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		rows, at, _ = liveModelChoices(context.Background(), known)
	}
	if at.IsZero() || len(rows) != 2 {
		t.Fatalf("known list: rows=%+v at=%v, want the 2 provider groups with a stamp", rows, at)
	}
	sets, _ := provider.ModelSetsFor(known.Type)
	want, err := sets.Sets(context.Background(), known)
	if err != nil {
		t.Fatal(err)
	}
	for i := range want {
		if rows[i].ID != want[i].ID || rows[i].Label != want[i].Label || rows[i].Live != want[i].Live || rows[i].Default != want[i].Default {
			t.Fatalf("row %d = %+v, drill-in has %+v", i, rows[i], want[i])
		}
	}

	// A Refresh replaced the list: the old rows are not served under the
	// new stamp; the next build carries the new list's groups.
	restore := provider.SetCLIModelsForTest(known, "opencode/mimo-free", "openrouter/qwen", "anthropic/claude-x")
	t.Cleanup(restore)
	if rows, at2, _ := liveModelChoices(context.Background(), known); rows != nil {
		t.Fatalf("old rows served after the list changed (stamp %v): %+v", at2, rows)
	}
	deadline = time.Now().Add(5 * time.Second)
	for rows = nil; rows == nil && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
		rows, _, _ = liveModelChoices(context.Background(), known)
	}
	if len(rows) != 3 {
		t.Fatalf("rebuilt rows = %+v, want the 3 groups of the new list", rows)
	}

	if rows, _, _ := liveModelChoices(context.Background(), mk("oc-cold")); rows != nil {
		t.Fatalf("cold instance: %+v, want nil (listing never waits on a harvest)", rows)
	}
}
