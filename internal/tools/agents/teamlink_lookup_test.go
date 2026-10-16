package agents

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/ticket"
)

// bindSlackThread binds session id to Slack thread (channel, ts).
func bindSlackThread(t *testing.T, id, channel, ts string, active time.Time) {
	t.Helper()
	s, err := session.Load(globalLayout, id)
	if err != nil {
		t.Fatal(err)
	}
	s.Meta.ChannelRef = &session.ChannelRef{Channel: "slack", ChatID: channel, ThreadID: ts}
	s.Meta.LastActive = active
	if err := session.SaveMeta(globalLayout, id, s.Meta); err != nil {
		t.Fatal(err)
	}
	if err := globalMgr.RefreshSession(id); err != nil {
		t.Fatal(err)
	}
}

func lookupRows(t *testing.T, callerAgent, callerSession string) (map[string]ThreadSession, ThreadLookup) {
	t.Helper()
	got, err := TeamSessionLookup(context.Background(), callerAgent, callerSession, "C1", "1792044355.318329")
	if err != nil {
		t.Fatal(err)
	}
	res := got.(ThreadLookup)
	rows := map[string]ThreadSession{}
	for _, r := range res.Sessions {
		rows[r.SessionID] = r
	}
	return rows, res
}

// session_lookup lists the thread's sessions of the caller owner's agents
// (and agents shared with the owner) and counts the rest as hidden.
func TestTeamSessionLookupFiltersByOwner(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "p1", "u1")
	seedTeamProject(t, "p2", "u2")
	seedTeamProject(t, "p3", "u3")
	seedTeamProject(t, "p4", "u1")
	seedTeamAgent(t, "u1", "franky", "p1")
	seedTeamAgent(t, "u2", "bob", "p2")
	carol := seedTeamAgent(t, "u3", "carol", "p3")
	anton := seedTeamAgent(t, "u1", "anton", "p4")
	if err := globalTeam.AddShare(context.Background(), carol.ID, "u1", "u3"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	newSlackSession(t, "slack-f", "p1", "u1", "")
	newSlackSession(t, "slack-b", "p2", "u2", "")
	newSlackSession(t, "slack-c", "p3", "u3", "")
	newSlackSession(t, "slack-x", "", "u1", "")
	newSlackSession(t, "slack-y", "", "u9", "")
	newSlackSession(t, "slack-other", "p1", "u1", "")
	bindSlackThread(t, "slack-f", "C1", "1792044355.318329", now)
	bindSlackThread(t, "slack-b", "C1", "1792044355.318329", now)
	bindSlackThread(t, "slack-c", "C1", "1792044355.318329", now.Add(-time.Minute))
	bindSlackThread(t, "slack-x", "C1", "1792044355.318329", now.Add(-2*time.Minute))
	bindSlackThread(t, "slack-y", "C1", "1792044355.318329", now)
	bindSlackThread(t, "slack-other", "C1", "1792044355.000001", now)

	rows, res := lookupRows(t, anton.ID, "anton-chat")
	if res.Hidden != 2 || len(rows) != 3 {
		t.Fatalf("hidden %d, rows %+v", res.Hidden, res.Sessions)
	}
	if res.Sessions[0].SessionID != "slack-f" || res.Sessions[2].SessionID != "slack-x" {
		t.Fatalf("not newest first: %+v", res.Sessions)
	}
	if f := rows["slack-f"]; f.Agent != "franky" || f.ProjectID != "p1" || f.OwnerUser != "u1" || !f.CanSend || f.Chat != "slack-f" {
		t.Fatalf("owner's agent session: %+v", f)
	}
	// carol is shared with u1, but this session is u3's and in no ticket
	// with the caller: listed, not sendable.
	if c := rows["slack-c"]; c.Agent != "carol" || c.CanSend || c.Reason == "" || c.Chat != "" {
		t.Fatalf("shared agent session: %+v", c)
	}
	if x := rows["slack-x"]; x.Agent != "" || x.CanSend || x.Reason == "" {
		t.Fatalf("owner's session of no agent: %+v", x)
	}
}

// The caller's own agent in the thread is sendable only within one ticket
// with the asking chat, and never the asking chat itself.
func TestTeamSessionLookupSelfNeedsTicket(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "p1", "u1")
	franky := seedTeamAgent(t, "u1", "franky", "p1")
	newSlackSession(t, "slack-f", "p1", "u1", "")
	newSlackSession(t, "slack-req", "p1", "u1", "")
	bindSlackThread(t, "slack-f", "C1", "1792044355.318329", time.Now().UTC())

	if f := mustRow(t, franky.ID, "slack-f", "slack-f"); f.CanSend || f.Reason == "" {
		t.Fatalf("the asking chat itself: %+v", f)
	}
	if f := mustRow(t, franky.ID, "slack-req", "slack-f"); f.CanSend || f.Reason == "" || f.TicketID != "" {
		t.Fatalf("own chat, no ticket: %+v", f)
	}
	tk, err := ticket.Create(globalLayout, ticket.CreateOptions{ProjectID: "p1", Title: "inquiry"})
	if err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"slack-f", "slack-req"} {
		if err := ticket.AttachSession(globalLayout, "p1", tk.ID, sid); err != nil {
			t.Fatal(err)
		}
	}
	if f := mustRow(t, franky.ID, "slack-req", "slack-f"); f.CanSend || f.Reason == "" {
		t.Fatalf("own chat in one ticket, not its owner: %+v", f)
	}
	setTicketOwner(t, "p1", tk.ID, "slack-f")
	if f := mustRow(t, franky.ID, "slack-req", "slack-f"); !f.CanSend || f.Chat != "slack-f" || f.TicketID != tk.ID {
		t.Fatalf("own chat in one ticket: %+v", f)
	}
}

func mustRow(t *testing.T, callerAgent, callerSession, id string) ThreadSession {
	t.Helper()
	rows, _ := lookupRows(t, callerAgent, callerSession)
	r, ok := rows[id]
	if !ok {
		t.Fatalf("%s not listed", id)
	}
	return r
}
