package session

import (
	"strings"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

func patchLayout(t *testing.T) agentconfig.Layout {
	t.Helper()
	return agentconfig.NewLayout(t.TempDir())
}

// A build script knows "stage 2 is running" and nothing about the other
// five. Making it restate the whole checklist would mean inventing the rest,
// so a patch names one item and leaves the others alone.
func TestPatchTodoItemTouchesOnlyTheItemItNames(t *testing.T) {
	l := patchLayout(t)
	if _, err := RecordTodos(l, "s1", []TodoItem{
		{ID: "unit", Title: "unit", Status: "completed"},
		{ID: "race", Title: "race", Status: "pending"},
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := PatchTodoItem(l, "s1", TodoPatch{
		Select:   "race",
		Status:   "in_progress",
		Progress: &TodoProgress{Done: 3, Total: 9, Label: "packages"},
		Detail:   &TodoDetail{Format: "text", Body: "ok internal/admin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Active.Items) != 2 {
		t.Fatalf("items = %d, want the list it patched, not a new one", len(rec.Active.Items))
	}
	if rec.Active.Items[0].Status != "completed" {
		t.Errorf("the untouched item changed to %q", rec.Active.Items[0].Status)
	}
	got := rec.Active.Items[1]
	if got.Status != "in_progress" || got.Progress == nil || got.Progress.Done != 3 || got.Progress.Total != 9 {
		t.Errorf("patched item = %+v, want in_progress 3/9", got)
	}
	if got.Detail == nil || got.Detail.Body != "ok internal/admin" {
		t.Errorf("detail = %+v, want the body it was given", got.Detail)
	}
}

// First report of a run: there is no list yet, and a script should not have
// to create one in a separate call before it can say anything.
func TestPatchTodoItemCreatesTheListAndTheItem(t *testing.T) {
	l := patchLayout(t)
	rec, err := PatchTodoItem(l, "s1", TodoPatch{Select: "build", Status: "in_progress"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Active == nil || len(rec.Active.Items) != 1 || rec.Active.Items[0].Label() != "build" {
		t.Fatalf("record = %+v, want one item called build", rec.Active)
	}
}

// A log is not a checklist entry, and todos.json is read in full every time
// the panel opens. The tail is what is kept: the end of a log says what
// happened.
func TestPatchTodoItemTruncatesAHugeDetail(t *testing.T) {
	l := patchLayout(t)
	body := strings.Repeat("x", maxDetailRunes+500) + "THE-END"
	rec, err := PatchTodoItem(l, "s1", TodoPatch{Select: "gate", Detail: &TodoDetail{Body: body}})
	if err != nil {
		t.Fatal(err)
	}
	got := rec.Active.Items[0].Detail.Body
	if len([]rune(got)) > maxDetailRunes+80 {
		t.Errorf("detail kept %d runes, want it clamped", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "THE-END") {
		t.Error("truncation kept the head; the end of a log is the part that matters")
	}
}

// "Nobody is working on this" and "this finished" look identical in a
// half-ticked list, and only one of them means the work happened.
func TestStopTodosMarksTheListAndItsRunningItems(t *testing.T) {
	l := patchLayout(t)
	if _, err := RecordTodos(l, "s1", []TodoItem{
		{ID: "a", Title: "a", Status: "completed"},
		{ID: "b", Title: "b", Status: "in_progress"},
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := StopTodos(l, "s1", "the box ran out of memory")
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Active.Stopped || rec.Active.StoppedAt.IsZero() {
		t.Error("the list does not say it was stopped")
	}
	if rec.Active.Done {
		t.Error("a stopped list must not read as done")
	}
	if rec.Active.Items[1].Status != "stopped" {
		t.Errorf("running item = %q, want it stopped too — that is the stuck card", rec.Active.Items[1].Status)
	}
	if rec.Active.Note != "the box ran out of memory" {
		t.Errorf("note = %q, want the reason it was given", rec.Active.Note)
	}
}

// The next run starts its own list rather than merging into the one that
// was abandoned, even when it repeats the same step names.
func TestRecordAfterStopStartsAFreshList(t *testing.T) {
	l := patchLayout(t)
	if _, err := RecordTodos(l, "s1", []TodoItem{{ID: "gate", Title: "gate", Status: "in_progress"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := StopTodos(l, "s1", ""); err != nil {
		t.Fatal(err)
	}
	rec, err := RecordTodos(l, "s1", []TodoItem{{ID: "gate", Title: "gate", Status: "in_progress"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.History) != 1 || !rec.History[0].Stopped {
		t.Fatalf("history = %+v, want the stopped list moved into it", rec.History)
	}
	if rec.Active.Stopped {
		t.Error("the new list inherited the stopped flag")
	}
}

func TestClearTodos(t *testing.T) {
	l := patchLayout(t)
	if _, err := RecordTodos(l, "s1", []TodoItem{{ID: "a", Title: "a", Status: "completed"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordTodos(l, "s1", []TodoItem{{ID: "b", Title: "b", Status: "pending"}}); err != nil {
		t.Fatal(err)
	}
	rec, err := ClearTodos(l, "s1", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.History) != 0 {
		t.Errorf("history = %d lists, want it emptied", len(rec.History))
	}
	if rec.Active == nil {
		t.Error("clearing the history took the live list with it")
	}
	if rec, err = ClearTodos(l, "s1", true); err != nil {
		t.Fatal(err)
	}
	if rec.Active != nil {
		t.Error("clear-all left the active list behind")
	}
}
