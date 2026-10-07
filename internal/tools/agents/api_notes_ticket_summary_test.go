package agents

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/ticket"
)

func TestNoteTicketSummaryCarriesFields(t *testing.T) {
	tk := ticket.Ticket{
		ID: "T-1", Title: "Kalender dokter", Status: "in_progress", Body: "Desc",
		Fields: map[string]string{"app_code": "app-abc123", "slack": "https://example.slack.com/archives/C1/p1"},
	}
	got := noteTicketSummary(tk)
	for k, want := range map[string]string{"id": "T-1", "title": "Kalender dokter", "status": "in_progress", "body": "Desc"} {
		if got[k] != want {
			t.Errorf("%s = %v, want %q", k, got[k], want)
		}
	}
	fields, ok := got["fields"].(map[string]string)
	if !ok {
		t.Fatalf("fields = %T, want map[string]string", got["fields"])
	}
	if fields["app_code"] != "app-abc123" || fields["slack"] != "https://example.slack.com/archives/C1/p1" {
		t.Errorf("fields = %v", fields)
	}
}

// The rail reads fields.<key> directly; a ticket that never had a field
// must serialise as {} rather than null.
func TestNoteTicketSummaryFieldsNeverNull(t *testing.T) {
	b, err := json.Marshal(noteTicketSummary(ticket.Ticket{ID: "T-2"}))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if string(m["fields"]) != "{}" {
		t.Errorf(`fields = %s, want {}`, m["fields"])
	}
}

func TestTicketPanelButtonsKeepsTicketPlacedClickableOnes(t *testing.T) {
	got := ticketPanelButtons([]project.TicketButton{
		{ID: "b1", Label: " Sync from Notion ", URL: "https://x/refresh"},                              // default placement = ticket
		{ID: "b2", Label: "Sync my tickets", URL: "https://x/board", Placement: project.ButtonOnBoard}, // list button
		{ID: "", Label: "No id yet", URL: "https://x/a"},
		{ID: "b4", Label: "   ", URL: "https://x/b"},
		{ID: "b5", Label: "Open in Notion", URL: "https://x/c", Placement: project.ButtonOnTicket},
	})
	want := []ticketPanelButton{{ID: "b1", Label: "Sync from Notion"}, {ID: "b5", Label: "Open in Notion"}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// The URL stays server-side: the rail clicks through /actions/{buttonID}.
func TestTicketPanelButtonsNeverCarryTheURL(t *testing.T) {
	b, err := json.Marshal(ticketPanelButtons([]project.TicketButton{{ID: "b1", Label: "Sync", URL: "https://secret.example/hook"}}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "secret.example") || strings.Contains(string(b), `"url"`) {
		t.Errorf("button payload leaks the URL: %s", b)
	}
	if string(ticketPanelButtonsJSON(nil)) != "[]" {
		t.Errorf("no buttons must serialise as [], got %s", ticketPanelButtonsJSON(nil))
	}
}

func ticketPanelButtonsJSON(in []project.TicketButton) []byte {
	b, _ := json.Marshal(ticketPanelButtons(in))
	return b
}
