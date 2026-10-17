package agents

import (
	"slices"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/session"
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

// A row whose sub-agents run from a side chat opens that chat: the most
// recently active of several, and none when the main chat has its own.
func TestTeamLiveSubagentsSession(t *testing.T) {
	at := func(id string, min int) session.Session {
		return session.Session{ID: id, Meta: session.Meta{LastActive: time.Date(2026, 10, 17, 10, min, 0, 0, time.UTC)}}
	}
	sessions := []session.Session{at("main", 30), at("side-old", 5), at("side-new", 20), at("idle", 40)}
	l := teamLive{subagents: map[string][]string{"side-old": {"a"}, "side-new": {"b"}}}
	if got, at := l.subagentsSession(sessions, "main"); got != "side-new" || at == nil || at.Minute() != 20 {
		t.Fatalf("side chats = %q %v, want side-new at :20", got, at)
	}
	l.subagents["main"] = []string{"c"}
	if got, _ := l.subagentsSession(sessions, "main"); got != "" {
		t.Fatalf("main has sub-agents = %q, want empty", got)
	}
	if got, _ := (teamLive{}).subagentsSession(sessions, "main"); got != "" {
		t.Fatalf("none working = %q, want empty", got)
	}
}
