package agents

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/ticket"
)

// The list a board button sends IS the list the clicker was looking at, so
// the filter has to be applied exactly as the board applies it: an empty
// status set means every column, and an assignee narrows to one person.
func TestSelectBoardTicketsAppliesTheToolbarFilter(t *testing.T) {
	base := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	tickets := []ticket.Ticket{
		{ID: "T-1", Status: "open", Assignee: "usr_1", UpdatedAt: base},
		{ID: "T-2", Status: "done", Assignee: "usr_1", UpdatedAt: base.Add(time.Hour)},
		{ID: "T-3", Status: "open", Assignee: "usr_2", UpdatedAt: base.Add(2 * time.Hour)},
	}

	rows, n, truncated := selectBoardTickets(tickets, nil, "")
	if n != 3 || len(rows) != 3 || truncated {
		t.Fatalf("no filter = %d rows (%d matched, truncated=%v), want all three", len(rows), n, truncated)
	}
	// Newest first: a truncated list must keep the rows most likely to matter.
	if rows[0].ID != "T-3" || rows[2].ID != "T-1" {
		t.Fatalf("order = %s,%s,%s, want newest first", rows[0].ID, rows[1].ID, rows[2].ID)
	}

	rows, n, _ = selectBoardTickets(tickets, map[string]bool{"open": true}, "usr_1")
	if n != 1 || len(rows) != 1 || rows[0].ID != "T-1" {
		t.Fatalf("open + usr_1 = %+v (%d matched), want just T-1", rows, n)
	}

	// The field map travels whole: a receiver's join key is exactly the
	// kind of field nobody marks show_on_card.
	withFields := []ticket.Ticket{{ID: "T-9", Status: "open", Fields: map[string]string{"notion_page_id": "abc"}}}
	rows, _, _ = selectBoardTickets(withFields, nil, "")
	if rows[0].Fields["notion_page_id"] != "abc" {
		t.Fatalf("fields = %+v, want the full map", rows[0].Fields)
	}
}

// A capped list still reports the honest total — a receiver that believed
// it had seen everything would quietly act on a subset.
func TestSelectBoardTicketsCapsRowsButNotTheCount(t *testing.T) {
	tickets := make([]ticket.Ticket, maxBoardActionTickets+25)
	for i := range tickets {
		tickets[i] = ticket.Ticket{ID: "T-" + string(rune('a'+i%26)), Status: "open"}
	}
	rows, n, truncated := selectBoardTickets(tickets, nil, "")
	if len(rows) != maxBoardActionTickets {
		t.Fatalf("rows = %d, want the cap %d", len(rows), maxBoardActionTickets)
	}
	if n != len(tickets) || !truncated {
		t.Fatalf("matched = %d truncated = %v, want %d and true", n, truncated, len(tickets))
	}
}

// A poll URL is chosen by the RECEIVER, not by an operator, so it is not
// allowed to point anywhere the button itself does not already reach —
// otherwise a ticket button becomes an SSRF primitive with the server's
// network position.
func TestSameOriginGuardsThePollURL(t *testing.T) {
	const btn = "https://abc.com/hooks/sync"

	for _, ok := range []string{
		"https://abc.com/hooks/sync/status?run=7",
		"https://abc.com/anything/else",
	} {
		if err := sameOrigin(btn, ok); err != nil {
			t.Errorf("same origin %q refused: %v", ok, err)
		}
	}

	for _, bad := range []string{
		"http://abc.com/hooks/sync",       // scheme downgrade
		"https://evil.com/hooks/sync",     // another host
		"https://abc.com.evil.com/status", // suffix trick
		"https://169.254.169.254/latest",  // the classic target
		"/hooks/sync/status",              // not absolute
		"",
	} {
		if err := sameOrigin(btn, bad); err == nil {
			t.Errorf("poll url %q should have been refused", bad)
		}
	}
}

// The receiver's JSON is passed through to the client verbatim; anything
// that is not a JSON object is dropped rather than half-rendered.
func TestReplyObjectOnlyAcceptsObjects(t *testing.T) {
	obj := replyObject(`{"status":"running","progress":{"done":3,"total":9}}`)
	if obj == nil || obj["status"] != "running" {
		t.Fatalf("object reply = %+v, want it passed through", obj)
	}
	for _, raw := range []string{"", "   ", "not json", `["a"]`, `"just a string"`} {
		if got := replyObject(raw); got != nil {
			t.Errorf("replyObject(%q) = %+v, want nil", raw, got)
		}
	}
}

// The board draws a chosen few fields; a MACHINE reading the same endpoint
// needs the ones nobody marks — an external id, a mirror's page reference.
// Without ?fields=all a sync cannot recognise its own tickets and re-creates
// them on every run.
func TestCardFieldsRespectsTheAllFlag(t *testing.T) {
	cfg := project.TicketConfig{Fields: []project.TicketField{
		{Key: "priority", ShowOnCard: true},
		{Key: "severity"},
	}}
	stored := map[string]string{
		"priority":       "high",
		"severity":       "sev2",
		"notion_page_id": "3c11f07f-4ae0-8110-a59f-c2d586163299",
	}

	card := cardFields(cfg, stored, false)
	if len(card) != 1 || card["priority"] != "high" {
		t.Fatalf("card = %+v, want only the show_on_card field", card)
	}

	all := cardFields(cfg, stored, true)
	if len(all) != 3 || all["notion_page_id"] == "" {
		t.Fatalf("fields=all = %+v, want every stored field", all)
	}
	// A copy, not the ticket's own map: a handler that mutated it would be
	// editing stored state.
	all["priority"] = "changed"
	if stored["priority"] != "high" {
		t.Error("cardFields handed out the ticket's own map")
	}
}
