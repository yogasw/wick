package notes

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/ticket"
)

// moveLayout wires the real mover, so these tests exercise the same path
// the server does rather than calling MoveForSession directly.
func moveLayout(t *testing.T) config.Layout {
	t.Helper()
	l := resolveLayout(t)
	ticket.SetNotesMover(MoveForSession)
	t.Cleanup(func() { ticket.SetNotesMover(nil) })
	return l
}

func bodies(t *testing.T, l config.Layout, sc Scope) []string {
	t.Helper()
	list, err := List(l, sc)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(list))
	for _, n := range list {
		out = append(out, n.Body)
	}
	return out
}

// Attaching a loose session must carry its notes onto the ticket, or
// everything written before the attach silently stops being visible.
func TestAttachCarriesSessionNotesOntoTheTicket(t *testing.T) {
	l := moveLayout(t)
	mkSession(t, l, "s1", "p1")
	if _, err := Add(l, Scope{SessionID: "s1"}, AddOptions{Body: "found it in the queue config", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	tk, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	if err != nil {
		t.Fatal(err)
	}

	if err := ticket.AttachSession(l, "p1", tk.ID, "s1"); err != nil {
		t.Fatal(err)
	}

	onTicket := bodies(t, l, Scope{ProjectID: "p1", TicketID: tk.ID})
	if len(onTicket) != 1 || onTicket[0] != "found it in the queue config" {
		t.Fatalf("ticket notes = %v, want the session's note", onTicket)
	}
	if left := bodies(t, l, Scope{SessionID: "s1"}); len(left) != 0 {
		t.Fatalf("notes were copied, not moved: %v", left)
	}
	// The scope a session resolves to is the ticket's, so the same note is
	// what the session now reads.
	sc, err := Resolve(l, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got := bodies(t, l, sc); len(got) != 1 {
		t.Fatalf("resolved scope sees %v", got)
	}
}

// Dragging a session from one card to another is a MOVE: the session ends
// up on exactly one ticket, with its notes.
func TestAttachToAnotherTicketMovesSessionAndNotes(t *testing.T) {
	l := moveLayout(t)
	mkSession(t, l, "s1", "p1")
	a, _ := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "first"})
	b, _ := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "second"})
	if err := ticket.AttachSession(l, "p1", a.ID, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(l, Scope{ProjectID: "p1", TicketID: a.ID}, AddOptions{Body: "learned on A", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}

	if err := ticket.AttachSession(l, "p1", b.ID, "s1"); err != nil {
		t.Fatal(err)
	}

	loadedA, _ := ticket.Load(l, "p1", a.ID)
	loadedB, _ := ticket.Load(l, "p1", b.ID)
	if len(loadedA.Sessions) != 0 {
		t.Fatalf("session still on the old ticket: %v", loadedA.Sessions)
	}
	if len(loadedB.Sessions) != 1 || loadedB.Sessions[0] != "s1" {
		t.Fatalf("session not on the new ticket: %v", loadedB.Sessions)
	}
	if got := bodies(t, l, Scope{ProjectID: "p1", TicketID: b.ID}); len(got) != 1 || got[0] != "learned on A" {
		t.Fatalf("notes did not follow the session: %v", got)
	}
	if got := bodies(t, l, Scope{ProjectID: "p1", TicketID: a.ID}); len(got) != 0 {
		t.Fatalf("old ticket kept the moved note: %v", got)
	}
}

// Detaching takes back only what THIS session wrote. Notes another session
// left on the ticket describe the ticket's work and stay with it.
func TestDetachTakesBackOnlyThisSessionsNotes(t *testing.T) {
	l := moveLayout(t)
	mkSession(t, l, "s1", "p1")
	mkSession(t, l, "s2", "p1")
	tk, _ := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	for _, s := range []string{"s1", "s2"} {
		if err := ticket.AttachSession(l, "p1", tk.ID, s); err != nil {
			t.Fatal(err)
		}
	}
	sc := Scope{ProjectID: "p1", TicketID: tk.ID}
	if _, err := Add(l, sc, AddOptions{Body: "from s1", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(l, sc, AddOptions{Body: "from s2", SessionID: "s2"}); err != nil {
		t.Fatal(err)
	}
	// A note written on the ticket itself (no session): belongs to the
	// ticket, not to whoever happens to detach.
	if _, err := Add(l, sc, AddOptions{Body: "written on the ticket"}); err != nil {
		t.Fatal(err)
	}

	if err := ticket.DetachSession(l, "p1", tk.ID, "s1"); err != nil {
		t.Fatal(err)
	}

	onTicket := bodies(t, l, sc)
	if len(onTicket) != 2 {
		t.Fatalf("ticket should keep s2's and its own note, got %v", onTicket)
	}
	for _, b := range onTicket {
		if b == "from s1" {
			t.Fatalf("s1's note stayed behind: %v", onTicket)
		}
	}
	back := bodies(t, l, Scope{SessionID: "s1"})
	if len(back) != 1 || back[0] != "from s1" {
		t.Fatalf("detached session did not get its note back: %v", back)
	}
}

// A moved note keeps its identity and gains provenance, so a reader can
// tell it arrived from somewhere else.
func TestMovedNoteKeepsIDAndGainsProvenance(t *testing.T) {
	l := moveLayout(t)
	mkSession(t, l, "s1", "p1")
	n, err := Add(l, Scope{SessionID: "s1"}, AddOptions{Body: "x", SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	tk, _ := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	if err := ticket.AttachSession(l, "p1", tk.ID, "s1"); err != nil {
		t.Fatal(err)
	}

	list, err := List(l, Scope{ProjectID: "p1", TicketID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want one note, got %d", len(list))
	}
	got := list[0]
	if got.ID != n.ID {
		t.Fatalf("id changed on move: %s -> %s", n.ID, got.ID)
	}
	if !got.CreatedAt.Equal(n.CreatedAt) {
		t.Fatal("created_at changed on move")
	}
	if got.MovedAt.IsZero() {
		t.Fatal("moved_at not stamped")
	}
	if got.SourceSessionID != "s1" {
		t.Fatalf("source session = %q, want s1", got.SourceSessionID)
	}
}

// Attaching a session that has no notes must not error or invent one.
func TestAttachWithNoNotesIsQuiet(t *testing.T) {
	l := moveLayout(t)
	mkSession(t, l, "s1", "p1")
	tk, _ := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	if err := ticket.AttachSession(l, "p1", tk.ID, "s1"); err != nil {
		t.Fatal(err)
	}
	if got := bodies(t, l, Scope{ProjectID: "p1", TicketID: tk.ID}); len(got) != 0 {
		t.Fatalf("want no notes, got %v", got)
	}
}

// Re-attaching to the same ticket is a no-op and must not shuffle notes.
func TestReAttachSameTicketDoesNotTouchNotes(t *testing.T) {
	l := moveLayout(t)
	mkSession(t, l, "s1", "p1")
	tk, _ := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	if err := ticket.AttachSession(l, "p1", tk.ID, "s1"); err != nil {
		t.Fatal(err)
	}
	sc := Scope{ProjectID: "p1", TicketID: tk.ID}
	if _, err := Add(l, sc, AddOptions{Body: "keep me", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if err := ticket.AttachSession(l, "p1", tk.ID, "s1"); err != nil {
		t.Fatal(err)
	}
	if got := bodies(t, l, sc); len(got) != 1 || got[0] != "keep me" {
		t.Fatalf("notes disturbed by a redundant attach: %v", got)
	}
}

// With no mover wired (a plain unit-test setup, or a build that never calls
// SetNotesMover) attach still works — notes simply stay put.
func TestAttachWithoutMoverStillWorks(t *testing.T) {
	l := resolveLayout(t)
	ticket.SetNotesMover(nil)
	mkSession(t, l, "s1", "p1")
	if _, err := Add(l, Scope{SessionID: "s1"}, AddOptions{Body: "stays", SessionID: "s1"}); err != nil {
		t.Fatal(err)
	}
	tk, _ := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	if err := ticket.AttachSession(l, "p1", tk.ID, "s1"); err != nil {
		t.Fatal(err)
	}
	if got := bodies(t, l, Scope{SessionID: "s1"}); len(got) != 1 {
		t.Fatalf("notes should be untouched without a mover: %v", got)
	}
}
