package agents

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/entity"
)

// newSlackSession creates an unstamped top-level session of project pid,
// as a Slack thread in a project without the Team tag gets.
func newSlackSession(t *testing.T, id, pid, user, parent string) {
	t.Helper()
	if _, err := globalMgr.CreateSession(context.Background(), session.CreateOptions{
		ID: id, ProjectID: pid, Origin: session.OriginSlack, UserID: user, ParentSessionID: parent,
	}); err != nil {
		t.Fatal(err)
	}
}

// A Slack thread session of a project exactly one agent points at speaks
// as that agent on the Team paths only; AgentFor still sees none.
func TestTeamAgentOfSessionProjectPersona(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "p1", "u1")
	seedTeamProject(t, "p2", "u1")
	franky := seedTeamAgent(t, "u1", "franky", "p1")
	seedTeamAgent(t, "u1", "one", "p2")
	seedTeamAgent(t, "u1", "two", "p2")
	newSlackSession(t, "slack-main", "p1", "u1", "")
	newSlackSession(t, "slack-sub", "p1", "u1", "slack-main")
	newSlackSession(t, "slack-shared", "p2", "u1", "")
	ctx := context.Background()

	if got := TeamAgentOf(ctx, "slack-main"); got != "" {
		t.Fatalf("AgentFor must not change: %q", got)
	}
	for id, want := range map[string]string{
		"slack-main":   franky.ID,
		"slack-sub":    "", // a sub-agent never gets one this way
		"slack-shared": "", // two agents on the project
		"nope":         "",
	} {
		if got := teamAgentOfSession(ctx, id); got != want {
			t.Errorf("%s: got %q, want %q", id, got, want)
		}
	}
}

// CanTarget admits the agent's session when it exists and is its owner's,
// or in the asking chat's ticket (ticket = true); never a missing one.
func TestPoolTurnsCanTarget(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "p1", "u1")
	franky := seedTeamAgent(t, "u1", "franky", "p1")
	newSlackSession(t, "slack-main", "p1", "u1", "")
	newSlackSession(t, "slack-card", "p1", "u1", "")
	newSlackSession(t, "slack-other", "p1", "u2", "")
	ctx := context.Background()
	turns := poolTurns{}
	peer := teamlink.Peer{ID: franky.ID, OwnerID: "u1", Handle: "franky"}

	if tk, err := turns.CanTarget(ctx, peer, "slack-main", "slack-card"); err != nil || tk {
		t.Fatalf("owner's session: ticket %v, err %v", tk, err)
	}
	if _, err := turns.CanTarget(ctx, peer, "slack-main", "missing-1"); err == nil || !strings.Contains(err.Error(), "session missing-1 not found") {
		t.Fatalf("missing session: %v", err)
	}
	if _, err := turns.CanTarget(ctx, peer, "slack-main", "slack-other"); !errors.Is(err, teamlink.ErrUnknownChat) {
		t.Fatalf("another person's session, no ticket: %v", err)
	}
	if _, err := turns.CanTarget(ctx, teamlink.Peer{ID: "someone-else", OwnerID: "u1"}, "slack-main", "slack-card"); !errors.Is(err, teamlink.ErrUnknownChat) {
		t.Fatalf("a session of another agent: %v", err)
	}
	tk, err := ticket.Create(globalLayout, ticket.CreateOptions{ProjectID: "p1", Title: "inquiry"})
	if err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"slack-main", "slack-other"} {
		if err := ticket.AttachSession(globalLayout, "p1", tk.ID, sid); err != nil {
			t.Fatal(err)
		}
	}
	// slack-other is a Slack thread of the ticket but not its owner:
	// a turn there would be posted in that thread, so it is refused.
	if _, err := turns.CanTarget(ctx, peer, "slack-main", "slack-other"); !errors.Is(err, teamlink.ErrUnknownChat) || !strings.Contains(err.Error(), "owner_session") {
		t.Fatalf("same ticket, not the owner: %v", err)
	}
	setTicketOwner(t, "p1", tk.ID, "slack-other")
	if same, err := turns.CanTarget(ctx, peer, "slack-main", "slack-other"); err != nil || !same {
		t.Fatalf("same ticket, the owner: ticket %v, err %v", same, err)
	}
	// From the owner back to another Slack thread of its ticket (the main
	// thread the client reads): refused, also when it is the chat user's.
	if _, err := turns.CanTarget(ctx, peer, "slack-other", "slack-main"); !errors.Is(err, teamlink.ErrUnknownChat) {
		t.Fatalf("owner to a non-owner thread of the ticket: %v", err)
	}
	if same, err := turns.CanTarget(ctx, peer, "slack-main", "slack-card"); err != nil || same {
		t.Fatalf("outside the ticket is the owner path only: ticket %v, err %v", same, err)
	}
}

// setTicketOwner records sid as the ticket's owner_session, as the
// first-check workflow does.
func setTicketOwner(t *testing.T, pid, ticketID, sid string) {
	t.Helper()
	tk, err := ticket.Load(globalLayout, pid, ticketID)
	if err != nil {
		t.Fatal(err)
	}
	if tk.Fields == nil {
		tk.Fields = map[string]string{}
	}
	tk.Fields[ticket.OwnerSessionField] = sid
	if err := ticket.Save(globalLayout, tk); err != nil {
		t.Fatal(err)
	}
}

// A session answering in a Slack thread is named with its thread link.
func TestPoolTurnsSessionLabelSlackThread(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "p1", "u1")
	newSlackSession(t, "slack-main", "p1", "u1", "")
	s, err := session.Load(globalLayout, "slack-main")
	if err != nil {
		t.Fatal(err)
	}
	s.Meta.ChannelRef = &session.ChannelRef{Channel: "slack", ChatID: "C1", ThreadID: "1792044355.318329"}
	if err := session.SaveMeta(globalLayout, "slack-main", s.Meta); err != nil {
		t.Fatal(err)
	}
	_ = globalMgr.RefreshSession("slack-main")
	got := poolTurns{}.SessionLabel(context.Background(), "slack-main")
	if got != "session slack-main, Slack thread https://slack.com/archives/C1/p1792044355318329" {
		t.Fatalf("label = %q", got)
	}
}

// S1: the project's agent is not the caller of anyone else's session
// there unless that session is in one of the project's tickets, and a
// disabled agent is never one.
func TestTeamAgentOfSessionOwnerOrTicketOnly(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "p1", "u1")
	seedTeamProject(t, "p2", "u1")
	franky := seedTeamAgent(t, "u1", "franky", "p1")
	off := &entity.AgentPersona{OwnerUserID: "u1", Handle: "off", ProjectID: "p2", AllowedConnectors: "[]", Disabled: true}
	if err := globalTeam.Create(context.Background(), off); err != nil {
		t.Fatal(err)
	}
	newSlackSession(t, "slack-req", "p1", "u2", "")
	newSlackSession(t, "slack-off", "p2", "u1", "")
	ctx := context.Background()
	if got := teamAgentOfSession(ctx, "slack-req"); got != "" {
		t.Fatalf("requester's session without a ticket: %q", got)
	}
	if got := teamAgentOfSession(ctx, "slack-off"); got != "" {
		t.Fatalf("disabled agent: %q", got)
	}
	tk, err := ticket.Create(globalLayout, ticket.CreateOptions{ProjectID: "p1", Title: "inquiry"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ticket.AttachSession(globalLayout, "p1", tk.ID, "slack-req"); err != nil {
		t.Fatal(err)
	}
	if got := teamAgentOfSession(ctx, "slack-req"); got != franky.ID {
		t.Fatalf("requester's session in a ticket: %q, want %q", got, franky.ID)
	}
}
