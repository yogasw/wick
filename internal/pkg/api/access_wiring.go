package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/accessreq"
	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	slackch "github.com/yogasw/wick/internal/agents/channels/slack"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/channelidentity"
	"github.com/yogasw/wick/internal/codemode"
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

func (a accessCatalog) Describe(ctx context.Context, connectorID, opKey string, override map[string]string) (accessreq.ActionMeta, error) {
	info, err := a.svc.DescribeOpWith(ctx, connectorID, opKey, override)
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

func (a accessCatalog) CanRun(ctx context.Context, userID, connectorID, opKey string, override map[string]string) (bool, string) {
	return a.svc.CanRunOwnWith(ctx, userID, connectorID, opKey, override)
}

func (a accessCatalog) Run(ctx context.Context, userID, connectorID, opKey, accountID string, params map[string]any) (string, string, error) {
	return a.svc.RunOwn(ctx, userID, connectorID, opKey, accountID, params)
}

// accessDirectory is accessreq.Directory over users, sessions and the Team.
type accessDirectory struct {
	users  *login.Service
	team   *team.Service
	ids    *channelidentity.Store
	layout agentconfig.Layout
}

// SessionProject names a session's project, for project-scoped rules and
// the project's request history.
func (d accessDirectory) SessionProject(sessionID string) string {
	return auditPlaces{layout: d.layout}.SessionProject(sessionID)
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
	reg *agentchannels.Registry, deliver func(ctx context.Context, sessionID, text string) error, _ func() string,
	layout agentconfig.Layout) *accessreq.Service {
	ar := accessreq.New(accessreq.NewStore(db), accessCatalog{svc: svc, users: users}, accessDirectory{users: users, team: teamSvc, ids: channelidentity.NewStore(db), layout: layout})
	ar.AddPublisher(accessPublisher{reg: reg})
	ar.AddPublisher(agentstool.AccessWebPublisher{})
	ar.AddPublisher(agentstool.AccessFilePublisher{Layout: layout, Project: auditPlaces{layout: layout}.SessionProject})
	go pruneAccessRequests(ar)
	ar.SetWaker(accessreq.Waker(deliver))
	svc.SetAccessCheck(func(ctx context.Context, q connectors.AccessQuery) (string, string, error) {
		return ar.Check(ctx, accessreq.Action{
			SessionID: q.SessionID, AgentID: q.AgentID, ConnectorID: q.ConnectorID, ConnectorKey: q.ConnectorKey,
			OpKey: q.OpKey, Kind: accessreq.ClassifyOp(q.OpKey, q.Destructive), Params: q.Input, Config: q.Config,
		})
	})
	ar.SetScriptRunner(func(ctx context.Context, ownerID, code string, input map[string]any) (json.RawMessage, []string, error) {
		u, err := users.GetUserByID(ctx, ownerID)
		if err != nil || u == nil {
			return nil, nil, fmt.Errorf("rule owner not found")
		}
		tags := users.GetUserFilterTagIDs(ctx, u.ID)
		raw, _ := json.Marshal(input)
		// Read-only, and never routed back into the access check.
		sctx := connectors.WithoutAccessCheck(connectors.WithAgentScope(ctx, connectors.ReadOnlyScope{}))
		res, err := mcphandlers.RunCodeAs(sctx, svc, layout, codemode.Request{Lang: "ts", Code: code, Input: raw, Timeout: 10 * time.Second}, "", u, tags)
		if err != nil {
			return nil, nil, err
		}
		if !res.OK {
			return nil, res.Logs, fmt.Errorf("script failed: %s", res.Error)
		}
		return res.Result, res.Logs, nil
	})
	mcphandlers.AccessRequests = ar
	slackch.AccessRequests = ar
	agentstool.SetAccessRequests(ar)
	return ar
}

// SubjectConfig reads a connector's non-secret subject settings (app_code
// and the like), so rules see them when a request is filed too.
func (a accessCatalog) SubjectConfig(ctx context.Context, connectorID string) map[string]string {
	return a.svc.SubjectConfig(ctx, connectorID)
}

// Identities names a person's email and chat accounts for approver
// scripts: {"email": …, "slack_id": …} (one per channel type).
func (d accessDirectory) Identities(ctx context.Context, userID string) map[string]string {
	out := map[string]string{}
	if u, err := d.users.GetUserByID(ctx, userID); err == nil && u != nil {
		out["email"] = u.Email
	}
	if d.ids != nil {
		if rows, err := d.ids.ListForUser(ctx, userID); err == nil {
			for _, r := range rows {
				k := r.ChannelType + "_id"
				if _, seen := out[k]; !seen {
					out[k] = r.ExternalUserID
				}
			}
		}
	}
	return out
}

// pruneAccessRequests drops finished requests past their retention once
// a day; their history stays in the sessions' files.
func pruneAccessRequests(ar *accessreq.Service) {
	for {
		if n, err := ar.PruneFinished(context.Background()); err != nil {
			log.Warn().Err(err).Msg("accessreq: prune failed")
		} else if n > 0 {
			log.Info().Int64("requests", n).Msg("accessreq: pruned finished requests")
		}
		time.Sleep(24 * time.Hour)
	}
}
