package agents

import (
	"testing"

	agentsconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/ticket"
)

func ticketLayout(t *testing.T) agentsconfig.Layout {
	t.Helper()
	l := agentsconfig.NewLayout(t.TempDir())
	if err := l.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if _, err := project.Create(l, project.CreateOptions{ID: "p1", Name: "one"}); err != nil {
		t.Fatal(err)
	}
	return l
}

// Moving the last chat off a ticket leaves nothing to track, and the board
// is told so it can offer the husk's removal.
func TestEmptiedResponseReportsATicketWithNoSessionsLeft(t *testing.T) {
	l := ticketLayout(t)
	tk, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	if err != nil {
		t.Fatal(err)
	}

	got := emptiedResponse(l, "p1", tk, true)
	if got["status"] != "ok" {
		t.Fatalf("status = %v", got["status"])
	}
	e, ok := got["emptied_ticket"].(map[string]string)
	if !ok {
		t.Fatalf("expected an emptied_ticket, got %#v", got)
	}
	if e["id"] != tk.ID || e["title"] != "work" {
		t.Fatalf("emptied_ticket = %v, want %s/work", e, tk.ID)
	}
}

// A ticket that still holds work is not offered for deletion.
func TestEmptiedResponseSilentWhenSessionsRemain(t *testing.T) {
	l := ticketLayout(t)
	tk, _ := ticket.Create(l, ticket.CreateOptions{
		ProjectID: "p1", Title: "work", Sessions: []string{"s-left-behind"},
	})

	got := emptiedResponse(l, "p1", tk, true)
	if _, present := got["emptied_ticket"]; present {
		t.Fatalf("a ticket with sessions must not be offered for deletion: %#v", got)
	}
}

// Attaching a chat that was on no ticket has no previous owner to empty.
func TestEmptiedResponseSilentWithoutAPreviousTicket(t *testing.T) {
	l := ticketLayout(t)
	got := emptiedResponse(l, "p1", ticket.Ticket{}, false)
	if _, present := got["emptied_ticket"]; present {
		t.Fatalf("nothing was left, so nothing should be reported: %#v", got)
	}
}

// A ticket deleted between the move and this check must not resurrect as a
// deletion prompt for something that is already gone.
func TestEmptiedResponseSilentWhenTheTicketIsGone(t *testing.T) {
	l := ticketLayout(t)
	tk, _ := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "work"})
	if err := ticket.Delete(l, "p1", tk.ID); err != nil {
		t.Fatal(err)
	}

	got := emptiedResponse(l, "p1", tk, true)
	if _, present := got["emptied_ticket"]; present {
		t.Fatalf("an already-deleted ticket must not be offered: %#v", got)
	}
}
