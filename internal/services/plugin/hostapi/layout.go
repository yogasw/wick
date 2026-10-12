package hostapi

import (
	"context"
	"sort"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/plugin/proto/hostpb"
)

// Default and maximum page sizes of the list RPCs.
const (
	defaultListLimit = 100
	maxListLimit     = 1000
)

// UserLookup loads the acting user and their tag ids, so access checks see
// them exactly as they would see that person signed in.
type UserLookup func(ctx context.Context, userID string) (*entity.User, []string, error)

// LayoutData serves sessions and tickets from wick's on-disk agent layout,
// through the same packages and access checks the built-in tickets
// connector uses (ticket.*, session.*, team.Check*). Only metadata leaves:
// never a conversation.
type LayoutData struct {
	Layout agentconfig.Layout
	Users  UserLookup
	// Send queues text into a session as userID (optional; nil answers
	// Unimplemented until the agent pool is wired in).
	Send func(ctx context.Context, userID, sessionID, text string) (bool, error)
}

var (
	_ SessionService = (*LayoutData)(nil)
	_ TicketService  = (*LayoutData)(nil)
)

// asUser puts userID on ctx as the signed-in person for team.Check*. An
// empty userID is refused here too: the team checks skip their per-user
// filter for "", so this adapter must never be reached without a person.
func (l *LayoutData) asUser(ctx context.Context, userID string) (context.Context, *entity.User, error) {
	if userID == "" {
		return ctx, nil, status.Error(codes.FailedPrecondition, "no acting user")
	}
	if l.Users == nil {
		return ctx, nil, nil
	}
	u, tags, err := l.Users(ctx, userID)
	if err != nil || u == nil {
		return ctx, nil, status.Error(codes.FailedPrecondition, "acting user not found")
	}
	if !u.Approved {
		return ctx, nil, status.Error(codes.FailedPrecondition, "acting user is not approved")
	}
	return login.WithUser(ctx, u, tags), u, nil
}

func limitOf(n int32) int {
	if n <= 0 {
		return defaultListLimit
	}
	return min(int(n), maxListLimit)
}

func ts(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func sessionMeta(id string, m session.Meta) *hostpb.SessionMeta {
	return &hostpb.SessionMeta{Id: id, ProjectId: m.ProjectID, TicketId: m.TicketID, Label: m.Label,
		Channel: string(m.Origin), OwnerUserId: m.UserID, CreatedAt: ts(m.CreatedAt), UpdatedAt: ts(m.LastActive),
		Extra: map[string]string{"status": string(m.Status)}}
}

// ListSessions implements SessionService: top-level sessions userID may
// see, most recently active first.
func (l *LayoutData) ListSessions(ctx context.Context, userID string, in *hostpb.ListSessionsRequest) ([]*hostpb.SessionMeta, error) {
	ctx, _, err := l.asUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	var since time.Time
	if s := strings.TrimSpace(in.GetSince()); s != "" {
		if since, err = time.Parse(time.RFC3339, s); err != nil {
			return nil, status.Error(codes.InvalidArgument, "since must be RFC3339")
		}
	}
	ids, err := session.List(l.Layout)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "list sessions: %v", err)
	}
	type row struct {
		id string
		m  session.Meta
	}
	var rows []row
	for _, id := range ids {
		sess, err := session.Load(l.Layout, id)
		if err != nil {
			continue
		}
		m := sess.Meta
		if p := in.GetProjectId(); p != "" && m.ProjectID != p {
			continue
		}
		if !since.IsZero() && m.LastActive.Before(since) {
			continue
		}
		if team.CheckSessionTarget(ctx, l.Layout, userID, "", id) != nil {
			continue
		}
		rows = append(rows, row{id, m})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].m.LastActive.After(rows[j].m.LastActive) })
	out := make([]*hostpb.SessionMeta, 0, min(len(rows), limitOf(in.GetLimit())))
	for _, r := range rows {
		if len(out) >= limitOf(in.GetLimit()) {
			break
		}
		out = append(out, sessionMeta(r.id, r.m))
	}
	return out, nil
}

// GetSessionMeta implements SessionService.
func (l *LayoutData) GetSessionMeta(ctx context.Context, userID, id string) (*hostpb.SessionMeta, error) {
	ctx, _, err := l.asUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := team.CheckSessionTarget(ctx, l.Layout, userID, "", id); err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	sess, err := session.Load(l.Layout, id)
	if err != nil {
		return nil, status.Error(codes.NotFound, "session not found: "+id)
	}
	return sessionMeta(id, sess.Meta), nil
}

// SendToSession implements SessionService.
func (l *LayoutData) SendToSession(ctx context.Context, userID, sessionID, text string) (bool, error) {
	if l.Send == nil {
		return false, unavailable("SendToSession")
	}
	ctx, _, err := l.asUser(ctx, userID)
	if err != nil {
		return false, err
	}
	if err := team.CheckSessionTarget(ctx, l.Layout, userID, "", sessionID); err != nil {
		return false, status.Error(codes.NotFound, err.Error())
	}
	return l.Send(ctx, userID, sessionID, text)
}

func pbTicket(t ticket.Ticket) *hostpb.Ticket {
	return &hostpb.Ticket{Id: t.ID, ProjectId: t.ProjectID, Title: t.Title, Body: t.Body, Status: t.Status,
		Assignees: t.AssigneeList(), Fields: t.Fields, CreatedAt: ts(t.CreatedAt), UpdatedAt: ts(t.UpdatedAt)}
}

// findTicket locates ticket id for userID. With projectID it looks only
// there. Without one it searches every project userID may access, because
// ticket ids are unique per project only; more than one match is refused
// rather than guessed (a write to the wrong project's ticket would succeed
// silently).
func (l *LayoutData) findTicket(ctx context.Context, userID, projectID, id string) (ticket.Ticket, error) {
	id = strings.TrimSpace(id)
	notFound := status.Error(codes.NotFound, "ticket not found: "+id)
	if projectID = strings.TrimSpace(projectID); projectID != "" {
		if err := l.checkProject(ctx, userID, projectID); err != nil {
			return ticket.Ticket{}, err
		}
		tk, err := ticket.Load(l.Layout, projectID, id)
		if err != nil {
			return ticket.Ticket{}, notFound
		}
		return tk, nil
	}
	projects, err := project.List(l.Layout)
	if err != nil {
		return ticket.Ticket{}, status.Errorf(codes.Unavailable, "list projects: %v", err)
	}
	var found []string
	for _, p := range projects {
		if ticket.Exists(l.Layout, p, id) && team.CheckProjectTarget(ctx, l.Layout, userID, "", p) == nil {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return ticket.Ticket{}, notFound
	case 1:
		tk, err := ticket.Load(l.Layout, found[0], id)
		if err != nil {
			return ticket.Ticket{}, notFound
		}
		return tk, nil
	}
	return ticket.Ticket{}, status.Errorf(codes.FailedPrecondition,
		"ticket id ambiguous (found in projects %s); pass project_id", strings.Join(found, ", "))
}

// checkProject requires projectID to be reachable by userID and to have its
// ticket REST API turned on — the same toggle a PAT request to the ticket
// API must pass, since a plugin is machine access too.
func (l *LayoutData) checkProject(ctx context.Context, userID, projectID string) error {
	if strings.TrimSpace(projectID) == "" {
		return status.Error(codes.InvalidArgument, "project_id is required")
	}
	if err := team.CheckProjectTarget(ctx, l.Layout, userID, "", projectID); err != nil {
		return status.Error(codes.NotFound, err.Error())
	}
	p, err := project.Load(l.Layout, projectID)
	if err != nil {
		return status.Error(codes.NotFound, "project not found: "+projectID)
	}
	if !p.Meta.Ticket.Integrations.APIEnabled {
		return status.Error(codes.FailedPrecondition, "the ticket API is turned off for project "+projectID)
	}
	return nil
}

func actorOf(u *entity.User, userID string) ticket.Actor {
	if u == nil {
		return ticket.Actor{Type: ticket.ActorAPI, ID: userID}
	}
	return ticket.Actor{Type: ticket.ActorAPI, ID: u.ID, Name: u.Name, Email: u.Email}
}

// GetTicket implements TicketService.
func (l *LayoutData) GetTicket(ctx context.Context, userID string, in *hostpb.GetTicketRequest) (*hostpb.Ticket, error) {
	ctx, _, err := l.asUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	tk, err := l.ticketFor(ctx, userID, in.GetProjectId(), in.GetId())
	if err != nil {
		return nil, err
	}
	return pbTicket(tk), nil
}

// ticketFor is findTicket plus the project checks of the project it was
// found in (the API toggle applies to a found ticket too).
func (l *LayoutData) ticketFor(ctx context.Context, userID, projectID, id string) (ticket.Ticket, error) {
	tk, err := l.findTicket(ctx, userID, projectID, id)
	if err != nil {
		return ticket.Ticket{}, err
	}
	if projectID == "" {
		if err := l.checkProject(ctx, userID, tk.ProjectID); err != nil {
			return ticket.Ticket{}, err
		}
	}
	return tk, nil
}

// ListTickets implements TicketService: a project's tickets, newest first.
func (l *LayoutData) ListTickets(ctx context.Context, userID string, in *hostpb.ListTicketsRequest) ([]*hostpb.Ticket, error) {
	ctx, _, err := l.asUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := l.checkProject(ctx, userID, in.GetProjectId()); err != nil {
		return nil, err
	}
	all, err := ticket.List(l.Layout, in.GetProjectId())
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "list tickets: %v", err)
	}
	out := []*hostpb.Ticket{}
	for _, tk := range all {
		if s := in.GetStatus(); s != "" && tk.Status != s {
			continue
		}
		if len(out) >= limitOf(in.GetLimit()) {
			break
		}
		out = append(out, pbTicket(tk))
	}
	return out, nil
}

// CreateTicket implements TicketService.
func (l *LayoutData) CreateTicket(ctx context.Context, userID string, in *hostpb.CreateTicketRequest) (*hostpb.Ticket, error) {
	ctx, u, err := l.asUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := l.checkProject(ctx, userID, in.GetProjectId()); err != nil {
		return nil, err
	}
	tk, err := ticket.Create(l.Layout, ticket.CreateOptions{
		ProjectID: in.GetProjectId(), ID: strings.TrimSpace(in.GetId()), Title: in.GetTitle(), Body: in.GetBody(),
		Status: strings.TrimSpace(in.GetStatus()), Assignees: in.GetAssignees(), Fields: in.GetFields(),
		Actor: actorOf(u, userID),
	})
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return pbTicket(tk), nil
}

// UpdateTicket implements TicketService. Only set fields change; a field
// sent empty is removed; if_updated_at guards against a concurrent edit.
func (l *LayoutData) UpdateTicket(ctx context.Context, userID string, in *hostpb.UpdateTicketRequest) (*hostpb.Ticket, error) {
	ctx, u, err := l.asUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	tk, err := l.ticketFor(ctx, userID, in.GetProjectId(), in.GetId())
	if err != nil {
		return nil, err
	}
	if want := in.GetIfUpdatedAt(); want != "" && want != ts(tk.UpdatedAt) {
		return nil, status.Error(codes.Aborted, "ticket changed since "+want)
	}
	if in.Status != nil {
		p, err := project.Load(l.Layout, tk.ProjectID)
		if err != nil {
			return nil, status.Errorf(codes.Unavailable, "load project: %v", err)
		}
		if !ticket.ValidStatus(p.Meta.Ticket, in.GetStatus()) {
			return nil, status.Errorf(codes.InvalidArgument, "invalid status %q", in.GetStatus())
		}
		tk.Status = in.GetStatus()
	}
	if in.Title != nil {
		if strings.TrimSpace(in.GetTitle()) == "" {
			return nil, status.Error(codes.InvalidArgument, "title cannot be empty")
		}
		tk.Title = strings.TrimSpace(in.GetTitle())
	}
	if in.Body != nil {
		tk.Body = strings.TrimSpace(in.GetBody())
	}
	if in.GetSetAssignees() || len(in.GetAssignees()) > 0 {
		tk.SetAssignees(in.GetAssignees())
	}
	for k, v := range in.GetFields() {
		if tk.Fields == nil {
			tk.Fields = map[string]string{}
		}
		if strings.TrimSpace(v) == "" {
			delete(tk.Fields, k)
			continue
		}
		tk.Fields[k] = v
	}
	saved, err := ticket.SaveAsAt(l.Layout, tk, actorOf(u, userID), time.Time{})
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "save ticket: %v", err)
	}
	return pbTicket(saved), nil
}

// AttachSession implements TicketService. The acting user must reach both
// the ticket and the session.
func (l *LayoutData) AttachSession(ctx context.Context, userID string, in *hostpb.AttachSessionRequest) error {
	ctx, _, err := l.asUser(ctx, userID)
	if err != nil {
		return err
	}
	sessionID := strings.TrimSpace(in.GetSessionId())
	tk, err := l.ticketFor(ctx, userID, in.GetProjectId(), in.GetTicketId())
	if err != nil {
		return err
	}
	if err := team.CheckSessionTarget(ctx, l.Layout, userID, "", sessionID); err != nil {
		return status.Error(codes.NotFound, err.Error())
	}
	if err := team.RefuseRoomSession(l.Layout, sessionID); err != nil {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	if err := ticket.AttachSession(l.Layout, tk.ProjectID, tk.ID, sessionID); err != nil {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	if sess, err := session.Load(l.Layout, sessionID); err == nil && sess.Meta.TicketID != tk.ID {
		sess.Meta.TicketID = tk.ID
		_ = session.SaveMeta(l.Layout, sessionID, sess.Meta)
	}
	return nil
}
