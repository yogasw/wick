package session

import (
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

func todoLayout(t *testing.T) agentconfig.Layout {
	t.Helper()
	return agentconfig.Layout{BaseDir: t.TempDir()}
}

func items(pairs ...string) []TodoItem {
	out := make([]TodoItem, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, TodoItem{Title: pairs[i], Status: pairs[i+1]})
	}
	return out
}

// TestRecordTodos locks the one judgement this store makes: whether a call
// continues the active checklist or starts a new one. Get it wrong in one
// direction and every checkpoint spawns a new list; in the other, a genuinely
// new list silently overwrites the one before it.
func TestRecordTodos(t *testing.T) {
	t.Run("a checkpoint updates the same list", func(t *testing.T) {
		l := todoLayout(t)
		if _, err := RecordTodos(l, "s1", items("read the logs", "in_progress", "write the fix", "pending")); err != nil {
			t.Fatal(err)
		}
		rec, err := RecordTodos(l, "s1", items("read the logs", "completed", "write the fix", "in_progress"))
		if err != nil {
			t.Fatal(err)
		}
		if len(rec.History) != 0 {
			t.Fatalf("a checkpoint archived the list: %d in history", len(rec.History))
		}
		if rec.Active.Items[0].Status != "completed" {
			t.Fatal("the update did not land on the active list")
		}
	})

	t.Run("a list sharing nothing starts a new one and keeps the old", func(t *testing.T) {
		l := todoLayout(t)
		if _, err := RecordTodos(l, "s1", items("read the logs", "completed")); err != nil {
			t.Fatal(err)
		}
		rec, err := RecordTodos(l, "s1", items("ship the release", "pending"))
		if err != nil {
			t.Fatal(err)
		}
		if len(rec.History) != 1 || rec.History[0].Items[0].Label() != "read the logs" {
			t.Fatalf("the finished list was lost: %+v", rec.History)
		}
		if rec.Active.Items[0].Label() != "ship the release" {
			t.Fatal("the new list did not become active")
		}
	})

	t.Run("the start time survives a checkpoint", func(t *testing.T) {
		// "Started 40 minutes ago" is the useful number; resetting it on
		// every update would make every list look brand new.
		l := todoLayout(t)
		first, _ := RecordTodos(l, "s1", items("a", "pending"))
		started := first.Active.StartedAt
		again, _ := RecordTodos(l, "s1", items("a", "completed"))
		if !again.Active.StartedAt.Equal(started) {
			t.Fatalf("start time moved: %v -> %v", started, again.Active.StartedAt)
		}
	})

	t.Run("done is recorded, not left to the reader", func(t *testing.T) {
		l := todoLayout(t)
		rec, _ := RecordTodos(l, "s1", items("a", "completed", "b", "completed"))
		if !rec.Active.Done {
			t.Fatal("a fully completed list was not marked done")
		}
	})

	t.Run("labels match case-insensitively", func(t *testing.T) {
		// A model re-typing its own list is the common case, and it does not
		// retype it byte for byte.
		l := todoLayout(t)
		RecordTodos(l, "s1", items("Read The Logs", "pending"))
		rec, _ := RecordTodos(l, "s1", items("read the logs ", "completed"))
		if len(rec.History) != 0 {
			t.Fatal("a retyped list was treated as a different one")
		}
	})

	t.Run("a session with no todos reads empty, not an error", func(t *testing.T) {
		rec, err := LoadTodos(todoLayout(t), "never-used")
		if err != nil || rec.Active != nil || len(rec.History) != 0 {
			t.Fatalf("empty session: %+v, %v", rec, err)
		}
	})
}
