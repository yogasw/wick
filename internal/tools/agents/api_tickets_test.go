package agents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	agentsconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/notes"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/registry"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/pkg/tool"
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

/* ── board request params ────────────────────────────────────────────────── */

// A missing param and an empty one are different requests: absent means "no
// opinion, send the default", while `?statuses=` is the caller saying it drew
// no columns and wants no cards. Collapsing the two would make an all-off
// board silently show everything.
func TestQueryCSVSeparatesAbsentFromExplicitlyEmpty(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		present bool
		want    map[string]bool
	}{
		{name: "absent", url: "/x", present: false},
		{name: "explicitly empty", url: "/x?statuses=", present: true, want: map[string]bool{}},
		{name: "one", url: "/x?statuses=open", present: true, want: map[string]bool{"open": true}},
		{
			name:    "several, with padding",
			url:     "/x?statuses=open,%20waiting%20,,done",
			present: true,
			want:    map[string]bool{"open": true, "waiting": true, "done": true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &tool.Ctx{R: httptest.NewRequest(http.MethodGet, tc.url, nil)}
			got, present := queryCSV(c, "statuses")
			if present != tc.present {
				t.Fatalf("present = %v, want %v", present, tc.present)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("set = %v, want %v", got, tc.want)
			}
			for k := range tc.want {
				if !got[k] {
					t.Errorf("set is missing %q: %v", k, got)
				}
			}
		})
	}
}

// The untracked list is the board's most expensive part, so it is opt-in:
// anything that is not a plain yes leaves it unbuilt.
func TestIsTrueishOnlyAcceptsAnActualYes(t *testing.T) {
	for _, yes := range []string{"1", "true", "TRUE", "yes", "on", " 1 "} {
		if !isTrueish(yes) {
			t.Errorf("isTrueish(%q) = false, want true", yes)
		}
	}
	for _, no := range []string{"", "0", "false", "no", "off", "2", "maybe"} {
		if isTrueish(no) {
			t.Errorf("isTrueish(%q) = true, want false", no)
		}
	}
}

/*
The rail asks "does this project run tickets?" through the notes payload.

	A session that is not on a ticket resolves to its own scope, which carries
	no project — so the answer has to come from the session itself. Without
	that, the field went missing, the rail read the gap as an older server,
	and a project with tickets switched OFF still showed a Ticket tab.
*/
func TestNotesProjectIDFallsBackToTheSessionsProject(t *testing.T) {
	layout := agentsconfig.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if _, err := project.Create(layout, project.CreateOptions{ID: "p1", Name: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID: "s1", ProjectID: "p1", Origin: session.OriginUI,
	}); err != nil {
		t.Fatal(err)
	}
	reg := registry.New(layout)
	if err := reg.Reload(); err != nil {
		t.Fatal(err)
	}
	prev := globalMgr
	globalMgr = registry.NewManager(reg)
	t.Cleanup(func() { globalMgr = prev })

	if got := notesProjectID(notes.Scope{SessionID: "s1"}); got != "p1" {
		t.Fatalf("project for a ticket-less session = %q, want p1", got)
	}
	// A ticket scope already names its project and must be left alone.
	if got := notesProjectID(notes.Scope{ProjectID: "p2", TicketID: "T-1"}); got != "p2" {
		t.Fatalf("project for a ticket scope = %q, want p2", got)
	}
	// A session nobody has heard of names no project rather than guessing.
	if got := notesProjectID(notes.Scope{SessionID: "gone"}); got != "" {
		t.Fatalf("project for an unknown session = %q, want empty", got)
	}
}
