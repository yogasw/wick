// Package teamagents exposes the Captain's agents.* ops — list, create,
// update_persona, set_access, schedule, folders — as a fixed, single-instance connector.
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
	"strconv"
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
	// AccessSuggestions and NativeTools become one approval request,
	// never a grant. NativeTools nil = the read-only defaults stay.
	AccessSuggestions []team.ConnectorGrant
	NativeTools       []string
	// Provider is "type/name" (e.g. claude/claude_waba), "" = the
	// server default. Model pins a model of that provider.
	Provider, Model string
	// Folder files the new agent into the owner's Team-sidebar folder of
	// that name (made when missing); "" = loose.
	Folder string
}

// PersonaInput is agents.update_persona's input. nil = leave as is.
// Avatar is a JSON team.Avatar (kind, shape, color, expression);
// AvatarImage a picture file path relative to the caller's working
// folder, which becomes the agent's image avatar.
type PersonaInput struct {
	Agent                                    string
	Name, Tagline, Description, SystemPrompt *string
	Avatar, AvatarImage                      *string
	// Provider "default" clears the agent's provider (server default).
	Provider, Model *string
	// ForceDefaultProvider "on"/"off" holds every chat of the agent on
	// its default provider (team.SessionPolicy.ForceDefaultProvider).
	ForceDefaultProvider *string
}

// AccessInput is agents.set_access's input: the agent's whole proposed
// connector checklist and/or native-tool list. nil = leave that part as
// it is; at least one is set.
type AccessInput struct {
	Agent       string
	Grants      []team.ConnectorGrant
	NativeTools []string
	Reason      string
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

// FoldersInput is agents.folders's input: one action on the owner's
// Team-sidebar folders.
type FoldersInput struct {
	// Action is list | move | rename | delete | order.
	Action string
	// Agent is the @handle or id to move.
	Agent string
	// Folder is the folder name; for move "" takes the agent out of any.
	Folder  string
	NewName string
	// Order is folder names, first to last.
	Order []string
}

// DeleteInput is agents.delete's input.
type DeleteInput struct {
	// Agent is the @handle or id to remove.
	Agent string
	// Confirm must repeat the agent's handle: a typo never removes the
	// wrong agent.
	Confirm string
	// Mode is delete (gone for good) or disable (kept, turned off).
	Mode string
	// PurgeChats deletes the agent's project with its chats; false keeps
	// the project as an ordinary one (delete only).
	PurgeChats bool
}

// Ops is what the connector drives; implemented in internal/tools/agents.
type Ops interface {
	List(ctx context.Context, sessionID string) (any, error)
	Create(ctx context.Context, sessionID string, in CreateInput) (any, error)
	UpdatePersona(ctx context.Context, sessionID string, in PersonaInput) (any, error)
	SetAccess(ctx context.Context, sessionID string, in AccessInput) (any, error)
	Schedule(ctx context.Context, sessionID string, in ScheduleInput) (any, error)
	Folders(ctx context.Context, sessionID string, in FoldersInput) (any, error)
	Delete(ctx context.Context, sessionID string, in DeleteInput) (any, error)
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
			"edit its persona, propose access changes for the owner to approve, sort agents into sidebar folders and, when the owner asks, delete or turn one off.",
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
	Provider          string `wick:"desc=Optional default provider instance as type/name, e.g. claude/claude_waba. Must be one the owner may use. Empty = the server default."`
	Model             string `wick:"desc=Optional default model of that provider, e.g. opus. Empty = the provider default."`
	NativeTools       string `wick:"desc=Optional native tools the agent should have, comma-separated or a JSON array, from Read, Grep, Glob, WebFetch, WebSearch, Bash, Edit, Write. The WHOLE list (a new agent has only the read-only five). Joins the same approval request as access_suggestions."`
	Folder            string `wick:"desc=Optional sidebar folder name to file the new agent in, created when missing. Empty = no folder."`
}

type personaInput struct {
	Agent                string `wick:"required;desc=The agent's @handle or id."`
	Name                 string `wick:"desc=New display name, empty = unchanged."`
	Tagline              string `wick:"desc=New tagline, empty = unchanged."`
	Description          string `wick:"desc=New short description, empty = unchanged."`
	SystemPrompt         string `wick:"textarea;desc=New system prompt, empty = unchanged."`
	Avatar               string `wick:"desc=New avatar as JSON {kind,shape,color,expression}: kind \"\" (classic: shape circle|squircle|triangle|diamond) or \"blob\" (shape circle|pebble|squircle|capsule|triangle|cloud|droplet|flame|medal|acorn|jellyfish|clover, expression neutral|attentive|surprised|excited|happy|angry|sad|suspicious|curious|proud|shy|unimpressed), color a #rrggbb hex. Empty = unchanged."`
	Provider             string `wick:"desc=New default provider instance as type/name, e.g. claude/claude_waba (must be one the owner may use), or default to fall back to the server default. Changing it drops the model pin unless model is given. Empty = unchanged."`
	Model                string `wick:"desc=New default model of the agent's provider, e.g. opus or sonnet. Empty = unchanged."`
	AvatarImage          string `wick:"desc=Path of a PNG or JPEG (max 2 MiB) inside your working folder, e.g. artifacts/avatar.png, it becomes the agent's picture avatar. Empty = unchanged."`
	ForceDefaultProvider string `wick:"desc=on or off. on = keep every chat of the agent on its default provider and move the ones that are not (a chat on a failover stand-in is left alone). Empty = unchanged."`
}

type accessInput struct {
	Agent       string `wick:"required;desc=The agent's @handle or id."`
	Grants      string `wick:"textarea;desc=JSON array: the agent's WHOLE new connector checklist [{connector_id,level:read|all|pick|off,ops?,accounts?}]. Only connectors the owner can use. Empty = keep the connectors as they are."`
	NativeTools string `wick:"desc=The agent's WHOLE new native-tool list, comma-separated or a JSON array, from Read, Grep, Glob, WebFetch, WebSearch, Bash, Edit, Write. Empty = keep the tools as they are. Give grants, native_tools or both."`
	Reason      string `wick:"desc=Why, shown to the owner on the approval card."`
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

type foldersInput struct {
	Action  string `wick:"required;desc=list | move | rename | delete | order."`
	Agent   string `wick:"desc=The agent's @handle or id (move)."`
	Folder  string `wick:"desc=Folder name. move: the folder to put the agent in (created when missing), empty = take it out of any folder. rename and delete: the folder to change."`
	NewName string `wick:"desc=The folder's new name (rename)."`
	Order   string `wick:"desc=Comma-separated folder names, first to last (order). Folders not named keep their order after them."`
}

type deleteInput struct {
	Agent      string `wick:"required;desc=The agent's @handle or id."`
	Confirm    string `wick:"required;desc=The agent's handle again (e.g. @client-alpha), as a confirmation."`
	Mode       string `wick:"dropdown=delete|disable;desc=delete (default): the agent is removed for good. disable: it stays, turned off (the owner can turn it on again)."`
	PurgeChats bool   `wick:"desc=delete only: also delete the agent's project with its chats and files. false (default) keeps them as an ordinary project."`
}

// Operations lists the connector's ops.
func Operations(deps Deps) []connector.Category {
	return []connector.Category{
		connector.Cat("Agents", "Manage the other agents of the Team.",
			connector.Op("list", "List Team Agents",
				"Status of every agent of the Team: id, handle, name, tagline, description, remote (with its agent_description), captain, disabled, status (idle or working), current_action, needs_attention, unread, last_error.",
				listInput{}, deps.list, wickdocs.Docs{}),
			connector.OpDestructive("create", "Create an Agent",
				"Create a new agent with NO access (deny by default) and only the read-only native tools. access_suggestions and native_tools (e.g. Bash, Edit, Write) are sent to the owner as one approval request; nothing is granted until they accept, unless the owner set the Captain to auto-approve.",
				createInput{}, deps.create, wickdocs.Docs{}),
			connector.OpDestructive("update_persona", "Edit an Agent's Persona",
				"Change another agent's name, tagline, description, system prompt, default provider/model or avatar (shape/colour/blob mascot, or a picture file from your working folder). For a remote agent the system prompt is its agent description (what it does, when to call it); nothing is sent to the remote. Refused when that agent does not let the Captain edit its persona.",
				personaInput{}, deps.updatePersona, wickdocs.Docs{}),
			connector.OpDestructive("set_access", "Propose an Access Change",
				"Ask the owner to change another agent's connector access and/or native tools (Bash, Edit, Write…). Returns pending_approval: the owner accepts or declines on a card, and the change applies only then — or status applied when the owner set the Captain to auto-approve. Can never exceed the owner's own access. Refused when that agent does not let the Captain propose access changes.",
				accessInput{}, deps.setAccess, wickdocs.Docs{}),
			connector.OpDestructive("schedule", "Manage an Agent's Schedules",
				"List, create, update, pause or resume the schedules in another agent's Scheduled drawer. New schedules fire into that agent's main chat. Applies at once and is announced in that agent's chat. Refused when that agent does not let the Captain manage its routines.",
				scheduleInput{}, deps.schedule, wickdocs.Docs{}),
			connector.OpDestructive("folders", "Organize Sidebar Folders",
				"List or change the owner's Team-sidebar folders: list (folders with their agents, and the loose ones), move an agent into a folder (created when missing) or out of all, rename, delete (its agents become loose) or order folders. Changes only the owner's own sidebar, silently. Refused when the owner switched Captain folder organizing off.",
				foldersInput{}, deps.folders, wickdocs.Docs{}),
			connector.OpDestructive("delete", "Delete or Disable an Agent",
				"Remove another agent of the Team (mode=delete) or turn it off (mode=disable). Only when the owner (or an admin) is the person of this chat, never a Captain, never yourself, and confirm repeating its handle. Refused while any chat of that agent is running or queued: try again once it is idle. Do it only when the owner asked for it.",
				deleteInput{}, deps.delete, wickdocs.Docs{}),
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
	tools, err := ParseNativeTools(c.Input("native_tools"))
	if err != nil {
		return nil, err
	}
	return ops.Create(c.Context(), c.SessionID(), CreateInput{
		Name: strings.TrimSpace(c.Input("name")), Handle: c.Input("handle"),
		Tagline: c.Input("tagline"), Description: c.Input("description"),
		SystemPrompt: c.Input("system_prompt"), Avatar: strings.TrimSpace(c.Input("avatar")),
		AccessSuggestions: sugg, NativeTools: tools,
		Provider: strings.TrimSpace(c.Input("provider")), Model: strings.TrimSpace(c.Input("model")),
		Folder: strings.TrimSpace(c.Input("folder")),
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
		Avatar: opt("avatar"), AvatarImage: opt("avatar_image"),
		Provider: opt("provider"), Model: opt("model"),
		ForceDefaultProvider: opt("force_default_provider"),
	})
}

func (d Deps) setAccess(c *connector.Ctx) (any, error) {
	ops, err := d.ops()
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(c.Input("grants"))
	gs, err := ParseGrants(raw)
	if err != nil {
		return nil, err
	}
	if raw != "" && gs == nil {
		// "[]" is a real proposal (remove every connector), not "keep".
		gs = []team.ConnectorGrant{}
	}
	tools, err := ParseNativeTools(c.Input("native_tools"))
	if err != nil {
		return nil, err
	}
	if gs == nil && tools == nil {
		return nil, errors.New("give grants, native_tools or both")
	}
	return ops.SetAccess(c.Context(), c.SessionID(), AccessInput{Agent: c.Input("agent"), Grants: gs, NativeTools: tools, Reason: c.Input("reason")})
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

func (d Deps) folders(c *connector.Ctx) (any, error) {
	ops, err := d.ops()
	if err != nil {
		return nil, err
	}
	var order []string
	for _, n := range strings.Split(c.Input("order"), ",") {
		if n = strings.TrimSpace(n); n != "" {
			order = append(order, n)
		}
	}
	return ops.Folders(c.Context(), c.SessionID(), FoldersInput{
		Action: strings.TrimSpace(strings.ToLower(c.Input("action"))), Agent: strings.TrimSpace(c.Input("agent")),
		Folder: strings.TrimSpace(c.Input("folder")), NewName: strings.TrimSpace(c.Input("new_name")), Order: order,
	})
}

func (d Deps) delete(c *connector.Ctx) (any, error) {
	ops, err := d.ops()
	if err != nil {
		return nil, err
	}
	return ops.Delete(c.Context(), c.SessionID(), DeleteInput{
		Agent: strings.TrimSpace(c.Input("agent")), Confirm: strings.TrimSpace(c.Input("confirm")),
		Mode: strings.TrimSpace(strings.ToLower(c.Input("mode"))), PurgeChats: c.InputBool("purge_chats"),
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

// ParseNativeTools reads a native-tool list, comma-separated or a JSON
// array, names as team.NativeTools spells them (any case); "" is nil.
func ParseNativeTools(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var in []string
	if strings.HasPrefix(raw, "[") {
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			return nil, errors.New("native_tools must be a JSON array of names or a comma-separated list: " + err.Error())
		}
	} else {
		in = strings.Split(raw, ",")
	}
	out := []string{}
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		name := ""
		for _, n := range team.NativeTools {
			if strings.EqualFold(n, t) {
				name = n
			}
		}
		if name == "" {
			return nil, errors.New("unknown native tool " + strconv.Quote(t) + "; use " + strings.Join(team.NativeTools, ", "))
		}
		out = append(out, name)
	}
	return team.NormalizeNativeTools(out), nil
}
