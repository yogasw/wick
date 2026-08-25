package ticket

import (
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
)

// Event names. These strings are a public contract: they go out on the wire
// in X-Wick-Event, they are what a webhook filters on, and they are stored
// in project config. Renaming one silently breaks every receiver, so treat
// them as append-only.
const (
	EventCreated         = "ticket.created"
	EventUpdated         = "ticket.updated"
	EventStatusChanged   = "ticket.status_changed"
	EventAssigned        = "ticket.assigned"
	EventDeleted         = "ticket.deleted"
	EventSessionAttached = "ticket.session_attached"
	EventSessionDetached = "ticket.session_detached"
	EventNoteAdded       = "ticket.note_added"
	EventFollowup        = "ticket.followup"
	EventAutoResolved    = "ticket.auto_resolved"
)

// AllEvents is the catalogue the settings UI offers and the docs list, in a
// sensible reading order rather than alphabetically.
func AllEvents() []string {
	return []string{
		EventCreated,
		EventUpdated,
		EventStatusChanged,
		EventAssigned,
		EventDeleted,
		EventSessionAttached,
		EventSessionDetached,
		EventNoteAdded,
		EventFollowup,
		EventAutoResolved,
	}
}

// ValidEvent reports whether name is a known event. Used when saving a
// webhook so a typo'd filter is refused instead of silently matching
// nothing forever.
func ValidEvent(name string) bool {
	for _, e := range AllEvents() {
		if e == name {
			return true
		}
	}
	return false
}

// Actor types. Who moved the ticket matters to a receiver: a human dragging
// a card and the stale-followup sweeper closing one are different signals,
// and a receiver that echoes changes back needs to recognise its own writes.
const (
	ActorUser   = "user"
	ActorAgent  = "agent"
	ActorSystem = "system"
	ActorAPI    = "api"
)

// Actor identifies who caused an event.
type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// Change is one field's before/after. Values are strings because every
// ticket field that can change is already a string on the wire; a typed
// union would buy nothing a receiver can use.
type Change struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Event is the webhook envelope.
//
// The full ticket rides along on every event, not just the diff: a receiver
// that missed an earlier delivery (or starts up mid-stream) can still act on
// the current state without calling back for it.
type Event struct {
	ID          string            `json:"id"`
	Event       string            `json:"event"`
	DeliveredAt time.Time         `json:"delivered_at"`
	ProjectID   string            `json:"project_id"`
	Actor       Actor             `json:"actor"`
	Ticket      Ticket            `json:"ticket"`
	Changes     map[string]Change `json:"changes,omitempty"`
	// Session carries the session id for the attach/detach events, where
	// the interesting subject is the link rather than the ticket.
	Session string `json:"session,omitempty"`
	// Note carries the note body for ticket.note_added.
	Note string `json:"note,omitempty"`
}

// Emitter delivers a ticket event. Implemented by the webhook dispatcher and
// injected via SetEmitter.
//
// This indirection keeps the ticket package free of HTTP, project-registry,
// and config-service imports: ticket is the storage layer that the whole app
// depends on, so making it reach outward for the project's webhook list
// would create an import cycle. Same shape as the SetManager/SetLayout
// wiring in tools/agents.
type Emitter interface {
	Emit(ev Event)
}

// emitter is the process-wide sink. nil means "nobody is listening", which
// is the correct state in every test and in a CLI invocation — emit calls
// then cost one nil check.
var emitter Emitter

// SetEmitter installs the process-wide event sink. Called once at boot.
func SetEmitter(e Emitter) { emitter = e }

// emit hands ev to the installed emitter, stamping the fields every event
// shares. Non-blocking by contract: the dispatcher fans out in its own
// goroutines, so a ticket write never waits on a slow endpoint.
func emit(ev Event) {
	if emitter == nil {
		return
	}
	if ev.ID == "" {
		// Reuse the ticket ID generator: a delivery ID only has to be
		// unique enough for a receiver to dedupe on.
		if id, err := newID(); err == nil {
			ev.ID = "evt_" + strings.TrimPrefix(id, "T-")
		}
	}
	if ev.DeliveredAt.IsZero() {
		ev.DeliveredAt = time.Now().UTC()
	}
	if ev.ProjectID == "" {
		ev.ProjectID = ev.Ticket.ProjectID
	}
	emitter.Emit(ev)
}

// orSystem fills in the actor type for a write that did not name one.
//
// There is deliberately no package-level "current actor": that would be
// wrong under concurrent HTTP requests, so who is acting travels with the
// call and an unnamed actor reports honestly as the system.
func (a Actor) orSystem() Actor {
	if a.Type == "" {
		return Actor{Type: ActorSystem}
	}
	return a
}

// diff computes the field-level changes between two versions of a ticket.
// Only the fields a receiver can act on are compared — timestamps move on
// every write and would make every diff non-empty.
func diff(before, after Ticket) map[string]Change {
	out := map[string]Change{}
	if before.Status != after.Status {
		out["status"] = Change{From: before.Status, To: after.Status}
	}
	if before.Assignee != after.Assignee {
		out["assignee"] = Change{From: before.Assignee, To: after.Assignee}
	}
	if before.Title != after.Title {
		out["title"] = Change{From: before.Title, To: after.Title}
	}
	for k, v := range after.Fields {
		if before.Fields[k] != v {
			out["fields."+k] = Change{From: before.Fields[k], To: v}
		}
	}
	// A field cleared out of the map is still a change.
	for k, v := range before.Fields {
		if _, still := after.Fields[k]; !still {
			out["fields."+k] = Change{From: v, To: ""}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// EventsFor decides which events a before→after transition should fire.
//
// A status move emits BOTH ticket.status_changed and ticket.updated: a
// receiver that only cares about board movement subscribes to the specific
// one, while a receiver mirroring all edits subscribes to updated and does
// not have to enumerate every specific event to stay complete.
func EventsFor(changes map[string]Change) []string {
	if len(changes) == 0 {
		return nil
	}
	out := []string{EventUpdated}
	if _, ok := changes["status"]; ok {
		out = append(out, EventStatusChanged)
	}
	if _, ok := changes["assignee"]; ok {
		out = append(out, EventAssigned)
	}
	return out
}

// EmitNoteAdded fires ticket.note_added.
//
// Exported because notes are stored by their own package: the note write does
// not pass through this one, so the HTTP layer reports it. A ticket that
// cannot be loaded is skipped rather than failing the note.
func EmitNoteAdded(layout config.Layout, projectID, ticketID, body string, actor Actor) {
	if emitter == nil || ticketID == "" {
		return
	}
	tk, err := Load(layout, projectID, ticketID)
	if err != nil {
		return
	}
	emit(Event{Event: EventNoteAdded, Ticket: tk, Note: body, Actor: actor.orSystem()})
}

// EmitFollowup fires ticket.followup — the sweeper nudged a stale ticket.
func EmitFollowup(tk Ticket) {
	emit(Event{Event: EventFollowup, Ticket: tk, Actor: Actor{Type: ActorSystem}})
}

// EmitAutoResolved fires ticket.auto_resolved, naming the stage the sweeper
// moved the ticket out of.
func EmitAutoResolved(tk Ticket, from string) {
	emit(Event{
		Event:   EventAutoResolved,
		Ticket:  tk,
		Actor:   Actor{Type: ActorSystem},
		Changes: map[string]Change{"status": {From: from, To: tk.Status}},
	})
}

// WebhookTarget is one resolved delivery: the endpoint plus the event that
// matched it. The dispatcher takes these rather than a TicketConfig so the
// matching rules stay testable without a project on disk.
type WebhookTarget struct {
	Webhook project.TicketWebhook
	Event   Event
}
