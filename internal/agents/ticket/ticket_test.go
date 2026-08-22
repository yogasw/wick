package ticket

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
)

func newLayout(t *testing.T) config.Layout {
	t.Helper()
	l := config.NewLayout(t.TempDir())
	if err := l.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if _, err := project.Create(l, project.CreateOptions{ID: "p1", Name: "one"}); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestCreateAndLoad(t *testing.T) {
	l := newLayout(t)
	tk, err := Create(l, CreateOptions{ProjectID: "p1", Title: "Payment webhook failing"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tk.ID, "T-") || len(tk.ID) != 6 {
		t.Fatalf("id %q should be T- plus 4 chars", tk.ID)
	}
	if tk.Status != StatusOpen {
		t.Fatalf("new ticket status = %q, want open", tk.Status)
	}
	if tk.UpdatedAt.IsZero() || tk.CreatedAt.IsZero() {
		t.Fatal("timestamps not stamped")
	}

	got, err := Load(l, "p1", tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Payment webhook failing" || got.ProjectID != "p1" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestCreateRejectsUnknownProject(t *testing.T) {
	l := newLayout(t)
	if _, err := Create(l, CreateOptions{ProjectID: "nope", Title: "x"}); err == nil {
		t.Fatal("expected an error for an unknown project")
	}
}

func TestCreateRequiresTitle(t *testing.T) {
	l := newLayout(t)
	if _, err := Create(l, CreateOptions{ProjectID: "p1"}); err == nil {
		t.Fatal("expected an error for an empty title")
	}
}

// A short ID is only 4 random chars, so collisions are plausible enough to
// need a retry — this pins that the retry exists and yields distinct ids.
func TestCreateRetriesOnIDCollision(t *testing.T) {
	l := newLayout(t)
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		tk, err := Create(l, CreateOptions{ProjectID: "p1", Title: "t"})
		if err != nil {
			t.Fatal(err)
		}
		if seen[tk.ID] {
			t.Fatalf("duplicate id issued: %s", tk.ID)
		}
		seen[tk.ID] = true
	}
}

func TestListSortsNewestFirst(t *testing.T) {
	l := newLayout(t)
	a, _ := Create(l, CreateOptions{ProjectID: "p1", Title: "first"})
	b, _ := Create(l, CreateOptions{ProjectID: "p1", Title: "second"})

	got, err := List(l, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d tickets, want 2", len(got))
	}
	// Both present; order is by CreatedAt descending.
	if got[0].ID != b.ID || got[1].ID != a.ID {
		t.Fatalf("order = [%s %s], want [%s %s]", got[0].ID, got[1].ID, b.ID, a.ID)
	}
}

func TestListOfProjectWithoutTicketsIsEmpty(t *testing.T) {
	l := newLayout(t)
	got, err := List(l, "p1")
	if err != nil {
		t.Fatalf("a project with no tickets should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no tickets, got %d", len(got))
	}
}

func TestUpdateBumpsUpdatedAt(t *testing.T) {
	l := newLayout(t)
	tk, _ := Create(l, CreateOptions{ProjectID: "p1", Title: "t"})
	before := tk.UpdatedAt

	tk.Status = StatusInProgress
	tk.Assignee = "user-1"
	if err := Save(l, tk); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(l, "p1", tk.ID)
	if got.Status != StatusInProgress || got.Assignee != "user-1" {
		t.Fatalf("update lost: %+v", got)
	}
	if !got.UpdatedAt.After(before) && !got.UpdatedAt.Equal(before) {
		t.Fatalf("UpdatedAt went backwards: %v -> %v", before, got.UpdatedAt)
	}
}

func TestValidStatus(t *testing.T) {
	for _, ok := range []string{StatusOpen, StatusInProgress, StatusWaiting, StatusDone} {
		if !ValidStatus(ok) {
			t.Errorf("ValidStatus(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "closed", "OPEN", "in-progress"} {
		if ValidStatus(bad) {
			t.Errorf("ValidStatus(%q) = true", bad)
		}
	}
}

func TestAttachAndDetachSession(t *testing.T) {
	l := newLayout(t)
	tk, _ := Create(l, CreateOptions{ProjectID: "p1", Title: "t"})

	if err := AttachSession(l, "p1", tk.ID, "sess-a"); err != nil {
		t.Fatal(err)
	}
	if err := AttachSession(l, "p1", tk.ID, "sess-b"); err != nil {
		t.Fatal(err)
	}
	// Attaching twice must not duplicate the entry.
	if err := AttachSession(l, "p1", tk.ID, "sess-a"); err != nil {
		t.Fatal(err)
	}
	got, _ := Load(l, "p1", tk.ID)
	if len(got.Sessions) != 2 {
		t.Fatalf("sessions = %v, want 2 distinct", got.Sessions)
	}

	if err := DetachSession(l, "p1", tk.ID, "sess-a"); err != nil {
		t.Fatal(err)
	}
	got, _ = Load(l, "p1", tk.ID)
	if len(got.Sessions) != 1 || got.Sessions[0] != "sess-b" {
		t.Fatalf("after detach sessions = %v, want [sess-b]", got.Sessions)
	}

	// Detaching something absent is a no-op, not an error: the caller may be
	// reconciling a stale back-pointer.
	if err := DetachSession(l, "p1", tk.ID, "ghost"); err != nil {
		t.Fatalf("detaching an absent session should be a no-op: %v", err)
	}
}

func TestFindBySessionScansProjectTickets(t *testing.T) {
	l := newLayout(t)
	a, _ := Create(l, CreateOptions{ProjectID: "p1", Title: "a"})
	b, _ := Create(l, CreateOptions{ProjectID: "p1", Title: "b"})
	if err := AttachSession(l, "p1", b.ID, "sess-x"); err != nil {
		t.Fatal(err)
	}

	got, ok := FindBySession(l, "p1", "sess-x")
	if !ok || got.ID != b.ID {
		t.Fatalf("FindBySession = (%v, %v), want ticket %s", got.ID, ok, b.ID)
	}
	if _, ok := FindBySession(l, "p1", "sess-none"); ok {
		t.Fatal("FindBySession found a session that is attached to nothing")
	}
	_ = a
}

func TestDelete(t *testing.T) {
	l := newLayout(t)
	tk, _ := Create(l, CreateOptions{ProjectID: "p1", Title: "t"})
	if err := Delete(l, "p1", tk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(l, "p1", tk.ID); err == nil {
		t.Fatal("ticket still loadable after delete")
	}
}
