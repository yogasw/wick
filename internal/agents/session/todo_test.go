package session

import (
	"testing"
	"time"

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

// An agent that rethinks its plan renames every step, so the new list
// shares nothing with the one it replaces. Read as a new list, that filled
// the history with half-done near-duplicates nobody wrote on purpose — and
// left the panel disagreeing with the card in the transcript.
func TestRecordTodosRewriteIsTheSameList(t *testing.T) {
	l := todoLayout(t)
	if _, err := RecordTodos(l, "s1", items(
		"Hide tickets from projects where tickets are not enabled", "in_progress",
		"Fix the connector layout", "pending",
	)); err != nil {
		t.Fatal(err)
	}
	rec, err := RecordTodos(l, "s1", items(
		"Hide ticket tab when the project has tickets disabled", "completed",
		"Fix the connector long-description layout", "in_progress",
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.History) != 0 {
		t.Fatalf("a rewrite archived the plan it replaced: %d in history", len(rec.History))
	}
	if len(rec.Active.Items) != 2 || rec.Active.Items[0].Label() != "Hide ticket tab when the project has tickets disabled" {
		t.Fatalf("the rewritten list did not become the active one: %+v", rec.Active.Items)
	}
}

// A list that has gone cold is over. What arrives after it is new work, and
// the old plan belongs in the history rather than being silently dropped.
func TestSameTodoListColdActiveIsANewList(t *testing.T) {
	now := time.Now().UTC()
	warm := TodoList{Items: items("a", "in_progress"), UpdatedAt: now.Add(-time.Minute)}
	cold := TodoList{Items: items("a", "in_progress"), UpdatedAt: now.Add(-2 * todoReviseWindow)}
	fresh := items("something else entirely", "pending")

	if !sameTodoList(warm, fresh, now) {
		t.Fatal("a warm unfinished list should be revised, not archived")
	}
	if sameTodoList(cold, fresh, now) {
		t.Fatal("a cold list should be archived, not revised")
	}
}

// Stopped means abandoned, and done means finished. Neither is continued by
// whatever comes next.
func TestSameTodoListNeverContinuesAClosedList(t *testing.T) {
	now := time.Now().UTC()
	fresh := items("something else", "pending")
	stopped := TodoList{Items: items("a", "in_progress"), UpdatedAt: now, Stopped: true}
	done := TodoList{Items: items("a", "completed"), UpdatedAt: now, Done: true}
	if sameTodoList(stopped, fresh, now) {
		t.Fatal("an abandoned list was continued")
	}
	if sameTodoList(done, fresh, now) {
		t.Fatal("a finished list was continued")
	}
	// …but a call that names one of its own items still belongs to it: a
	// finished list being re-opened is that list, not a new one.
	if !sameTodoList(done, items("a", "in_progress"), now) {
		t.Fatal("a call naming the list's own item started a new list")
	}
}
