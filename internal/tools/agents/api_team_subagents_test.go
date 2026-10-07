package agents

import (
	"slices"
	"testing"
)

// The card reads the main session's live sub-agents from the one snapshot;
// another session's work never shows, and the item gets its own copy so a
// later edit to it cannot reach the shared snapshot.
func TestTeamLiveSubagentsOf(t *testing.T) {
	l := teamLive{subagents: map[string][]string{"main-1": {"wick-fixer", "reviewer-2"}}}
	got := l.subagentsOf("main-1")
	if !slices.Equal(got, []string{"wick-fixer", "reviewer-2"}) {
		t.Fatalf("subagentsOf = %v", got)
	}
	got[0] = "changed"
	if l.subagents["main-1"][0] != "wick-fixer" {
		t.Fatal("subagentsOf shares its slice with the snapshot")
	}
	if got := l.subagentsOf("main-2"); got != nil {
		t.Fatalf("other session = %v, want nil", got)
	}
	if got := (teamLive{}).subagentsOf("main-1"); got != nil {
		t.Fatalf("empty snapshot = %v, want nil", got)
	}
}
