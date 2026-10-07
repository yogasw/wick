package remote

// SchemaVersion is the version of the Event schema every adapter speaks;
// each adapter names it in its Adapter.Schema and Register refuses any
// other. v1 is the EventKind set and Event fields below. Adding an
// optional field is still v1; renaming or removing one, or changing what
// a kind means, is v2 and every adapter moves with it.
const SchemaVersion = 1

// EventKind names what an Event carries.
type EventKind string

const (
	// EventTextDelta appends Text to the reply.
	EventTextDelta EventKind = "text_delta"
	// EventText is the reply so far in full; the runner shows only the
	// part not shown yet.
	EventText EventKind = "text"
	// EventDraft is the reply so far in full, not shown yet: the remote
	// may still rewrite it, so the runner keeps it as the turn's result
	// and shows it when the turn ends. Added within v1: a runner that
	// does not know it only shows the reply later.
	EventDraft EventKind = "draft"
	// EventStatus reports what the remote is doing: Status is one of the
	// Status* values, Detail a short label ("Bash: ls").
	EventStatus EventKind = "status"
	// EventAttachment is a file the remote sent: Name, URL, MIME.
	EventAttachment EventKind = "attachment"
	// EventDone ends the turn. Text, when set, is the whole reply; Note
	// labels how it ended ("ended without marker").
	EventDone EventKind = "done"
	// EventError ends the turn as failed; Text is the message.
	EventError EventKind = "error"
)

// Status values of an EventStatus.
const (
	StatusThinking      = "thinking"
	StatusWorking       = "working"
	StatusInputRequired = "input_required"
	// StatusIdle: the remote no longer shows it is working (an hourglass
	// or assistant status went away). Not shown; it lets the idle window
	// run again.
	StatusIdle = "idle"
	// StatusForwarded: a message sent while the turn ran was handed to
	// the remote (Injector); the runner emits it, never a source.
	StatusForwarded = "forwarded"
)

// Event is one thing a remote did during a turn, the same shape for every
// adapter, so chat, mention, schedule and the Slack bridge never learn
// where the agent lives.
type Event struct {
	Kind   EventKind `json:"kind"`
	Text   string    `json:"text,omitempty"`
	Status string    `json:"status,omitempty"`
	Detail string    `json:"detail,omitempty"`
	Name   string    `json:"name,omitempty"`
	URL    string    `json:"url,omitempty"`
	MIME   string    `json:"mime,omitempty"`
	Note   string    `json:"note,omitempty"`
}

// Terminal reports whether e ends the turn.
func (e Event) Terminal() bool { return e.Kind == EventDone || e.Kind == EventError }

// NoteNoMarker labels a turn that went quiet without its end marker.
const NoteNoMarker = "ended without marker"

// NoteFollowUp labels a reply the remote sent or edited after its turn
// had ended, inside the grace window.
const NoteFollowUp = "follow-up"

// NoteLate labels a reply the remote finished after its turn had timed
// out: a late answer to the same request, not a new message.
const NoteLate = "late reply"
