package shardedlog

import "testing"

type removeRow struct {
	ID string `json:"id"`
}

func TestStore_Remove(t *testing.T) {
	s := &Store[removeRow]{Dir: t.TempDir(), ShardMax: 2}
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := s.Append(removeRow{ID: id}); err != nil {
			t.Fatalf("append %s: %v", id, err)
		}
	}

	n, err := s.Remove(func(r removeRow) bool { return r.ID == "b" || r.ID == "d" })
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if n != 2 {
		t.Fatalf("removed = %d, want 2", n)
	}

	got, _, err := s.Page(1, 100)
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	ids := map[string]bool{}
	for _, r := range got {
		ids[r.ID] = true
	}
	if ids["b"] || ids["d"] {
		t.Fatalf("removed entries still present: %v", got)
	}
	if !ids["a"] || !ids["c"] || len(got) != 2 {
		t.Fatalf("kept entries wrong: %v", got)
	}
}
