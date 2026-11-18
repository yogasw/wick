package api

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/accessreq"
	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	slackch "github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	mcphandlers "github.com/yogasw/wick/internal/mcp/handlers"
	agentstool "github.com/yogasw/wick/internal/tools/agents"
)

// accessCatalog is accessreq.Catalog over the connectors service.
type accessCatalog struct {
	svc   *connectors.Service
	users *login.Service
}

func (a accessCatalog) Describe(ctx context.Context, connectorID, opKey string) (accessreq.ActionMeta, error) {
	info, err := a.svc.DescribeOp(ctx, connectorID, opKey)
	if err != nil {
		return accessreq.ActionMeta{}, err
	}
	return accessreq.ActionMeta{ConnectorKey: info.ConnectorKey, ConnectorLabel: info.ConnectorLabel, OpName: info.OpName, Destructive: info.Destructive}, nil
}

// Requestable: an agent may only ask for a connector already in its
// access list; outside an agent, the session's owner must see it.
func (a accessCatalog) Requestable(ctx context.Context, sessionID, _ string, connectorID string) (bool, string) {
	if scope := connectors.AgentScopeFrom(ctx); scope != nil {
		if !scope.AllowConnector(connectorID) {
			return false, "this connector is not in the agent's access at all; its owner has to add it (at least read) before actions on it can be requested"
		}
		return true, ""
	}
	owner := agentstool.SessionUserOf(sessionID)
	if owner == "" {
		return false, "the session has no owner"
	}
	u, err := a.users.GetUserByID(ctx, owner)
	if err != nil || u == nil {
		return false, "the session's owner is unknown"
	}
	ok, err := a.svc.IsVisibleTo(ctx, connectorID, u.ID, a.users.GetUserFilterTagIDs(ctx, u.ID), u.IsAdmin())
	if err != nil || !ok {
		return false, "this connector is not in your access"
	}
	return true, ""
}

func (a accessCatalog) CanRun(ctx context.Context, userID, connectorID, opKey string) (bool, string) {
	return a.svc.CanRunOwn(ctx, userID, connectorID, opKey)
}

func (a accessCatalog) Run(ctx context.Context, userID, connectorID, opKey, accountID string, params map[string]any) (string, string, error) {
	return a.svc.RunOwn(ctx, userID, connectorID, opKey, accountID, params)
}

// accessDirectory is accessreq.Directory over users, sessions and the Team.
type accessDirectory struct {
	users *login.Service
	team  *team.Service
}

func personOf(u *entity.User) accessreq.Person {
	name := u.Name
	if name == "" {
		name = u.Email
	}
	return accessreq.Person{ID: u.ID, Name: name, IsAdmin: u.IsAdmin(), Approved: u.Approved}
}

func (d accessDirectory) User(ctx context.Context, id string) (accessreq.Person, bool) {
	if id == "" {
		return accessreq.Person{}, false
	}
	u, err := d.users.GetUserByID(ctx, id)
	if err != nil || u == nil {
		return accessreq.Person{}, false
	}
	return personOf(u), true
}

func (d accessDirectory) Admins(ctx context.Context) []accessreq.Person {
	us, err := d.users.ListApprovedAdmins(ctx)
	if err != nil {
		return nil
	}
	out := make([]accessreq.Person, 0, len(us))
	for _, u := range us {
		if u != nil {
			out = append(out, personOf(u))
		}
	}
	return out
}

func (d accessDirectory) SessionOwner(sessionID string) string {
	return agentstool.SessionUserOf(sessionID)
}

func (d accessDirectory) AgentOwner(ctx context.Context, agentID string) string {
	if d.team == nil || agentID == "" {
		return ""
	}
	p, err := d.team.Get(ctx, agentID)
	if err != nil {
		return ""
	}
	return p.OwnerUserID
}

func (d accessDirectory) AgentName(ctx context.Context, agentID string) string {
	if d.team == nil || agentID == "" {
		return ""
	}
	p, err := d.team.Get(ctx, agentID)
	if err != nil || p.Handle == "" {
		return ""
	}
	return "@" + p.Handle
}

// accessPublisher draws a request on whichever chat channel owns its
// session (a Slack thread today; any channel that implements the method
// later).
type accessPublisher struct{ reg *agentchannels.Registry }

type accessCardPublisher interface {
	OwnsSession(sessionID string) bool
	PublishAccessRequest(ctx context.Context, v accessreq.View) (map[string]string, error)
}

func (p accessPublisher) Publish(ctx context.Context, v accessreq.View) (map[string]string, error) {
	if p.reg == nil {
		return nil, nil
	}
	for _, c := range p.reg.Channels() {
		if cp, ok := c.(accessCardPublisher); ok && cp.OwnsSession(v.SessionID) {
			return cp.PublishAccessRequest(ctx, v)
		}
	}
	return nil, nil
}

// wireAccessRequests builds the access-request service and connects it
// to the connector hook, the MCP tool, the Slack cards, the web page and
// the session wake-up.
func wireAccessRequests(db *gorm.DB, svc *connectors.Service, users *login.Service, teamSvc *team.Service,
	reg *agentchannels.Registry, deliver func(ctx context.Context, sessionID, text string) error, appURL func() string) *accessreq.Service {
	ar := accessreq.New(accessreq.NewStore(db), accessCatalog{svc: svc, users: users}, accessDirectory{users: users, team: teamSvc})
	ar.AddPublisher(accessPublisher{reg: reg})
	ar.AddPublisher(agentstool.AccessWebPublisher{})
	ar.SetWaker(accessreq.Waker(deliver))
	ar.SetURL(func(id string) string {
		base := strings.TrimRight(appURL(), "/")
		if base == "" {
			return ""
		}
		return base + "/tools/agents/access/" + id
	})
	svc.SetAccessCheck(func(ctx context.Context, q connectors.AccessQuery) (string, string, error) {
		return ar.Check(ctx, accessreq.Action{
			SessionID: q.SessionID, AgentID: q.AgentID, ConnectorID: q.ConnectorID, ConnectorKey: q.ConnectorKey,
			OpKey: q.OpKey, Kind: accessreq.ClassifyOp(q.OpKey, q.Destructive), Params: q.Input, Config: q.Config,
		})
	})
	mcphandlers.AccessRequests = ar
	slackch.AccessRequests = ar
	agentstool.SetAccessRequests(ar)
	return ar
}
