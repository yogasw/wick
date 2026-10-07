package delegation

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// A leader with two trees: a mention of the handle that lives in the
// OLDER tree must route there, not to the newest tree RootForSession picks.
func TestRootForMentionFindsOlderTree(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	now := time.Now()
	rows := []entity.AgentDelegation{
		{ID: "a1", RootID: "rootA", ParentSessionID: "leader", ChildSessionID: "childA",
			ProfileKey: "researcher", Handle: "researcher", Status: entity.DelegationRunning, StartedAt: now.Add(-time.Hour)},
		{ID: "b1", RootID: "rootB", ParentSessionID: "leader", ChildSessionID: "childB",
			ProfileKey: "reviewer", Handle: "reviewer", Status: entity.DelegationRunning, StartedAt: now},
	}
	for i := range rows {
		if err := r.Create(ctx, &rows[i]); err != nil {
			t.Fatal(err)
		}
	}
	s := &Service{Repo: r}
	if got := s.rootForMention(ctx, "leader", "@researcher cek lagi"); got != "rootA" {
		t.Fatalf("mention of the older tree's handle routed to %q, want rootA", got)
	}
	if got := s.rootForMention(ctx, "leader", "@reviewer cek"); got != "rootB" {
		t.Fatalf("mention of reviewer routed to %q, want rootB", got)
	}
	// Without a mention it stays RootForSession: the newest tree.
	if got := s.rootForMention(ctx, "leader", "no mention here"); got != "rootB" {
		t.Fatalf("no mention: got %q, want the newest tree rootB", got)
	}
	if got := s.rootForMention(ctx, "childA", "@reviewer hi"); got != "rootA" {
		t.Fatalf("a sub-agent routes within its own tree, got %q", got)
	}
}

// The live row of a handle wins over a finished one in a newer tree.
func TestRootForMentionPrefersLiveRow(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	now := time.Now()
	rows := []entity.AgentDelegation{
		{ID: "a1", RootID: "rootA", ParentSessionID: "leader", ChildSessionID: "childA",
			ProfileKey: "researcher", Handle: "researcher", Status: entity.DelegationRunning, StartedAt: now.Add(-time.Hour)},
		{ID: "b1", RootID: "rootB", ParentSessionID: "leader", ChildSessionID: "childB",
			ProfileKey: "researcher", Handle: "researcher", Status: entity.DelegationDone, StartedAt: now},
	}
	for i := range rows {
		if err := r.Create(ctx, &rows[i]); err != nil {
			t.Fatal(err)
		}
	}
	s := &Service{Repo: r}
	if got := s.rootForMention(ctx, "leader", "@researcher lanjut"); got != "rootA" {
		t.Fatalf("got %q, want the live tree rootA", got)
	}
}
