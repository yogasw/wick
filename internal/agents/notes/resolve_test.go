package notes

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/ticket"
)

// resolveLayout builds a layout with one project so tickets can be created.
func resolveLayout(t *testing.T) config.Layout {
	t.Helper()
	l := newLayout(t)
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

// The point of the whole design: a session inside a ticket reads and writes
// the TICKET's notes, so opening a fresh session keeps the context.
func TestResolveSessionInTicketUsesTicketScope(t *testing.T) {
	l := resolveLayout(t)
	mkSession(t, l, "sess-1", "p1")
	tk, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ticket.AttachSession(l, "p1", tk.ID, "sess-1"); err != nil {
		t.Fatal(err)
	}

	sc, err := Resolve(l, "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if sc.TicketID != tk.ID || sc.ProjectID != "p1" || sc.SessionID != "" {
		t.Fatalf("scope = %+v, want the ticket scope for %s", sc, tk.ID)
	}
}

// Two sessions on one ticket must land on the same notes — that is what
// makes a fresh session pick up where the previous one left off.
func TestResolveTwoSessionsOnOneTicketShareNotes(t *testing.T) {
	l := resolveLayout(t)
	mkSession(t, l, "sess-a", "p1")
	mkSession(t, l, "sess-b", "p1")
	tk, _ := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	for _, s := range []string{"sess-a", "sess-b"} {
		if err := ticket.AttachSession(l, "p1", tk.ID, s); err != nil {
			t.Fatal(err)
		}
	}

	scA, _ := Resolve(l, "sess-a")
	if _, err := Add(l, scA, AddOptions{Body: "found the cause in the retry loop"}); err != nil {
		t.Fatal(err)
	}
	scB, _ := Resolve(l, "sess-b")
	got, err := List(l, scB)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Body != "found the cause in the retry loop" {
		t.Fatalf("the second session did not see the first one's note: %+v", got)
	}
}

func TestResolveLooseSessionUsesItsOwnScope(t *testing.T) {
	l := resolveLayout(t)
	mkSession(t, l, "sess-loose", "p1")

	sc, err := Resolve(l, "sess-loose")
	if err != nil {
		t.Fatal(err)
	}
	if sc.SessionID != "sess-loose" || sc.TicketID != "" {
		t.Fatalf("scope = %+v, want the session's own scope", sc)
	}
}

func TestResolveSessionWithoutProjectUsesItsOwnScope(t *testing.T) {
	l := resolveLayout(t)
	mkSession(t, l, "sess-np", "")

	sc, err := Resolve(l, "sess-np")
	if err != nil {
		t.Fatal(err)
	}
	if sc.SessionID != "sess-np" || sc.TicketID != "" {
		t.Fatalf("scope = %+v, want the session's own scope", sc)
	}
}

// The back-pointer on session meta is a shortcut, not the authority. When
// it names a ticket the ticket does not claim, the ticket wins and the
// session falls back to its own notes rather than reading someone else's.
func TestResolveIgnoresStaleBackPointer(t *testing.T) {
	l := resolveLayout(t)
	mkSession(t, l, "sess-stale", "p1")
	tk, _ := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})

	sess, err := session.Load(l, "sess-stale")
	if err != nil {
		t.Fatal(err)
	}
	sess.Meta.TicketID = tk.ID // set, but never attached on the ticket side
	if err := session.SaveMeta(l, "sess-stale", sess.Meta); err != nil {
		t.Fatal(err)
	}

	sc, err := Resolve(l, "sess-stale")
	if err != nil {
		t.Fatal(err)
	}
	if sc.TicketID != "" || sc.SessionID != "sess-stale" {
		t.Fatalf("scope = %+v, want the session's own scope (stale pointer ignored)", sc)
	}
}

func TestResolveUnknownSession(t *testing.T) {
	l := resolveLayout(t)
	if _, err := Resolve(l, "ghost"); err == nil {
		t.Fatal("expected an error for an unknown session")
	}
	if _, err := Resolve(l, ""); err == nil {
		t.Fatal("expected an error for an empty session id")
	}
}
