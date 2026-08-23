// Package tickets exposes wick's ticket entities as a fixed
// single-instance MCP connector.
//
// A ticket is the unit of work; a session is one conversation about it,
// and a ticket can hold several. That is what lets you abandon a session
// an agent has derailed and open a fresh one on the same ticket, keeping
// its status, assignee, fields, and notes.
//
// Notes are a SEPARATE connector (internal/connectors/notes): a session
// with no ticket still keeps notes, so note-taking must be grantable
// without any access to the ticket board.
//
// File layout:
//
//   - connector.go — Meta, Input structs, Operations, handler struct
//   - ops.go       — handler implementations
//
// Wire-up: connectors.Register(tickets.Module(layout)) before
// connectors.Service.Bootstrap.
package tickets

import (
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
	"github.com/yogasw/wick/pkg/wickdocs"
)

const Key = "tickets"

// Configs is intentionally empty — tickets live on wick's own disk, not
// behind an external API.
type Configs struct{}

// Meta returns the static metadata block.
func Meta() connector.Meta {
	return connector.Meta{
		Key:  Key,
		Name: "Tickets",
		Description: "Read and manage wick tickets: the unit of work a project's sessions hang off. " +
			"One ticket can hold several sessions, so a fresh conversation can continue existing work.",
		Icon:  "🎫",
		Fixed: true,
	}
}

// Module returns the fully-wired connector.Module.
func Module(layout agentconfig.Layout) connector.Module {
	m := Meta()
	m.DefaultTags = []tool.DefaultTag{tags.Connector, tags.Platform}
	return connector.Module{
		Meta:       m,
		Operations: Operations(layout),
	}
}

// Operations builds the op list, capturing layout so handlers can reach
// the on-disk store.
func Operations(layout agentconfig.Layout) []connector.Category {
	h := &handlers{layout: layout}
	return []connector.Category{connector.Cat("Tickets",
		"Inspect and manage tickets. A ticket carries status, assignee, project-defined fields, and the sessions working on it.",
		connector.Op("ticket_list", "List Tickets",
			"List a project's tickets, newest first: id, title, status, assignee, fields, and how many sessions each holds. "+
				"Omit project_id to use the project of the calling session. "+
				"Filter by status, and by who a ticket is assigned to: pass mine=true for \"my tickets\" / \"what am I assigned\" "+
				"(the caller is resolved from the credential — do NOT pass a user id for this), "+
				"assignee=<user id> for a named person's queue, or assignee=unassigned for tickets nobody owns. "+
				"Combine with status, e.g. mine=true + status=in_progress for \"what am I working on\". "+
				"The response echoes the filters applied plus counts per status, so a follow-up question needs no second call.",
			listInput{}, h.list, wickdocs.Docs{}),
		connector.Op("ticket_mine", "My Tickets",
			"List tickets assigned to the CALLER — use this for \"my tickets\", \"what am I assigned\", "+
				"\"what am I working on\". The user is resolved from the credential, so no user id is needed "+
				"or accepted; you cannot read someone else's queue with this. "+
				"Optionally filter by status (e.g. status=in_progress for work in flight). "+
				"Returns count_by_status alongside the list, so \"how many of mine are open\" needs no second call. "+
				"For another person's queue use ticket_list with assignee=<user id>; "+
				"for tickets nobody owns use ticket_list with assignee=unassigned.",
			mineInput{}, h.mine, wickdocs.Docs{}),
		connector.Op("session_untracked", "Untracked Sessions",
			"List conversations in a project that are NOT attached to any ticket — work in progress that was never "+
				"turned into tracked work. Distinct from an unassigned TICKET (a ticket with no assignee): "+
				"these have no ticket at all. "+
				"Use it to answer \"what have I not filed yet\" or to find chats worth promoting with "+
				"ticket_create(attach_current_session=true) or ticket_attach_session. "+
				"Pass mine=true to see only the caller's own sessions.",
			untrackedInput{}, h.untracked, wickdocs.Docs{}),
		connector.Op("ticket_get", "Get Ticket",
			"Return one ticket in full, including its session list. "+
				"Omit ticket_id to get the ticket the calling session belongs to — the usual way to answer \"what am I working on?\".",
			getInput{}, h.get, wickdocs.Docs{}),
		connector.Op("ticket_create", "Create Ticket",
			"Create a ticket in a project. Status defaults to open. "+
				"Pass attach_current_session=true to attach the calling session to it, which is how an ad-hoc chat becomes tracked work.",
			createInput{}, h.create, wickdocs.Docs{}),
		connector.Op("ticket_update", "Update Ticket",
			"Update a ticket's title, status, assignee, or fields. Only what you pass changes. "+
				"Status must be open, in_progress, waiting, or done. "+
				"Every update resets the project's stale-followup and auto-resolve timers, so call this after acting on a follow-up.",
			updateInput{}, h.update, wickdocs.Docs{}),
		connector.Op("ticket_attach_session", "Attach Session",
			"Attach a session to a ticket, so its conversation counts as work on that ticket and it reads the ticket's notes. "+
				"Idempotent. Omit session_id to attach the calling session.",
			attachInput{}, h.attach, wickdocs.Docs{}),
		connector.Op("ticket_settings_get", "Get Ticket Settings",
			"Read a project's ticket configuration: whether ticket mode is on, the custom field schema, "+
				"the stale-followup and auto-resolve windows, and the auto-create rules that decide which new "+
				"sessions get a ticket without anyone asking. Omit project_id to read the calling session's project.",
			settingsGetInput{}, h.settingsGet, wickdocs.Docs{}),
		connector.Op("ticket_settings_set", "Set Ticket Settings",
			"Change a project's ticket configuration. Only what you pass changes. "+
				"auto_create is a JSON array of rules, each {origin, channel_kind, match, title, enabled}: "+
				"origin is \"ui\", \"slack\", \"telegram\", \"rest\" or \"*\"; channel_kind narrows a channel origin to "+
				"\"dm\", \"channel\" or \"thread\"; match is empty, \"contains:<text>\", or \"regex:<expr>\" tested against the "+
				"session's first message. Rules are tried in order and the FIRST match wins, so a disabled narrow rule "+
				"above a broad one carves an exception out of it — that is how \"everything from Slack except DMs\" is written. "+
				"A regex that does not compile is refused rather than stored inert.",
			settingsSetInput{}, h.settingsSet, wickdocs.Docs{}),
		connector.Op("ticket_detach_session", "Detach Session",
			"Detach a session from a ticket. The session and its history survive; it simply stops belonging to that ticket "+
				"and goes back to its own notes. Omit session_id to detach the calling session.",
			attachInput{}, h.detach, wickdocs.Docs{}),
	)}
}

/* ── Inputs ──────────────────────────────────────────────────────────── */

type listInput struct {
	ProjectID string `wick:"desc=Project whose tickets to list. Defaults to the calling session's project."`
	Status    string `wick:"dropdown=open|in_progress|waiting|done;desc=Optional status filter."`
	// Mine answers "my tickets" without the model having to know, or guess, a
	// user id. It resolves to the caller server-side, so the model cannot ask
	// for somebody else's list by passing an id it made up.
	Mine bool `wick:"desc=Only tickets assigned to the caller. Use this for \"my tickets\" — the user is resolved from the credential, so no id is needed."`
	// Assignee is for looking at a NAMED person's queue, which is a different
	// question from "mine" and needs an explicit id.
	Assignee string `wick:"desc=Only tickets assigned to this wick user id. Leave empty for everyone; prefer mine=true for the caller's own tickets. Pass 'unassigned' for tickets with nobody on them."`
}

type mineInput struct {
	ProjectID string `wick:"desc=Project whose tickets to list. Defaults to the calling session's project."`
	Status    string `wick:"dropdown=open|in_progress|waiting|done;desc=Optional status filter."`
}

type untrackedInput struct {
	ProjectID string `wick:"desc=Project whose sessions to scan. Defaults to the calling session's project."`
	// Owner-scoping is opt-in rather than automatic: an operator asking "what is
	// untracked in this project" wants the whole project, and silently hiding
	// other people's sessions would answer a narrower question than was asked.
	Mine  bool `wick:"desc=Only sessions owned by the caller. Off = every untracked session in the project."`
	Limit int  `wick:"desc=Max sessions to return, newest first. Default 50."`
}

type getInput struct {
	TicketID  string `wick:"desc=Ticket id (e.g. T-4F2A). Defaults to the ticket the calling session belongs to."`
	ProjectID string `wick:"desc=Project the ticket belongs to. Defaults to the calling session's project."`
}

type createInput struct {
	Title     string `wick:"required;desc=Short description of the work. Example: Payment webhook returns 401"`
	ProjectID string `wick:"desc=Project to create the ticket in. Defaults to the calling session's project."`
	Status    string `wick:"dropdown=open|in_progress|waiting|done;desc=Initial status. Defaults to open."`
	Assignee  string `wick:"desc=Optional wick user id to assign it to."`
	Fields    string `wick:"textarea;desc=Optional project-defined field values as JSON. Example: {\"priority\":\"high\",\"type\":\"incident\"}"`
	// Named "attach_current_session" rather than a bare boolean so the
	// agent cannot attach a session it did not mean to.
	AttachCurrentSession bool `wick:"desc=Attach the calling session to the new ticket."`
}

type updateInput struct {
	TicketID  string `wick:"desc=Ticket id to update. Defaults to the ticket the calling session belongs to."`
	ProjectID string `wick:"desc=Project the ticket belongs to. Defaults to the calling session's project."`
	Title     string `wick:"desc=New title. Omit to leave unchanged."`
	Status    string `wick:"dropdown=open|in_progress|waiting|done;desc=New status. Omit to leave unchanged."`
	Assignee  string `wick:"desc=New assignee (wick user id). Pass an empty string to unassign."`
	Fields    string `wick:"textarea;desc=Field values to merge as JSON. An empty string value clears that field."`
}

type settingsGetInput struct {
	ProjectID string `wick:"desc=Project whose ticket settings to read. Defaults to the calling session's project."`
}

type settingsSetInput struct {
	ProjectID string `wick:"desc=Project to configure. Defaults to the calling session's project."`
	Enabled   string `wick:"dropdown=true|false;desc=Turn ticket mode on or off. Omit to leave unchanged."`
	// Durations are minutes/days here rather than the stored seconds: an
	// agent writing "60" for an hour is far likelier to be right than one
	// converting to 3600.
	FollowupAfterMinutes string `wick:"desc=Wake the agent when a ticket has been untouched this long, in minutes. 0 turns it off. Omit to leave unchanged."`
	AutoResolveAfterDays string `wick:"desc=Close a ticket untouched this long, in days. 0 turns it off. Omit to leave unchanged."`
	FollowupPrompt       string `wick:"textarea;desc=Instruction sent to the agent when a ticket goes stale. Omit to leave unchanged."`
	AutoCreate           string `wick:"textarea;desc=JSON array of auto-create rules. Pass [] to remove them all. Omit to leave unchanged."`
	Statuses             string `wick:"textarea;desc=JSON array of board statuses, in column order: [{\"key\":\"triage\",\"label\":\"Triage\"},{\"key\":\"shipped\",\"label\":\"Shipped\",\"terminal\":true}]. Keys are lowercase a-z0-9_ and are what tickets store; labels are display only. EXACTLY ONE must have terminal:true — that is the stage auto-resolve moves finished work to. Pass [] to return to the built-in set. Omit to leave unchanged."`
}

type attachInput struct {
	TicketID  string `wick:"required;desc=Ticket id."`
	SessionID string `wick:"desc=Session to attach or detach. Defaults to the calling session."`
	ProjectID string `wick:"desc=Project the ticket belongs to. Defaults to the calling session's project."`
}

// handlers carries the layout every op needs.
type handlers struct {
	layout agentconfig.Layout
}
