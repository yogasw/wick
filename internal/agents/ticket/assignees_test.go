package ticket

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/storage"
)

func TestSetAssigneesKeepsTheSingularFieldInStep(t *testing.T) {
	var tk Ticket
	tk.SetAssignees([]string{" u-b ", "u-a", "u-b", "", "u-a"})
	if got := strings.Join(tk.Assignees, ","); got != "u-b,u-a" {
		t.Fatalf("assignees = %q, want trimmed, de-duplicated, in the order given", got)
	}
	// Everything that predates the list — board filters, webhook payloads,
	// the sweeper's prompt — reads Assignee, so it has to be the first one.
	if tk.Assignee != "u-b" {
		t.Fatalf("assignee = %q, want the first of the list", tk.Assignee)
	}
}

func TestSetAssigneesEmptyUnassigns(t *testing.T) {
	tk := Ticket{Assignee: "u-a", Assignees: []string{"u-a"}}
	tk.SetAssignees([]string{"  ", ""})
	if tk.Assignee != "" || tk.Assignees != nil {
		t.Fatalf("a list of blanks should unassign, got %q / %v", tk.Assignee, tk.Assignees)
	}
}

func TestHasAssigneeMatchesAnyoneOnIt(t *testing.T) {
	tk := Ticket{}
	tk.SetAssignees([]string{"u-a", "u-b"})
	for _, id := range []string{"u-a", "u-b"} {
		if !tk.HasAssignee(id) {
			t.Fatalf("%s is on the ticket but HasAssignee said no", id)
		}
	}
	if tk.HasAssignee("u-c") {
		t.Fatal("u-c is not on the ticket")
	}
}

// A ticket written before the list existed carries only `assignee`. Reading
// one must produce the same shape as a ticket written today, or every caller
// would have to handle both.
func TestLoadFoldsALegacyAssigneeIntoTheList(t *testing.T) {
	l := newLayout(t)
	tk, err := Create(l, CreateOptions{ProjectID: "p1", Title: "Legacy"})
	if err != nil {
		t.Fatal(err)
	}
	// Write the old shape straight to disk: `assignee` set, no `assignees`.
	raw := map[string]any{
		"id": tk.ID, "project_id": "p1", "title": "Legacy",
		"status": tk.Status, "assignee": "u-old",
		"created_at": tk.CreatedAt, "updated_at": tk.UpdatedAt,
	}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.TicketFile("p1", tk.ID), b, 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Load(l, "p1", tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Assignees) != 1 || got.Assignees[0] != "u-old" {
		t.Fatalf("assignees = %v, want the legacy assignee folded in", got.Assignees)
	}
}

// And the reverse: a caller that only filled in the list gets `assignee`
// written out for it, so nothing downstream has to know which door the
// ticket came in through.
func TestSaveNormalisesAListOnlyTicket(t *testing.T) {
	l := newLayout(t)
	tk, err := Create(l, CreateOptions{ProjectID: "p1", Title: "Shared"})
	if err != nil {
		t.Fatal(err)
	}
	tk.Assignees = []string{"u-a", "u-b"}
	tk.Assignee = ""
	if err := Save(l, tk); err != nil {
		t.Fatal(err)
	}
	var onDisk Ticket
	if err := storage.ReadJSON(l.TicketFile("p1", tk.ID), &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.Assignee != "u-a" {
		t.Fatalf("assignee on disk = %q, want the first of the list", onDisk.Assignee)
	}
}

func TestCreateMergesAssigneeAndAssignees(t *testing.T) {
	l := newLayout(t)
	tk, err := Create(l, CreateOptions{
		ProjectID: "p1", Title: "Shared work",
		Assignee: "u-a", Assignees: []string{"u-b", "u-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(tk.Assignees, ","); got != "u-a,u-b" {
		t.Fatalf("assignees = %q, want the singular one first and no duplicate", got)
	}
}

// Adding a second person does not move `assignee`, so a diff that only
// watched that field reported nothing had changed.
func TestDiffNoticesAChangeBehindTheFirstAssignee(t *testing.T) {
	before, after := Ticket{}, Ticket{}
	before.SetAssignees([]string{"u-a"})
	after.SetAssignees([]string{"u-a", "u-b"})
	changes := diff(before, after)
	if _, ok := changes["assignees"]; !ok {
		t.Fatalf("no assignees change reported: %v", changes)
	}
	events := EventsFor(changes)
	found := false
	for _, e := range events {
		if e == EventAssigned {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %v, want %s — somebody was assigned", events, EventAssigned)
	}
}
