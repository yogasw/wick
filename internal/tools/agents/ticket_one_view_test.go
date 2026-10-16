package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// A ticket's room is one more row of its Sessions: the ticket page no
// longer draws rooms, so the thread must stay reachable from the list.
func TestTicketDetailListsRoomAsSessionRow(t *testing.T) {
	withTeamWorld(t)
	alice := &entity.User{ID: "alice"}
	seedTeamProject(t, "p1", alice.ID)
	tk := seedTicket(t, "p1", "Webhook 401")
	code, room := openRoom(t, alice, tk.ID)
	if code != http.StatusCreated {
		t.Fatalf("create room: %d", code)
	}

	w, c := teamReq(t, alice, http.MethodGet, "/?project=p1", nil, map[string]string{"ticketID": tk.ID})
	apiTicketDetail(c)
	if w.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Sessions []struct {
			ID string `json:"id"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].ID != room.SessionID {
		t.Fatalf("sessions = %+v, want the room %q as the one row", got.Sessions, room.SessionID)
	}
}

// Only an enabled Captain answers a Team ticket chat, and only the
// caller's own.
func TestOwnCaptain(t *testing.T) {
	if _, ok := captainIn([]entity.AgentPersona{{ID: "a"}, {ID: "b", IsCaptain: true, Disabled: true}}); ok {
		t.Fatal("a disabled Captain must not answer")
	}
	if p, ok := captainIn([]entity.AgentPersona{{ID: "a"}, {ID: "cap", IsCaptain: true}}); !ok || p.ID != "cap" {
		t.Fatalf("captainIn = %q %v, want cap", p.ID, ok)
	}

	withTeamWorld(t)
	seedTeamProject(t, "pa", "alice")
	seedTeamProject(t, "pb", "bob")
	captain := &entity.AgentPersona{OwnerUserID: "alice", Handle: "captain", ProjectID: "pa", AllowedConnectors: "[]", IsCaptain: true}
	if err := globalTeam.Create(context.Background(), captain); err != nil {
		t.Fatal(err)
	}
	seedTeamAgent(t, "bob", "helper", "pb")
	if p, ok := ownCaptain(context.Background(), "alice"); !ok || p.ID != captain.ID {
		t.Fatalf("alice's Captain = %q %v, want %q", p.ID, ok, captain.ID)
	}
	if _, ok := ownCaptain(context.Background(), "bob"); ok {
		t.Fatal("bob has no Captain; none must be borrowed")
	}
	if _, ok := ownCaptain(context.Background(), ""); ok {
		t.Fatal("no caller, no Captain")
	}
}

// A Captain chat on a ticket carries its ticket and project to the
// Team drawer's chip; a plain chat carries none.
func TestTeamAgentSessionItemNamesTicket(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "p1", "alice")
	on := session.Session{ID: "s1", Meta: session.Meta{UserID: "alice", ProjectID: "p1", TicketID: "T-1"}}
	it := teamAgentSessionItem(on, "alice", nil, teamLive{})
	if it.TicketID != "T-1" || it.TicketProjectID != "p1" || it.TicketProjectName != "p1" {
		t.Fatalf("item = %+v, want T-1 in p1", it)
	}
	off := teamAgentSessionItem(session.Session{ID: "s2", Meta: session.Meta{UserID: "alice", ProjectID: "p1"}}, "alice", nil, teamLive{})
	if off.TicketID != "" || off.TicketProjectID != "" || off.TicketProjectName != "" {
		t.Fatalf("plain chat = %+v, want no ticket", off)
	}
}
