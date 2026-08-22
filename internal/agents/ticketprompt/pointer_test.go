package ticketprompt

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/notes"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/ticket"
)

func setup(t *testing.T) config.Layout {
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

func mkSession(t *testing.T, l config.Layout, id, projectID string) {
	t.Helper()
	if _, err := session.Create(context.Background(), l, session.CreateOptions{ID: id, ProjectID: projectID}); err != nil {
		t.Fatal(err)
	}
}

// Nothing to point at: a loose session with no notes must add nothing to
// the prompt at all.
func TestPointerEmptyForLooseSessionWithoutNotes(t *testing.T) {
	l := setup(t)
	mkSession(t, l, "s1", "p1")
	if got := Pointer(l, "s1"); got != "" {
		t.Fatalf("want empty pointer, got:\n%s", got)
	}
}

func TestPointerEmptyForUnknownSession(t *testing.T) {
	l := setup(t)
	if got := Pointer(l, "ghost"); got != "" {
		t.Fatalf("want empty pointer for an unknown session, got:\n%s", got)
	}
}

func TestPointerNamesTicketAndCountsNotes(t *testing.T) {
	l := setup(t)
	mkSession(t, l, "s1", "p1")
	tk, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "Payment webhook failing"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ticket.AttachSession(l, "p1", tk.ID, "s1"); err != nil {
		t.Fatal(err)
	}
	sc := notes.Scope{ProjectID: "p1", TicketID: tk.ID}
	if _, err := notes.Add(l, sc, notes.AddOptions{Body: "root cause is the retry loop"}); err != nil {
		t.Fatal(err)
	}
	if _, err := notes.Add(l, sc, notes.AddOptions{Body: "verify on staging", Checkable: true}); err != nil {
		t.Fatal(err)
	}

	got := Pointer(l, "s1")
	for _, want := range []string{tk.ID, "Payment webhook failing", "2 note(s)", "1 of them unchecked", "notes_list"} {
		if !strings.Contains(got, want) {
			t.Errorf("pointer missing %q in:\n%s", want, got)
		}
	}
	// The whole point: bodies stay out of the prompt.
	if strings.Contains(got, "root cause is the retry loop") {
		t.Fatalf("pointer leaked a note body into the prompt:\n%s", got)
	}
}

// The pointer must stay a fixed size as notes pile up — that is why it is a
// pointer and not the notes themselves.
func TestPointerLengthDoesNotGrowWithNotes(t *testing.T) {
	l := setup(t)
	mkSession(t, l, "s1", "p1")
	tk, _ := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	if err := ticket.AttachSession(l, "p1", tk.ID, "s1"); err != nil {
		t.Fatal(err)
	}
	sc := notes.Scope{ProjectID: "p1", TicketID: tk.ID}
	if _, err := notes.Add(l, sc, notes.AddOptions{Body: "first"}); err != nil {
		t.Fatal(err)
	}
	short := len(Pointer(l, "s1"))

	for i := 0; i < 30; i++ {
		if _, err := notes.Add(l, sc, notes.AddOptions{Body: strings.Repeat("a lot of detail ", 40)}); err != nil {
			t.Fatal(err)
		}
	}
	long := len(Pointer(l, "s1"))
	// Only the count digits change, so a few characters at most.
	if long-short > 8 {
		t.Fatalf("pointer grew %d chars with 30 more notes; it must stay fixed-size", long-short)
	}
}

func TestPointerCountsSessionsWhenTicketHasSeveral(t *testing.T) {
	l := setup(t)
	mkSession(t, l, "s1", "p1")
	mkSession(t, l, "s2", "p1")
	tk, _ := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	for _, s := range []string{"s1", "s2"} {
		if err := ticket.AttachSession(l, "p1", tk.ID, s); err != nil {
			t.Fatal(err)
		}
	}
	got := Pointer(l, "s1")
	if !strings.Contains(got, "2 sessions") {
		t.Fatalf("pointer should mention the other sessions on the ticket:\n%s", got)
	}
}

// A loose session that has its own notes still gets a pointer — notes work
// without tickets.
func TestPointerForLooseSessionWithNotes(t *testing.T) {
	l := setup(t)
	mkSession(t, l, "s1", "p1")
	if _, err := notes.Add(l, notes.Scope{SessionID: "s1"}, notes.AddOptions{Body: "remember this"}); err != nil {
		t.Fatal(err)
	}
	got := Pointer(l, "s1")
	if !strings.Contains(got, "1 note(s)") || !strings.Contains(got, "notes_list") {
		t.Fatalf("loose session with notes should get a pointer:\n%s", got)
	}
	if strings.Contains(got, "ticket T-") {
		t.Fatalf("pointer named a ticket for a loose session:\n%s", got)
	}
}

// Hidden notes are invisible to the agent, so the pointer must not count
// them — advertising a note it cannot fetch would be worse than silence.
func TestPointerIgnoresHiddenNotes(t *testing.T) {
	l := setup(t)
	mkSession(t, l, "s1", "p1")
	sc := notes.Scope{SessionID: "s1"}
	n, err := notes.Add(l, sc, notes.AddOptions{Body: "private"})
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	if _, err := notes.Update(l, sc, n.ID, notes.UpdateOptions{Hidden: &yes}); err != nil {
		t.Fatal(err)
	}
	if got := Pointer(l, "s1"); got != "" {
		t.Fatalf("hidden-only scope should produce no pointer, got:\n%s", got)
	}
}
