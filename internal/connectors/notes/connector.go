// Package notes exposes wick's notes as a fixed single-instance MCP
// connector.
//
// Notes carry knowledge across sessions. A session attached to a ticket
// reads and writes the TICKET's notes, so opening a fresh conversation on
// the same ticket picks up what the last one learned; a session attached
// to nothing keeps its own.
//
// Deliberately separate from the tickets connector: notes work without
// tickets at all, so an agent can be granted note-taking with no access
// to the ticket board, and a project that never enables ticket mode still
// gets notes.
//
// Note bodies are never injected into the system prompt — a long-lived
// ticket would then charge its whole history on every turn. Sessions get a
// one-line pointer and the agent reads through these ops on demand.
//
// File layout:
//
//   - connector.go — Meta, Input structs, Operations, handler struct
//   - ops.go       — handler implementations
//
// Wire-up: connectors.Register(notes.Module(layout)) before
// connectors.Service.Bootstrap.
package notes

import (
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
	"github.com/yogasw/wick/pkg/wickdocs"
)

const Key = "notes"

// Configs is intentionally empty — notes live on wick's own disk.
type Configs struct{}

// Meta returns the static metadata block.
func Meta() connector.Meta {
	return connector.Meta{
		Key:  Key,
		Name: "Notes",
		Description: "Read and write notes attached to a ticket or a session — the running record of what has been " +
			"learned, so a later session (or a person) can pick the work up without re-deriving it.",
		Icon:  "📝",
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
	return []connector.Category{connector.Cat("Notes",
		"Notes attached to a ticket or a session. Scope defaults to the calling session, which resolves to its ticket's "+
			"notes when it belongs to one — so notes written in one session are visible to the next.",
		connector.Op("notes_list", "List Notes",
			"List the scope's notes, oldest first. Each carries: id, body (markdown), audience (who it was written for: "+
				"ai, human, or both), checkable/done, author, and timestamps. "+
				"READ THIS FIRST when picking up work — it is where the previous session left its findings. "+
				"Notes hidden by a user are never returned.",
			scopeInput{}, h.list, wickdocs.Docs{}),
		connector.Op("notes_add", "Add Note",
			"Add a note to the scope. Write one whenever you learn something a later session would otherwise have to "+
				"rediscover: a root cause, a dead end, a decision and its reason, or what to do next. "+
				"Set checkable=true for something with a done state (\"verified the fix on staging\"). "+
				"audience says who it is for — 'human' when it is a handover message, 'ai' for a hint to yourself, "+
				"'both' (the default) when either would want it.",
			addInput{}, h.add, wickdocs.Docs{}),
		connector.Op("notes_update", "Update Note",
			"Edit an existing note's body, audience, or checkable flag. Use it to sharpen a vague note rather than "+
				"piling another one on top — including notes written for humans, whose wording you can improve.",
			updateInput{}, h.update, wickdocs.Docs{}),
		connector.Op("notes_check", "Check Note",
			"Mark a checkable note done, or clear it. Only works on notes created with checkable=true.",
			checkInput{}, h.check, wickdocs.Docs{}),
		// Destructive: a deleted note is gone, and notes are the only
		// record of why earlier decisions were made. Off by default.
		connector.OpDestructive("notes_delete", "Delete Note",
			"Delete a note for good. DESTRUCTIVE and not reversible — prefer editing a note that is merely wrong, and "+
				"leave notes that are simply old alone: they are the history of the work.",
			idInput{}, h.del, wickdocs.Docs{}),
	)}
}

/* ── Inputs ──────────────────────────────────────────────────────────── */

// scopeInput is the shared scope selector. All three fields optional: with
// none set the calling session decides, which is the common case.
type scopeInput struct {
	TicketID  string `wick:"desc=Notes on this ticket (e.g. T-4F2A). Defaults to the calling session's scope."`
	SessionID string `wick:"desc=Notes on this session. Ignored when ticket_id is set."`
	ProjectID string `wick:"desc=Project the ticket belongs to. Defaults to the calling session's project."`
}

type addInput struct {
	Body      string `wick:"required;textarea;desc=The note, in markdown. Be specific enough to be useful without this conversation."`
	Checkable bool   `wick:"desc=Render as a checkbox with a done state."`
	Audience  string `wick:"dropdown=both|ai|human;desc=Who the note is for. Defaults to both."`
	TicketID  string `wick:"desc=Attach to this ticket. Defaults to the calling session's scope."`
	SessionID string `wick:"desc=Attach to this session. Ignored when ticket_id is set."`
	ProjectID string `wick:"desc=Project the ticket belongs to. Defaults to the calling session's project."`
}

type updateInput struct {
	NoteID    string `wick:"required;desc=Note id from notes_list."`
	Body      string `wick:"textarea;desc=New body. Omit to leave unchanged."`
	Audience  string `wick:"dropdown=both|ai|human;desc=New audience. Omit to leave unchanged."`
	Checkable string `wick:"dropdown=true|false;desc=Turn the done state on or off. Omit to leave unchanged."`
	TicketID  string `wick:"desc=Ticket the note is on. Defaults to the calling session's scope."`
	SessionID string `wick:"desc=Session the note is on. Ignored when ticket_id is set."`
	ProjectID string `wick:"desc=Project the ticket belongs to. Defaults to the calling session's project."`
}

type checkInput struct {
	NoteID    string `wick:"required;desc=Note id from notes_list."`
	Done      bool   `wick:"desc=true marks it done, false clears it."`
	TicketID  string `wick:"desc=Ticket the note is on. Defaults to the calling session's scope."`
	SessionID string `wick:"desc=Session the note is on. Ignored when ticket_id is set."`
	ProjectID string `wick:"desc=Project the ticket belongs to. Defaults to the calling session's project."`
}

type idInput struct {
	NoteID    string `wick:"required;desc=Note id from notes_list."`
	TicketID  string `wick:"desc=Ticket the note is on. Defaults to the calling session's scope."`
	SessionID string `wick:"desc=Session the note is on. Ignored when ticket_id is set."`
	ProjectID string `wick:"desc=Project the ticket belongs to. Defaults to the calling session's project."`
}

// handlers carries the layout every op needs.
type handlers struct {
	layout agentconfig.Layout
}
