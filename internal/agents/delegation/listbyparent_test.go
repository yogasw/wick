package delegation

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// The rail is read top-down while work is in flight, so the delegation
// you just watched the leader make has to be the first row — with
// oldest-first it sinks further down with every new sub-agent.
func TestListByParentReturnsNewestFirst(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)

	for i, id := range []string{"oldest", "middle", "newest"} {
		d := &entity.AgentDelegation{
			ID:              id,
			RootID:          "root",
			ParentSessionID: "parent",
			ChildSessionID:  "parent--sub-" + id,
			ProfileKey:      "researcher",
			Status:          entity.DelegationDone,
			StartedAt:       base.Add(time.Duration(i) * time.Minute),
		}
		if err := r.Create(ctx, d); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	rows, err := r.ListByParent(ctx, "parent")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := make([]string, len(rows))
	for i, d := range rows {
		got[i] = d.ID
	}
	want := []string{"newest", "middle", "oldest"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// A sibling conversation's delegations must not bleed into this one.
func TestListByParentScopesToOneParent(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()

	for _, tc := range []struct{ id, parent string }{
		{"a", "parent"},
		{"b", "other-parent"},
	} {
		d := &entity.AgentDelegation{
			ID:              tc.id,
			RootID:          "root",
			ParentSessionID: tc.parent,
			ProfileKey:      "researcher",
			Status:          entity.DelegationDone,
			StartedAt:       time.Now().UTC(),
		}
		if err := r.Create(ctx, d); err != nil {
			t.Fatalf("create %s: %v", tc.id, err)
		}
	}

	rows, err := r.ListByParent(ctx, "parent")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "a" {
		t.Fatalf("rows = %+v, want only delegation a", rows)
	}
}
