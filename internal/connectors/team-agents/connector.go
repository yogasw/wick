// Package teamagents exposes the Captain's agents.* ops — list, create,
// update_persona, set_access, schedule — as a fixed, single-instance connector.
//
// Every op runs on behalf of the OWNER of the calling agent and only ever
// touches that owner's agents. The scope (team.ManageAgentsKey) shows the
// connector only to an agent's own chat with "Manage other agents" on;
// each op checks it again, and set_access never changes access itself —
// it files an approval card the owner accepts or declines.
package teamagents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// Key is the connector definition slug.
const Key = team.ManageAgentsKey

// Configs is empty: the connector drives in-process wick services.
type Configs struct{}

// CreateInput is agents.create's input, parsed.
type CreateInput struct {
	Name, Handle, Tagline, Description, SystemPrompt string
	// Avatar is a JSON team.Avatar, "" for the handle's default.
	Avatar string
	// AccessSuggestions become one approval request, never a grant.
	AccessSuggestions []team.ConnectorGrant
}

// PersonaInput is agents.update_persona's input. nil = leave as is.
type PersonaInput struct {
	Agent                                    string
	Name, Tagline, Description, SystemPrompt *string
}

// AccessInput is agents.set_access's input: the agent's whole proposed
// checklist.
type AccessInput struct {
	Agent  string
	Grants []team.ConnectorGrant
	Reason string
}

// ScheduleInput is agents.schedule's input: one action on another
// agent's Scheduled drawer.
type ScheduleInput struct {
	Agent string
	// Action is list | create | update | pause | resume.
	Action     string
	ScheduleID string
	// One of RunAt (one-shot), Every or Cron; update leaves all three
	// empty to keep the time.
	RunAt, Every, Cron string
	Message            string
}

// Ops is what the connector drives; implemented in internal/tools/agents.
type Ops interface {
	List(ctx context.Context, sessionID string) (any, error)
	Create(ctx context.Context, sessionID string, in CreateInput) (any, error)
	UpdatePersona(ctx context.Context, sessionID string, in PersonaInput) (any, error)
	SetAccess(ctx context.Context, sessionID string, in AccessInput) (any, error)
	Schedule(ctx context.Context, sessionID string, in ScheduleInput) (any, error)
}

// Deps wires the connector to its implementation. Ops is late-bound.
type Deps struct {
	Ops func() Ops
}

var errUnavailable = errors.New("team management is not configured on this server")

// Meta returns the static metadata block for the registry.
func Meta() connector.Meta {
	return connector.Meta{
		Key:  Key,
		Name: "Team agents",
		Description: "Manage the other agents of your owner's Team: see what each is doing, create one, " +
			"edit its persona and propose access changes for the owner to approve.",
		Icon:  "🧭",
		Fixed: true,
	}
}

// Module returns the fully-wired connector.Module. Platform-tagged so the
// owner's catalog carries it; the agent scope then hides it from every
// agent without "Manage other agents".
func Module(deps Deps) connector.Module {
	m := Meta()
	m.DefaultTags = []tool.DefaultTag{tags.Connector, tags.Platform}
	return connector.Module{Meta: m, Operations: Operations(deps)}
}

type listInput struct{}

type createInput struct {
	Name              string `wick:"required;desc=Display name of the new agent."`
	Handle            string `wick:"desc=@handle, lowercase-kebab. Derived from the name when empty."`
	Tagline           string `wick:"desc=Short label, e.g. The Critic."`
	Description       string `wick:"desc=One sentence on what the agent is for."`
	SystemPrompt      string `wick:"textarea;desc=The agent's persona and focus. Do not put its name in it."`
	Avatar            string `wick:"desc=Optional JSON {kind,shape,color,expression}."`
	AccessSuggestions string `wick:"textarea;desc=Optional JSON array of grants [{connector_id,level:read|all|pick|off,ops?,accounts?}]. Becomes an approval request to the owner, never a grant."`
}

type personaInput struct {
	Agent        string `wick:"required;desc=The agent's @handle or id."`
	Name         string `wick:"desc=New display name, empty = unchanged."`
	Tagline      string `wick:"desc=New tagline, empty = unchanged."`
	Description  string `wick:"desc=New short description, empty = unchanged."`
	SystemPrompt string `wick:"textarea;desc=New system prompt, empty = unchanged."`
}

type accessInput struct {
	Agent  string `wick:"required;desc=The agent's @handle or id."`
	Grants string `wick:"required;textarea;desc=JSON array: the agent's WHOLE new connector checklist [{connector_id,level:read|all|pick|off,ops?,accounts?}]. Only connectors the owner can use."`
	Reason string `wick:"desc=Why, shown to the owner on the approval card."`
}

type scheduleInput struct {
	Agent      string `wick:"required;desc=The agent's @handle or id."`
	Action     string `wick:"required;desc=list | create | update | pause | resume."`
	ScheduleID string `wick:"desc=The schedule id (update, pause, resume). From action=list."`
	RunAt      string `wick:"desc=One-shot fire time: RFC3339 or an offset like +2h."`
	Every      string `wick:"desc=Repeat interval, e.g. 30m, 1h30m."`
	Cron       string `wick:"desc=5-field cron (min hour dom mon dow) in the server time zone, e.g. 0 9 * * 1-5."`
	Message    string `wick:"textarea;desc=What the agent is told when it fires (create, update = new text, empty = unchanged)."`
}

// Operations lists the connector's ops.
func Operations(deps Deps) []connector.Category {
	return []connector.Category{
		connector.Cat("Agents", "Manage the other agents of the Team.",
			connector.Op("list", "List Team Agents",
				"Status of every agent of the Team: id, handle, name, tagline, description, remote (with its agent_description), captain, disabled, status (idle or working), current_action, needs_attention, unread, last_error.",
				listInput{}, deps.list, wickdocs.Docs{}),
			connector.OpDestructive("create", "Create an Agent",
				"Create a new agent with NO access (deny by default). access_suggestions are sent to the owner as one approval request; nothing is granted until they accept.",
				createInput{}, deps.create, wickdocs.Docs{}),
			connector.OpDestructive("update_persona", "Edit an Agent's Persona",
				"Change another agent's name, tagline, description or system prompt. For a remote agent the system prompt is its agent description (what it does, when to call it); nothing is sent to the remote. Refused when that agent does not let the Captain edit its persona.",
				personaInput{}, deps.updatePersona, wickdocs.Docs{}),
			connector.OpDestructive("set_access", "Propose an Access Change",
				"Ask the owner to change another agent's connector access. ALWAYS returns pending_approval: the owner accepts or declines on a card, and the change applies only then. Can never exceed the owner's own access. Refused when that agent does not let the Captain propose access changes.",
				accessInput{}, deps.setAccess, wickdocs.Docs{}),
			connector.OpDestructive("schedule", "Manage an Agent's Schedules",
				"List, create, update, pause or resume the schedules in another agent's Scheduled drawer. New schedules fire into that agent's main chat. Applies at once and is announced in that agent's chat. Refused when that agent does not let the Captain manage its routines.",
				scheduleInput{}, deps.schedule, wickdocs.Docs{}),
		),
	}
}

func (d Deps) ops() (Ops, error) {
	if d.Ops == nil || d.Ops() == nil {
		return nil, errUnavailable
	}
	return d.Ops(), nil
}

func (d Deps) list(c *connector.Ctx) (any, error) {
	ops, err := d.ops()
	if err != nil {
		return nil, err
	}
	return ops.List(c.Context(), c.SessionID())
}

func (d Deps) create(c *connector.Ctx) (any, error) {
	ops, err := d.ops()
	if err != nil {
		return nil, err
	}
	sugg, err := ParseGrants(c.Input("access_suggestions"))
	if err != nil {
		return nil, err
	}
	return ops.Create(c.Context(), c.SessionID(), CreateInput{
		Name: strings.TrimSpace(c.Input("name")), Handle: c.Input("handle"),
		Tagline: c.Input("tagline"), Description: c.Input("description"),
		SystemPrompt: c.Input("system_prompt"), Avatar: strings.TrimSpace(c.Input("avatar")),
		AccessSuggestions: sugg,
	})
}

func (d Deps) updatePersona(c *connector.Ctx) (any, error) {
	ops, err := d.ops()
	if err != nil {
		return nil, err
	}
	opt := func(k string) *string {
		if v := c.Input(k); strings.TrimSpace(v) != "" {
			return &v
		}
		return nil
	}
	return ops.UpdatePersona(c.Context(), c.SessionID(), PersonaInput{
		Agent: c.Input("agent"), Name: opt("name"), Tagline: opt("tagline"),
		Description: opt("description"), SystemPrompt: opt("system_prompt"),
	})
}

func (d Deps) setAccess(c *connector.Ctx) (any, error) {
	ops, err := d.ops()
	if err != nil {
		return nil, err
	}
	gs, err := ParseGrants(c.Input("grants"))
	if err != nil {
		return nil, err
	}
	return ops.SetAccess(c.Context(), c.SessionID(), AccessInput{Agent: c.Input("agent"), Grants: gs, Reason: c.Input("reason")})
}

func (d Deps) schedule(c *connector.Ctx) (any, error) {
	ops, err := d.ops()
	if err != nil {
		return nil, err
	}
	return ops.Schedule(c.Context(), c.SessionID(), ScheduleInput{
		Agent: c.Input("agent"), Action: strings.TrimSpace(strings.ToLower(c.Input("action"))),
		ScheduleID: strings.TrimSpace(c.Input("schedule_id")), RunAt: strings.TrimSpace(c.Input("run_at")),
		Every: strings.TrimSpace(c.Input("every")), Cron: strings.TrimSpace(c.Input("cron")), Message: c.Input("message"),
	})
}

// ParseGrants reads a JSON grant array; "" is none.
func ParseGrants(raw string) ([]team.ConnectorGrant, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var gs []team.ConnectorGrant
	if err := json.Unmarshal([]byte(raw), &gs); err != nil {
		return nil, errors.New("grants must be a JSON array of {connector_id, level, ops?, accounts?}: " + err.Error())
	}
	return gs, nil
}
