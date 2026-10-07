package todoprompt

import (
	"strings"
	"testing"

	config "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

func layoutWith(t *testing.T, id string, items []session.TodoItem) config.Layout {
	t.Helper()
	l := config.NewLayout(t.TempDir())
	if err := l.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if len(items) > 0 {
		if _, err := session.RecordTodos(l, id, items); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

// TestPointer locks WHEN the list is put in front of the agent — the part
// that decides whether a spawn picks up its own unfinished work or forgets it.
func TestPointer(t *testing.T) {
	t.Run("an unfinished list is carried into the prompt", func(t *testing.T) {
		l := layoutWith(t, "s1", []session.TodoItem{
			{Title: "read the logs", Status: "completed"},
			{Title: "write the fix", Status: "in_progress"},
			{Title: "ship it", Status: "pending"},
		})
		got := Pointer(l, "s1")
		if !strings.Contains(got, "write the fix") || !strings.Contains(got, "ship it") {
			t.Fatalf("the outstanding items are missing:\n%s", got)
		}
		if !strings.Contains(got, "1 of 3") {
			t.Fatalf("progress not stated:\n%s", got)
		}
		// The instruction that keeps a checkpoint from being read as a new
		// list is the whole reason this block is worth its tokens.
		if !strings.Contains(got, "SAME titles") {
			t.Fatalf("no instruction to continue the same list:\n%s", got)
		}
	})

	t.Run("a finished list says nothing", func(t *testing.T) {
		// A completed checklist is history. Repeating it every spawn spends
		// context on work already done.
		l := layoutWith(t, "s1", []session.TodoItem{{Title: "done thing", Status: "completed"}})
		if got := Pointer(l, "s1"); got != "" {
			t.Fatalf("finished list was injected:\n%s", got)
		}
	})

	t.Run("a session with no list says nothing", func(t *testing.T) {
		l := layoutWith(t, "s1", nil)
		if got := Pointer(l, "s1"); got != "" {
			t.Fatalf("empty session produced a block:\n%s", got)
		}
	})

	t.Run("substeps come along", func(t *testing.T) {
		l := layoutWith(t, "s1", []session.TodoItem{{
			Title:  "write the fix",
			Status: "in_progress",
			Substeps: []session.TodoSubstep{
				{Step: "add the test", Status: "completed"},
				{Step: "make it pass", Status: "pending"},
			},
		}})
		got := Pointer(l, "s1")
		if !strings.Contains(got, "make it pass") {
			t.Fatalf("substeps dropped:\n%s", got)
		}
	})

	t.Run("a very long list is truncated, and says so", func(t *testing.T) {
		// Past a dozen lines a checklist stops being a reminder and starts
		// being a second prompt.
		items := make([]session.TodoItem, 0, 20)
		for i := 0; i < 20; i++ {
			items = append(items, session.TodoItem{Title: "step " + string(rune('a'+i)), Status: "pending"})
		}
		got := Pointer(layoutWith(t, "s1", items), "s1")
		if !strings.Contains(got, "and 8 more") {
			t.Fatalf("no truncation notice:\n%s", got)
		}
	})

	t.Run("no session id, no block", func(t *testing.T) {
		if got := Pointer(layoutWith(t, "s1", nil), ""); got != "" {
			t.Fatal("a blank session id produced a block")
		}
	})
}
