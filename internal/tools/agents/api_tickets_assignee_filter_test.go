package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/entity"
)

// ?assignee= takes a comma list of ids, "me" and "none". A ticket stays when
// ANY of its people is asked for, "none" keeps the unassigned ones, and a
// single value keeps meaning what it always did.
func TestApiProjectTickets_AssigneeFilter(t *testing.T) {
	withSessionWorld(t, []seededSession{{id: "s1", userID: "admin"}})
	admin := &entity.User{ID: "admin", Role: entity.RoleAdmin}

	p, ok := globalMgr.Registry().Project("p1")
	if !ok {
		t.Fatal("project p1 missing")
	}
	meta := p.Meta
	meta.Ticket.Enabled = true
	if _, err := globalMgr.UpdateProject(context.Background(), "p1", meta); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, tc := range []struct {
		title  string
		people []string
	}{
		{"mine", []string{"admin"}},
		{"bob's", []string{"bob"}},
		{"shared", []string{"carol", "bob"}},
		{"nobody's", nil},
	} {
		tk, err := ticket.Create(globalLayout, ticket.CreateOptions{ProjectID: "p1", Title: tc.title, Assignees: tc.people})
		if err != nil {
			t.Fatal(err)
		}
		ids[tk.ID] = tc.title
	}

	titles := func(assignee string) string {
		t.Helper()
		target := "/api/projects/p1/tickets?rows=0"
		if assignee != "" {
			target += "&assignee=" + url.QueryEscape(assignee)
		}
		w, c := userCtx(t, admin, target, map[string]string{"id": "p1"})
		apiProjectTickets(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var resp ticketBoardResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(resp.Tickets))
		for _, card := range resp.Tickets {
			out = append(out, ids[card.ID])
		}
		sort.Strings(out)
		return strings.Join(out, "|")
	}

	for _, tc := range []struct{ assignee, want string }{
		{"", "bob's|mine|nobody's|shared"},
		{"me", "mine"},
		{"bob", "bob's|shared"},
		{"none", "nobody's"},
		{"me,none", "mine|nobody's"},
		{"carol, me", "mine|shared"},
		{"me,bob,none", "bob's|mine|nobody's|shared"},
		{"ghost", ""},
		{",", "bob's|mine|nobody's|shared"},
	} {
		if got := titles(tc.assignee); got != tc.want {
			t.Errorf("assignee=%q: got %q, want %q", tc.assignee, got, tc.want)
		}
	}
}

func TestParseAssigneeFilter(t *testing.T) {
	tk := func(people ...string) ticket.Ticket { return ticket.Ticket{Assignees: people} }

	f := parseAssigneeFilter("", "u1")
	if f.active() || !f.matches(tk()) || !f.matches(tk("x")) {
		t.Fatalf("empty filter must keep everything: %+v", f)
	}
	// "me" with nobody logged in drops out — an "only me" filter then
	// shows everyone, as the single value always did.
	if f = parseAssigneeFilter("me", ""); f.active() {
		t.Fatalf("me without a caller: %+v, want inactive", f)
	}
	f = parseAssigneeFilter("me,none", "u1")
	if !f.matches(tk("u1")) || !f.matches(tk()) || f.matches(tk("u2")) {
		t.Fatalf("me,none: %+v", f)
	}
	// A legacy single-Assignee ticket counts too.
	if !parseAssigneeFilter("u2", "").matches(ticket.Ticket{Assignee: "u2"}) {
		t.Fatal("legacy Assignee not matched")
	}
}

// A board button sends the same list, and the tickets it carries follow it.
func TestSelectBoardTicketsTakesAnAssigneeList(t *testing.T) {
	tickets := []ticket.Ticket{
		{ID: "T-1", Assignees: []string{"usr_1"}},
		{ID: "T-2", Assignees: []string{"usr_2", "usr_3"}},
		{ID: "T-3"},
	}
	rows, n, _ := selectBoardTickets(tickets, nil, parseAssigneeFilter("usr_3,none", ""))
	got := []string{}
	for _, r := range rows {
		got = append(got, r.ID)
	}
	sort.Strings(got)
	if n != 2 || strings.Join(got, ",") != "T-2,T-3" {
		t.Fatalf("rows=%v n=%d, want T-2,T-3", got, n)
	}
}

// A filter saved as one `assignee` is read back as the list the board uses.
func TestApiTicketFilterGetMigratesTheOldAssignee(t *testing.T) {
	u := &entity.User{ID: "admin", Role: entity.RoleAdmin}
	u.Metadata.TicketFilters = map[string]entity.TicketFilter{"p1": {Assignee: "u7"}}
	w, c := userCtx(t, u, "/api/me/ticket-filters/p1", map[string]string{"projectID": "p1"})
	apiTicketFilterGet(c)
	var got entity.TicketFilter
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Assignees) != 1 || got.Assignees[0] != "u7" {
		t.Fatalf("got %+v, want assignees [u7]", got)
	}
}

// The webhook says what the assignee pick was in a way an old reader of
// assignee_id cannot mistake for "everyone": several people or "none"
// leave assignee_id empty but set assignee_filtered and the list.
func TestBuildBoardContextResolvesAnAssigneeList(t *testing.T) {
	withSessionWorld(t, []seededSession{{id: "s1", userID: "admin"}})
	admin := &entity.User{ID: "admin", Role: entity.RoleAdmin}
	for _, title := range []string{"a", "b"} {
		people := []string{"admin"}
		if title == "b" {
			people = nil
		}
		if _, err := ticket.Create(globalLayout, ticket.CreateOptions{ProjectID: "p1", Title: title, Assignees: people}); err != nil {
			t.Fatal(err)
		}
	}
	build := func(assignee string) ticket.BoardContext {
		_, c := userCtx(t, admin, "/api/projects/p1/board-actions/b1", map[string]string{"id": "p1"})
		return buildBoardContext(c, project.TicketConfig{}, "p1", boardActionRequest{Assignee: assignee})
	}

	got := build("me,usr_2")
	if !got.AssigneeFiltered || got.AssigneeID != "" || got.AssigneeNone ||
		strings.Join(got.AssigneeIDs, ",") != "admin,usr_2" || got.MatchCount != 1 {
		t.Fatalf("me,usr_2: %+v", got)
	}
	got = build("none")
	if !got.AssigneeFiltered || !got.AssigneeNone || got.AssigneeID != "" || len(got.AssigneeIDs) != 0 || got.MatchCount != 1 {
		t.Fatalf("none: %+v", got)
	}
	got = build("me")
	if !got.AssigneeFiltered || got.AssigneeID != "admin" || got.MatchCount != 1 {
		t.Fatalf("me: %+v", got)
	}
	got = build("")
	if got.AssigneeFiltered || got.AssigneeID != "" || got.MatchCount != 2 {
		t.Fatalf("everyone: %+v", got)
	}
}
