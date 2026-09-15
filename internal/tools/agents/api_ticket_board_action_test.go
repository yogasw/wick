package agents

import (
	"testing"
	"time"

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
