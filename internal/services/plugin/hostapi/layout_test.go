package hostapi

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// layoutFixture: projects "a" and "b" owned by ada (both API on), "c" owned
// by bob (API on), "off" owned by ada with the ticket API off. Ticket id
// "dupe-1" exists in a and b; "bob-1" only in c.
func layoutFixture(t *testing.T) *LayoutData {
	t.Helper()
	l := agentconfig.Layout{BaseDir: t.TempDir()}
	for _, p := range []struct {
		id, owner string
		api       bool
	}{{"a", "ada", true}, {"b", "ada", true}, {"c", "bob", true}, {"off", "ada", false}} {
		pr, err := project.Create(l, project.CreateOptions{ID: p.id, Name: p.id, OwnerUserID: p.owner})
		if err != nil {
			t.Fatal(err)
		}
		pr.Meta.Ticket.Integrations.APIEnabled = p.api
		if err := project.SaveMeta(l, p.id, pr.Meta); err != nil {
			t.Fatal(err)
		}
	}
	for _, tk := range []struct{ p, id string }{{"a", "dupe-1"}, {"b", "dupe-1"}, {"a", "one-1"}, {"c", "bob-1"}, {"off", "off-1"}} {
		if _, err := ticket.Create(l, ticket.CreateOptions{ProjectID: tk.p, ID: tk.id, Title: tk.p + " " + tk.id}); err != nil {
			t.Fatalf("create %s/%s: %v", tk.p, tk.id, err)
		}
	}
	for _, s := range []struct{ id, p, user string }{{"s-ada", "a", "ada"}, {"s-bob", "c", "bob"}} {
		if _, err := session.Create(context.Background(), l, session.CreateOptions{ID: s.id, ProjectID: s.p, UserID: s.user}); err != nil {
			t.Fatal(err)
		}
	}
	users := map[string]*entity.User{
		"ada":     {ID: "ada", Name: "Ada", Approved: true},
		"bob":     {ID: "bob", Name: "Bob", Approved: true},
		"pending": {ID: "pending", Name: "P"},
	}
	return &LayoutData{Layout: l, Users: func(_ context.Context, id string) (*entity.User, []string, error) {
		return users[id], nil, nil
	}}
}

func codeIs(t *testing.T, err error, want codes.Code, ctxt string) {
	t.Helper()
	if status.Code(err) != want {
		t.Fatalf("%s: err = %v, want %s", ctxt, err, want)
	}
}

func TestLayoutGetTicketAmbiguity(t *testing.T) {
	l := layoutFixture(t)
	ctx := context.Background()
	_, err := l.GetTicket(ctx, "ada", &hostpb.GetTicketRequest{Id: "dupe-1"})
	codeIs(t, err, codes.FailedPrecondition, "ambiguous id")
	if !strings.Contains(err.Error(), "pass project_id") {
		t.Fatalf("ambiguity message = %v", err)
	}
	tk, err := l.GetTicket(ctx, "ada", &hostpb.GetTicketRequest{Id: "dupe-1", ProjectId: "b"})
	if err != nil || tk.ProjectId != "b" || tk.Title != "b dupe-1" {
		t.Fatalf("with project_id = %+v, %v", tk, err)
	}
	if tk, err = l.GetTicket(ctx, "ada", &hostpb.GetTicketRequest{Id: "one-1"}); err != nil || tk.ProjectId != "a" {
		t.Fatalf("unique id = %+v, %v", tk, err)
	}
	// Bob cannot see ada's projects, so for him dupe-1 simply does not exist.
	_, err = l.GetTicket(ctx, "bob", &hostpb.GetTicketRequest{Id: "dupe-1"})
	codeIs(t, err, codes.NotFound, "other user's ticket")
	_, err = l.GetTicket(ctx, "ada", &hostpb.GetTicketRequest{Id: "bob-1", ProjectId: "c"})
	codeIs(t, err, codes.NotFound, "other user's project")
}

func TestLayoutIdentityAndAPIToggle(t *testing.T) {
	l := layoutFixture(t)
	ctx := context.Background()
	_, err := l.GetTicket(ctx, "", &hostpb.GetTicketRequest{Id: "one-1"})
	codeIs(t, err, codes.FailedPrecondition, "no user")
	_, err = l.GetTicket(ctx, "pending", &hostpb.GetTicketRequest{Id: "one-1"})
	codeIs(t, err, codes.FailedPrecondition, "unapproved user")
	_, err = l.GetTicket(ctx, "ada", &hostpb.GetTicketRequest{Id: "off-1"})
	codeIs(t, err, codes.FailedPrecondition, "API toggle off (found by search)")
	_, err = l.ListTickets(ctx, "ada", &hostpb.ListTicketsRequest{ProjectId: "off"})
	codeIs(t, err, codes.FailedPrecondition, "API toggle off (list)")
	_, err = l.CreateTicket(ctx, "ada", &hostpb.CreateTicketRequest{ProjectId: "off", Title: "x"})
	codeIs(t, err, codes.FailedPrecondition, "API toggle off (create)")
}

func TestLayoutListTicketsCarriesProject(t *testing.T) {
	l := layoutFixture(t)
	list, err := l.ListTickets(context.Background(), "ada", &hostpb.ListTicketsRequest{ProjectId: "a"})
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %v, %v", list, err)
	}
	for _, tk := range list {
		if tk.ProjectId != "a" {
			t.Fatalf("ticket %s project_id = %q", tk.Id, tk.ProjectId)
		}
	}
}

func TestLayoutUpdateTicket(t *testing.T) {
	l := layoutFixture(t)
	ctx := context.Background()
	title, status := "renamed", "in_progress"
	_, err := l.UpdateTicket(ctx, "ada", &hostpb.UpdateTicketRequest{Id: "dupe-1", Title: &title})
	codeIs(t, err, codes.FailedPrecondition, "ambiguous update")
	got, err := l.UpdateTicket(ctx, "ada", &hostpb.UpdateTicketRequest{Id: "dupe-1", ProjectId: "b", Title: &title, Status: &status,
		Fields: map[string]string{"k": "v"}})
	if err != nil || got.Title != "renamed" || got.Status != "in_progress" || got.Fields["k"] != "v" {
		t.Fatalf("update = %+v, %v", got, err)
	}
	other, _ := ticket.Load(l.Layout, "a", "dupe-1")
	if other.Title != "a dupe-1" {
		t.Fatalf("the other project's ticket changed: %+v", other)
	}
	_, err = l.UpdateTicket(ctx, "ada", &hostpb.UpdateTicketRequest{Id: "dupe-1", ProjectId: "b", Title: &title, IfUpdatedAt: "2000-01-01T00:00:00Z"})
	codeIs(t, err, codes.Aborted, "stale if_updated_at")
	bad := "nope"
	_, err = l.UpdateTicket(ctx, "ada", &hostpb.UpdateTicketRequest{Id: "one-1", Status: &bad})
	codeIs(t, err, codes.InvalidArgument, "invalid status")
}

func TestLayoutAttachSession(t *testing.T) {
	l := layoutFixture(t)
	ctx := context.Background()
	err := l.AttachSession(ctx, "ada", &hostpb.AttachSessionRequest{TicketId: "dupe-1", SessionId: "s-ada"})
	codeIs(t, err, codes.FailedPrecondition, "ambiguous attach")
	if err := l.AttachSession(ctx, "ada", &hostpb.AttachSessionRequest{TicketId: "dupe-1", ProjectId: "a", SessionId: "s-ada"}); err != nil {
		t.Fatal(err)
	}
	tk, _ := ticket.Load(l.Layout, "a", "dupe-1")
	if len(tk.Sessions) != 1 || tk.Sessions[0] != "s-ada" {
		t.Fatalf("sessions = %v", tk.Sessions)
	}
	if sess, _ := session.Load(l.Layout, "s-ada"); sess.Meta.TicketID != "dupe-1" {
		t.Fatalf("back-pointer = %q", sess.Meta.TicketID)
	}
	// Bob's session is not ada's to attach.
	err = l.AttachSession(ctx, "ada", &hostpb.AttachSessionRequest{TicketId: "one-1", SessionId: "s-bob"})
	codeIs(t, err, codes.NotFound, "other user's session")
	// A ticket room stays on its ticket, and a member's backing session is
	// not filed anywhere.
	for _, id := range []string{"room-x", "group-x"} {
		if _, err := session.Create(ctx, l.Layout, session.CreateOptions{ID: id, ProjectID: "a", UserID: "ada"}); err != nil {
			t.Fatal(err)
		}
	}
	roomMeta, _ := session.Load(l.Layout, "room-x")
	roomMeta.Meta.TicketRoom = &session.TicketRoom{TicketID: "dupe-1"}
	if err := session.SaveMeta(l.Layout, "room-x", roomMeta.Meta); err != nil {
		t.Fatal(err)
	}
	for _, m := range []func(*session.Meta){
		func(m *session.Meta) { m.TicketRoom = &session.TicketRoom{TicketID: "dupe-1"} },
		func(m *session.Meta) { m.GroupSessionID = "room-x" },
	} {
		sess, _ := session.Load(l.Layout, "s-ada")
		sess.Meta.TicketRoom, sess.Meta.GroupSessionID = nil, ""
		m(&sess.Meta)
		if err := session.SaveMeta(l.Layout, "s-ada", sess.Meta); err != nil {
			t.Fatal(err)
		}
		err = l.AttachSession(ctx, "ada", &hostpb.AttachSessionRequest{TicketId: "one-1", ProjectId: "a", SessionId: "s-ada"})
		codeIs(t, err, codes.FailedPrecondition, "room session")
	}
	if tk, _ := ticket.Load(l.Layout, "a", "one-1"); len(tk.Sessions) != 0 {
		t.Fatalf("room session moved onto one-1: %v", tk.Sessions)
	}
	// An ordinary group chat's backing session is not a room's: it files.
	sess, _ := session.Load(l.Layout, "s-ada")
	sess.Meta.TicketRoom, sess.Meta.GroupSessionID = nil, "group-x"
	if err := session.SaveMeta(l.Layout, "s-ada", sess.Meta); err != nil {
		t.Fatal(err)
	}
	if err := l.AttachSession(ctx, "ada", &hostpb.AttachSessionRequest{TicketId: "one-1", ProjectId: "a", SessionId: "s-ada"}); err != nil {
		t.Fatalf("group backing: %v", err)
	}
}

func TestLayoutSessions(t *testing.T) {
	l := layoutFixture(t)
	ctx := context.Background()
	list, err := l.ListSessions(ctx, "ada", &hostpb.ListSessionsRequest{})
	if err != nil || len(list) != 1 || list[0].Id != "s-ada" || list[0].OwnerUserId != "ada" || list[0].ProjectId != "a" {
		t.Fatalf("ada sessions = %+v, %v", list, err)
	}
	if list, _ = l.ListSessions(ctx, "ada", &hostpb.ListSessionsRequest{ProjectId: "b"}); len(list) != 0 {
		t.Fatalf("project filter = %+v", list)
	}
	_, err = l.GetSessionMeta(ctx, "ada", "s-bob")
	codeIs(t, err, codes.NotFound, "other user's session meta")
	_, err = l.ListSessions(ctx, "ada", &hostpb.ListSessionsRequest{Since: "yesterday"})
	codeIs(t, err, codes.InvalidArgument, "bad since")
	_, err = l.SendToSession(ctx, "ada", "s-ada", "hi")
	codeIs(t, err, codes.Unimplemented, "send without a sender")
}
